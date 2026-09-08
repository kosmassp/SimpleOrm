package migrations

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strings"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/metadata"
)

// SyncPlan is force-sync planning's output (ADR-0013 add.3 / ADR-0017): after
// migrations complete, the real database is compared against the model.
// Additive fixes (missing tables, missing nullable columns, missing indexes
// on new tables) are safe; deletions (extra columns/indexes) are gated behind
// an explicit flag at the caller; type/nullability changes and non-nullable
// additions are never auto-applied (DDL-004). Renames are never inferred.
type SyncPlan struct {
	// Additive are safe statements: create table, add nullable column, create a missing index.
	Additive []string
	// Deletions are destructive statements: drop extra column/index — apply only with --allow-delete.
	Deletions []string
	// Unsupported names a change sync cannot express safely (DDL-004): write a migration.
	Unsupported []string
}

// IsEmpty reports whether the plan has nothing to do.
func (p *SyncPlan) IsEmpty() bool {
	return len(p.Additive) == 0 && len(p.Deletions) == 0 && len(p.Unsupported) == 0
}

// PlanSync compares the live schema of every table-backed entity in
// tableEntities against its EntityMap (§7.23, the `migrate --force` CLI
// step's core). View-, statement-, and procedure-backed types are skipped: force
// sync only ever touches tables.
func PlanSync(
	ctx context.Context, conn *sql.Conn, dialect core.Dialect, maps *metadata.Loader, tableEntities []reflect.Type,
) (*SyncPlan, error) {
	plan := &SyncPlan{}

	for _, t := range tableEntities {
		m, err := maps.Load(t)
		if err != nil {
			return nil, err
		}
		if m.Kind != core.RelationTable {
			continue
		}

		live, err := ReadLiveColumns(ctx, conn, dialect, m.RelationName)
		if err != nil {
			return nil, err
		}
		if len(live) == 0 {
			plan.Additive = append(plan.Additive, dialect.CreateTableSQL(m))
			plan.Additive = append(plan.Additive, dialect.CreateIndexSQL(m)...)
			continue
		}

		for _, property := range m.Properties {
			column, ok := live[strings.ToLower(property.ColumnName)]
			if !ok {
				if property.IsNullable {
					plan.Additive = append(plan.Additive, dialect.AddColumnSQL(
						m.RelationName, property.ColumnName, dialect.StorageType(property), true, ""))
				} else {
					plan.Unsupported = append(plan.Unsupported, fmt.Sprintf(
						"%s.%s: adding a non-nullable column needs a default/backfill — write a migration",
						m.RelationName, property.ColumnName))
				}
				continue
			}

			expected := dialect.StorageType(property)
			if !strings.EqualFold(column.DeclaredType, expected) || column.EffectivelyNotNull() == property.IsNullable {
				liveSuffix, modelSuffix := "", ""
				if column.EffectivelyNotNull() {
					liveSuffix = " not null"
				}
				if !property.IsNullable {
					modelSuffix = " not null"
				}
				plan.Unsupported = append(plan.Unsupported, fmt.Sprintf(
					"%s.%s: is %s%s, model wants %s%s — write a migration",
					m.RelationName, property.ColumnName, column.DeclaredType, liveSuffix, expected, modelSuffix))
			}
		}

		for _, column := range live {
			found := false
			for _, property := range m.Properties {
				if strings.EqualFold(property.ColumnName, column.Name) {
					found = true
					break
				}
			}
			if !found {
				plan.Deletions = append(plan.Deletions, dialect.DropColumnSQL(m.RelationName, column.Name))
			}
		}

		// Indexes match structurally (ADR-0017 add.2): what matters is the indexed
		// columns (order, direction, uniqueness), never the name.
		liveIndexes, err := ReadLiveIndexes(ctx, conn, dialect, m.RelationName)
		if err != nil {
			return nil, err
		}
		liveSignatures := map[string]bool{}
		for _, index := range liveIndexes {
			liveSignatures[IndexSignature(index)] = true
		}
		modelSignatures := map[string]bool{}
		for _, index := range m.Indexes {
			modelSignatures[EntityIndexSignature(index)] = true
		}
		createIndexSQL := dialect.CreateIndexSQL(m)
		for i, index := range m.Indexes {
			if !liveSignatures[EntityIndexSignature(index)] {
				plan.Additive = append(plan.Additive, createIndexSQL[i])
			}
		}
		for _, index := range liveIndexes {
			if !modelSignatures[IndexSignature(index)] {
				plan.Deletions = append(plan.Deletions, dialect.DropIndexSQL(m.RelationName, index.Name))
			}
		}
	}

	return plan, nil
}

