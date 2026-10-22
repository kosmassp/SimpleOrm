package sqlite

import (
	"context"
	"database/sql"
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

// Trigger snapshots in the shadow replay (Level 4, ADR-0042, spec/migrations.md "Snapshots"): a trigger is
// touched by name (the step's schema_version object), its history is the stored SQLite trigger parsed back to
// a declaration, and its snapshot lands under Trigger/<class>/ — dialect-neutral, so one tree serves every
// dialect.

// mappedTrigger is a trigger's snapshot destination: its class's name (the folder under Trigger/).
type mappedTrigger struct {
	typeName string
}

// mapTriggersByName indexes every trigger class the entities link, and every class the registry lists, by
// the name it declares (case-insensitive): Shadow resolves a touched object back to its class this way, since
// Go has no assembly to scan the other direction (CODING-STANDARD §10).
func mapTriggersByName(entities, triggerTypes []reflect.Type) (map[string]mappedTrigger, error) {
	loader := metadata.NewLoader(nil)
	byName := map[string]mappedTrigger{}
	note := func(t reflect.Type) {
		t = metadata.EntityType(t)
		if declaration, ok := core.TriggerDeclarationOf(t); ok {
			byName[strings.ToLower(strings.TrimSpace(declaration.Name))] = mappedTrigger{typeName: t.Name()}
		}
	}

	for _, t := range entities {
		if core.IsTriggerType(t) {
			continue
		}
		m, err := loader.Load(t)
		if err != nil {
			return nil, err
		}
		for _, trigger := range m.Triggers {
			note(trigger.Type)
		}
	}
	for _, t := range triggerTypes {
		note(t)
	}
	return byName, nil
}

// writeTriggerSnapshot reads the live trigger back through TriggerDefinitionSQL, parses it to its declaration,
// and writes Trigger/<class>/V000N.schema.json; it returns the file written, or "" when the trigger is absent
// (dropped at this version — its history ends here) or hand-written (skipped with a note).
func writeTriggerSnapshot(
	ctx context.Context, conn *sql.Conn, dialect core.Dialect, outDir string, trigger mappedTrigger, name string,
	version int64, generatedAt time.Time, result *ShadowResult,
) (string, error) {
	rows, err := migrations.ReadTriggerDefinitions(ctx, conn, dialect, name)
	if err != nil {
		return "", err
	}
	declaration, note := ParseTriggerRows(name, rows)
	if declaration == nil {
		if note != "" {
			result.Notes = append(result.Notes, fmt.Sprintf("V%04d: %s", version, note))
		}
		return "", nil
	}
	declaration.RelationKind, err = relationKindOf(ctx, conn, declaration.Relation)
	if err != nil {
		return "", err
	}

	directory := filepath.Join(outDir, "Trigger", trigger.typeName)
	if err := os.MkdirAll(directory, 0o777); err != nil {
		return "", err
	}
	file := filepath.Join(directory, fmt.Sprintf("V%04d.schema.json", version))
	if err := os.WriteFile(file, []byte(migrations.ExportTrigger(declaration, version, generatedAt)+"\n"), 0o666); err != nil {
		return "", err
	}
	return file, nil
}

// relationKindOf is whether a trigger's relation is a table or a view, from sqlite_master.
func relationKindOf(ctx context.Context, conn *sql.Conn, relation string) (string, error) {
	var kind string
	row := conn.QueryRowContext(ctx, "select type from sqlite_master where name = @name and type in ('table', 'view')", sql.Named("name", relation))
	if err := row.Scan(&kind); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return migrations.RelationKindTable, nil
		}
		return "", err
	}
	if kind == "view" {
		return migrations.RelationKindView, nil
	}
	return migrations.RelationKindTable, nil
}

// restoreTriggerBaseline recreates every trigger whose latest snapshot at or below fromVersion exists under
// migrationsDir, rendered through the dialect from the snapshot's declaration — trusted, like the tables and
// views before it, so a later trigger step's guard finds what it expects.
func restoreTriggerBaseline(
	ctx context.Context, conn *sql.Conn, dialect core.Dialect, migrationsDir string, fromVersion int64, result *ShadowResult,
) error {
	root := filepath.Join(migrationsDir, "Trigger")
	info, statErr := os.Stat(root)
	if statErr != nil || !info.IsDir() {
		return nil
	}
	objectDirs, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, entry := range objectDirs {
		if !entry.IsDir() {
			continue
		}
		declaration, atVersion, ok := migrations.LatestTriggerSnapshotAtOrBefore(filepath.Join(root, entry.Name()), fromVersion)
		if !ok {
			continue
		}
		creates, err := dialect.CreateTriggerSQL(declaration.Relation, declaration.OnView(), declaration.Definition())
		if err != nil {
			return err
		}
		for _, statement := range append(dialect.DropTriggerSQL(declaration.Relation, declaration.Name), creates...) {
			if err := execStatement(ctx, conn, statement); err != nil {
				return err
			}
		}
		result.Notes = append(result.Notes,
			fmt.Sprintf("baseline: %s restored from V%04d snapshot (trusted)", declaration.Name, atVersion))
	}
	return nil
}
