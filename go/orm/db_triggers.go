package orm

import (
	"context"
	"reflect"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
)

// CreateTriggers creates T's linked triggers from its metadata (Level 4, ADR-0042) with replace semantics —
// each is dropped if present, then created, so a second call leaves the same triggers — a dev/test utility
// beside CreateTable and CreateView, deliberately separate from CreateTable: a trigger is created after the
// tables it writes to exist, and versioned migrations (`CreateTrigger()` in a trigger step) stay the
// schema-evolution path. Sources that take no trigger are DDL-001; a form the dialect lacks is DDL-005,
// raised before anything executes.
func CreateTriggers[T any](ctx context.Context, db *Db) error {
	entityType := reflect.TypeFor[T]()
	m, err := db.maps.Load(entityType)
	if err != nil {
		return err
	}
	if m.Kind != core.RelationTable && m.Kind != core.RelationView {
		return core.Errorf("DDL-001", entityType.Name(), "is %s-backed; only tables and views take triggers", m.Kind)
	}

	dialect := db.options.Dialect
	var statements []string
	for _, trigger := range m.Triggers {
		creates, err := dialect.CreateTriggerSQL(m.RelationName, m.Kind == core.RelationView, trigger.Definition)
		if err != nil {
			return err
		}
		statements = append(statements, dialect.DropTriggerSQL(m.RelationName, trigger.Definition.Name)...)
		statements = append(statements, creates...)
	}

	for _, statement := range statements {
		if _, err := db.exec().ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}
