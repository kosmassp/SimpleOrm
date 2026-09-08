package sqlite

// Shadow rebuilds the versioned schema snapshots (V000N.schema.json) by
// replaying the migration history into a throwaway temp-file database and
// introspecting the touched relations after each version (ADR-0017), mirroring
// dotnet/src/SimpleOrm.Sqlite/SqliteShadow.cs and
// php/src/Dialect/SqliteShadow.php: the model-vs-history integrity check of
// spec/migrations.md — a shadow rebuild must reproduce every committed
// snapshot byte-for-byte modulo generatedAt.
//
// PHP adaptation carried over here (CODING-STANDARD §10): the C# reference
// resolves each touched relation back to its mapped entity by scanning the
// whole assembly. Go has no assembly to scan either, so this loads every
// entity in Entities up front and indexes it by relation name — the same
// direction PHP's port took (there, from the step's own entityClass()).

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/metadata"
	"github.com/kosmassp/SimpleOrm/go/orm/migrations"
)

// ShadowOptions configures [Shadow]: the migration set to replay, every
// entity Shadow may need to resolve a touched relation to its mapped type,
// the migrations tree's directory (both the trusted baseline source when
// From > 0 and the destination for regenerated snapshots), and the version
// range to regenerate. To == 0 means unbounded (regenerate through the newest
// version in Set).
type ShadowOptions struct {
	Set      *migrations.Set
	Entities []reflect.Type
	OutDir   string
	From     int64
	To       int64
}

// ShadowResult is [Shadow]'s outcome: files written and progress notes, in
// the order the replay produced them.
type ShadowResult struct {
	WrittenFiles []string
	Notes        []string
}

// Shadow replays Set's versions into a throwaway database opened through
// New().CreateConnection, deleted (with its -wal/-shm/-journal siblings) when
// it returns. With From > 0 the trusted baseline is restored from the
// committed snapshots under OutDir at <= From and those versions are
// baselined (never verified); every version in (From, To] then migrates in
// turn, and each object it touches (per Status at that version) is
// introspected and its snapshot rewritten.
func Shadow(ctx context.Context, options ShadowOptions) (*ShadowResult, error) {
	result := &ShadowResult{}
	if options.Set == nil || len(options.Set.Versions()) == 0 {
		result.Notes = append(result.Notes, "no migration versions found")
		return result, nil
	}

	entityByRelation, err := mapEntitiesByRelation(options.Entities)
	if err != nil {
		return nil, err
	}

	shadowFile := filepath.Join(os.TempDir(), "simpleorm-shadow-"+randomHex(16)+".db")
	defer func() {
		// Best effort: Windows can briefly hold a delete lock after the driver closes its handle.
		_ = os.Remove(shadowFile)
		_ = os.Remove(shadowFile + "-wal")
		_ = os.Remove(shadowFile + "-shm")
		_ = os.Remove(shadowFile + "-journal")
	}()

	dialect := New()
	pool, err := dialect.CreateConnection("Data Source=" + shadowFile)
	if err != nil {
		return nil, err
	}
	defer pool.Close()

	conn, err := pool.Conn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	maps := metadata.NewLoader(nil)
	generatedAt := time.Now().UTC()

	if options.From > 0 {
		if err := restoreBaseline(ctx, conn, options.OutDir, options.From, result); err != nil {
			return nil, err
		}
		baselineRunner := migrations.NewRunner(pool, dialect, maps, options.Set, nil)
		if err := baselineRunner.Baseline(ctx, options.From); err != nil {
			return nil, err
		}
	}

	allVersions := options.Set.Versions()
	allNumbers := options.Set.VersionNumbers()
	for i, number := range allNumbers {
		if number <= options.From {
			continue
		}
		if options.To != 0 && number > options.To {
			continue
		}

		// The runner for this step knows only versions <= number (this
		// version's steps must be exactly what shows Pending): a runner
		// holding the whole set would apply every later version too.
		subset, err := migrations.NewSet(allVersions[:i+1]...)
		if err != nil {
			return nil, err
		}
		runner := migrations.NewRunner(pool, dialect, maps, subset, nil)

		statuses, err := runner.Status(ctx)
		if err != nil {
			return nil, err
		}
		touched := touchedObjects(statuses, number)

		if _, err := runner.Migrate(ctx, migrations.RunOptions{}); err != nil {
			return nil, err
		}

		for _, relation := range touched {
			entity, ok := entityByRelation[strings.ToLower(relation)]
			if !ok {
				result.Notes = append(result.Notes,
					fmt.Sprintf("V%04d: '%s' has no mapped entity; snapshot skipped", number, relation))
				continue
			}

			var content string
			if entity.kind == core.RelationTable {
				schema, err := introspectTable(ctx, conn, dialect, relation)
				if err != nil {
					return nil, err
				}
				if schema == nil {
					continue // dropped at this version -- its snapshot history ends here
				}
				content = migrations.ExportSchema(schema, number, generatedAt)
			} else {
				// Views self-reflect: their history is the DDL, verbatim from sqlite_master.
				ddl, err := viewDefinition(ctx, conn, relation)
				if err != nil {
					return nil, err
				}
				if ddl == nil {
					continue
				}
				content = migrations.ExportDDL(relation, entity.kindToken, *ddl, number, generatedAt)
			}

			directory := filepath.Join(options.OutDir, entity.kindFolder, entity.typeName)
			if err := os.MkdirAll(directory, 0o777); err != nil {
				return nil, err
			}
			file := filepath.Join(directory, fmt.Sprintf("V%04d.schema.json", number))
			if err := os.WriteFile(file, []byte(content+"\n"), 0o666); err != nil {
				return nil, err
			}
			result.WrittenFiles = append(result.WrittenFiles, file)
		}
	}

	return result, nil
}