// ApplySync executes plan statements in order on conn — the CLI's force-sync
// apply step, and what the conformance suite's "sql" command uses for an
// outside hotfix (applied without touching migration history).
func ApplySync(ctx context.Context, conn *sql.Conn, statements []string) error {
	for _, statement := range statements {
		if _, err := conn.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

// LiveColumn is one column of a relation's live shape, read through the
// dialect's introspection SQL (§7.25 ColumnsInfoSQL). The one column-reader
// (CODING-STANDARD §8): force-sync planning, SchemaGuard's declared-type/
// nullability checks (orm/schema_guard.go), and the shadow replayer's table
// introspection (orm/sqlite/shadow.go) all read live columns through this.
type LiveColumn struct {
	Name         string
	DeclaredType string
	NotNull      bool
	PrimaryKey   bool
}

// EffectivelyNotNull reports whether the column refuses NULL in practice:
// declared NOT NULL, or a primary key (SQLite does not always mark a
// rowid-alias primary key column NOT NULL even though it refuses one).
func (c LiveColumn) EffectivelyNotNull() bool { return c.NotNull || c.PrimaryKey }

// ReadLiveColumns reads relation's columns from the database, keyed by
// lower-cased name (empty when the relation does not exist).
func ReadLiveColumns(ctx context.Context, conn *sql.Conn, dialect core.Dialect, relation string) (map[string]LiveColumn, error) {
	columns := map[string]LiveColumn{}
	rows, err := conn.QueryContext(ctx, dialect.ColumnsInfoSQL(), sql.Named("relation", relation))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var name, declaredType string
		var notNull, pk int64
		if err := rows.Scan(&name, &declaredType, &notNull, &pk); err != nil {
			return nil, err
		}
		columns[strings.ToLower(name)] = LiveColumn{Name: name, DeclaredType: declaredType, NotNull: notNull != 0, PrimaryKey: pk != 0}
	}
	return columns, rows.Err()
}

// ReadLiveIndexes reads relation's explicitly created indexes, structurally
// complete (columns in order, direction, uniqueness) — the same one reader
// force-sync's structural comparison (ADR-0017 add.2) and the shadow
// replayer's snapshot both need.
func ReadLiveIndexes(ctx context.Context, conn *sql.Conn, dialect core.Dialect, relation string) ([]TableIndex, error) {
	type accumulator struct {
		unique  bool
		columns []IndexPart
	}
	order := []string{}
	byName := map[string]*accumulator{}

	rows, err := conn.QueryContext(ctx, dialect.IndexesInfoSQL(), sql.Named("relation", relation))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var name, column string
		var unique, seqno, descending int64
		if err := rows.Scan(&name, &unique, &seqno, &column, &descending); err != nil {
			return nil, err
		}
		acc, ok := byName[name]
		if !ok {
			acc = &accumulator{unique: unique != 0}
			byName[name] = acc
			order = append(order, name)
		}
		acc.columns = append(acc.columns, IndexPart{ColumnName: column, Descending: descending != 0})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	indexes := make([]TableIndex, len(order))
	for i, name := range order {
		acc := byName[name]
		indexes[i] = TableIndex{Name: name, Columns: acc.columns, Unique: acc.unique}
	}
	return indexes, nil
}