// touchedObjects is the distinct object names Status reports at exactly version.
func touchedObjects(entries []migrations.MigrationEntry, version int64) []string {
	seen := map[string]bool{}
	var names []string
	for _, entry := range entries {
		if entry.Version == version && !seen[entry.ObjectName] {
			seen[entry.ObjectName] = true
			names = append(names, entry.ObjectName)
		}
	}
	return names
}

// --- trusted baseline (From > 0) --------------------------------------------

// restoreBaseline creates every table/view whose latest snapshot at or below
// fromVersion exists under migrationsDir, straight from that snapshot's DDL —
// trusted, never verified against the actual migration history below it.
func restoreBaseline(ctx context.Context, conn *sql.Conn, migrationsDir string, fromVersion int64, result *ShadowResult) error {
	tableRoot := filepath.Join(migrationsDir, "Table")
	if info, statErr := os.Stat(tableRoot); statErr == nil && info.IsDir() {
		objectDirs, err := os.ReadDir(tableRoot)
		if err != nil {
			return err
		}
		for _, entry := range objectDirs {
			if !entry.IsDir() {
				continue
			}
			schema, atVersion, ok := migrations.LatestTableSnapshotAtOrBefore(filepath.Join(tableRoot, entry.Name()), fromVersion)
			if !ok {
				continue // table born after the trusted version
			}
			if err := execStatement(ctx, conn, migrations.CreateTableSQL(schema)); err != nil {
				return err
			}
			for _, indexSQL := range migrations.CreateIndexSQLs(schema) {
				if err := execStatement(ctx, conn, indexSQL); err != nil {
					return err
				}
			}
			result.Notes = append(result.Notes,
				fmt.Sprintf("baseline: %s restored from V%04d snapshot (trusted)", schema.Name, atVersion))
		}
	} else {
		result.Notes = append(result.Notes,
			fmt.Sprintf("--from V%04d: no committed snapshots under %s; starting empty", fromVersion, tableRoot))
	}

	// Views restore after tables -- their DDL snapshots are executable as stored.
	for _, kindFolder := range []string{"View", "MaterializedView"} {
		kindRoot := filepath.Join(migrationsDir, kindFolder)
		info, statErr := os.Stat(kindRoot)
		if statErr != nil || !info.IsDir() {
			continue
		}
		objectDirs, err := os.ReadDir(kindRoot)
		if err != nil {
			return err
		}
		for _, entry := range objectDirs {
			if !entry.IsDir() {
				continue
			}
			object, ddl, atVersion, ok := migrations.LatestDDLSnapshotAtOrBefore(filepath.Join(kindRoot, entry.Name()), fromVersion)
			if !ok {
				continue
			}
			if err := execStatement(ctx, conn, ddl); err != nil {
				return err
			}
			result.Notes = append(result.Notes,
				fmt.Sprintf("baseline: %s restored from V%04d snapshot (trusted)", object, atVersion))
		}
	}
	return nil
}

// Latest-snapshot-at-or-below lookups delegate to
// migrations.LatestTableSnapshotAtOrBefore/LatestDDLSnapshotAtOrBefore — the
// one directory scan (CODING-STANDARD §8) shared with the diff generator's
// strictly-below variant.

// --- introspection -----------------------------------------------------------

// introspectTable is the live shape of relation, or nil when it is not a
// table right now (a view, or dropped at this version). Column and index
// reads go through migrations.ReadLiveColumns/ReadLiveIndexes — the one
// introspection helper also used by force-sync and SchemaGuard
// (CODING-STANDARD §8) — so this is only the shape conversion into a
// TableSchema, plus the rowid-alias generated-key rule (§7.14).
func introspectTable(ctx context.Context, conn *sql.Conn, dialect core.Dialect, relation string) (*migrations.TableSchema, error) {
	var count int64
	row := conn.QueryRowContext(ctx,
		"select count(*) from sqlite_master where type = 'table' and name = @name", sql.Named("name", relation))
	if err := row.Scan(&count); err != nil {
		return nil, err
	}
	if count == 0 {
		return nil, nil
	}

	live, err := migrations.ReadLiveColumns(ctx, conn, dialect, relation)
	if err != nil {
		return nil, err
	}

	keyCount := 0
	for _, c := range live {
		if c.PrimaryKey {
			keyCount++
		}
	}

	columns := make([]migrations.TableColumn, 0, len(live))
	for _, c := range live {
		// The rowid alias: a lone INTEGER PRIMARY KEY column is database-generated.
		generated := c.PrimaryKey && keyCount == 1 && strings.EqualFold(c.DeclaredType, "INTEGER")
		columns = append(columns, migrations.TableColumn{
			Name: c.Name, StorageType: c.DeclaredType, Nullable: !c.EffectivelyNotNull(), Key: c.PrimaryKey, Generated: generated,
		})
	}

	indexes, err := migrations.ReadLiveIndexes(ctx, conn, dialect, relation)
	if err != nil {
		return nil, err
	}

	return &migrations.TableSchema{Name: relation, Columns: columns, Indexes: indexes}, nil
}

// viewDefinition is the stored, normalized create statement of relation, or
// nil when it is absent (dropped, or never a view).
func viewDefinition(ctx context.Context, conn *sql.Conn, relation string) (*string, error) {
	row := conn.QueryRowContext(ctx,
		"select sql from sqlite_master where type = 'view' and name = @name", sql.Named("name", relation))
	var raw string
	if err := row.Scan(&raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	normalized := migrations.NormalizeDDL(raw)
	return &normalized, nil
}

func execStatement(ctx context.Context, conn *sql.Conn, statement string) error {
	_, err := conn.ExecContext(ctx, statement)
	return err
}

// --- entity resolution ---------------------------------------------------------

// mappedEntity is one entity's snapshot destination: its type name (the
// folder under kindFolder) and the kind token a DDL-shaped snapshot records.
type mappedEntity struct {
	typeName   string
	kind       core.RelationKind
	kindFolder string
	kindToken  string
}

// mapEntitiesByRelation loads every entity in entities and indexes the
// table/view/materialized-view ones by relation name (case-insensitive):
// Shadow resolves a touched relation back to its entity this way, since Go
// has no assembly to scan the other direction (CODING-STANDARD §10).
func mapEntitiesByRelation(entities []reflect.Type) (map[string]mappedEntity, error) {
	loader := metadata.NewLoader(nil)
	byRelation := map[string]mappedEntity{}
	for _, t := range entities {
		m, err := loader.Load(t)
		if err != nil {
			return nil, err
		}
		if m.RelationName == "" {
			continue
		}

		entry := mappedEntity{typeName: t.Name(), kind: m.Kind}
		switch m.Kind {
		case core.RelationTable:
			entry.kindFolder = "Table"
		case core.RelationView:
			entry.kindFolder, entry.kindToken = "View", "view"
		case core.RelationMaterializedView:
			entry.kindFolder, entry.kindToken = "MaterializedView", "materialized_view"
		default:
			continue // statements and (dormant) procedures are not schema objects to snapshot
		}
		byRelation[strings.ToLower(m.RelationName)] = entry
	}
	return byRelation, nil
}

func randomHex(n int) string {
	buf := make([]byte, n)
	_, _ = rand.Read(buf) // crypto/rand.Read only fails when the OS entropy source is broken; a fallback name would collide just as rarely.
	return hex.EncodeToString(buf)
}
