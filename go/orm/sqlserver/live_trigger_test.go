package sqlserver_test

// The trigger coverage of the live testing contract (Level 4, ADR-0042, spec/dialects.md "Testing contract"):
// the declared fixture trigger created from metadata, firing once per updated row, and read back through
// TriggerDefinitionSQL as exactly its rendering. SQL Server has no BEFORE trigger, so the multi-event narrowed
// `before` declaration of the contract is PostgreSQL's alone (its refusal here is DDL-005, pinned without a
// server by conformance/trigger-sql/). Skips where no server answers; UNTESTED against a server on the
// machine this file was written on.

import (
	"context"
	"reflect"
	"testing"

	"github.com/kosmassp/SimpleOrm/go/orm"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
	"github.com/kosmassp/SimpleOrm/go/orm/migrations"
	"github.com/kosmassp/SimpleOrm/go/orm/sample"
	"github.com/kosmassp/SimpleOrm/go/orm/sqlserver"
)

func TestLive_TheFixtureTriggerIsCreatedFromMetadataFiresOncePerUpdatedRowAndReadsBackAsItsRendering(t *testing.T) {
	ctx := context.Background()
	db, connectionString := newSampleSession(t, orm.Options{})
	pool := rawPool(t, connectionString)

	// Replace semantics: a second call leaves the same one trigger.
	for range 2 {
		if err := orm.CreateTriggers[sample.Role](ctx, db); err != nil {
			t.Fatalf("create triggers: %v", err)
		}
	}

	m, err := db.Maps().Load(reflect.TypeFor[sample.Role]())
	if err != nil {
		t.Fatal(err)
	}
	dialect := sqlserver.New()
	for _, trigger := range m.Triggers {
		rendered, err := dialect.CreateTriggerSQL(m.RelationName, m.Kind == core.RelationView, trigger.Definition)
		if err != nil {
			t.Fatal(err)
		}
		rows, err := migrations.ReadTriggerDefinitions(ctx, pool, dialect, trigger.Definition.Name)
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 1 || !migrations.TriggerStatementsMatch(rows, rendered) {
			t.Errorf("%s: the stored text differs from the rendering\n got  %q\n want %q", trigger.Definition.Name, rows, rendered)
		}
	}

	for _, name := range []string{"admin", "editor", "viewer"} {
		mustInsert(t, db, &sample.Role{Name: name})
	}
	if changes, err := orm.QueryAll[sample.RoleChange](ctx, db); err != nil || len(changes) != 0 {
		t.Fatalf("an insert fires no update trigger, got %d rows (%v)", len(changes), err)
	}

	// One statement updates three rows; the statement-level trigger sees all three in `inserted`.
	if _, err := pool.ExecContext(ctx, "update [roles] set [is_deleted] = 0"); err != nil {
		t.Fatal(err)
	}
	changes, err := orm.QueryAll[sample.RoleChange](ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 3 {
		t.Fatalf("expected one role_changes row per updated role, got %d", len(changes))
	}
	names := map[string]bool{}
	for _, change := range changes {
		names[change.RoleName] = true
		if change.RoleID <= 0 || change.ChangedAt.IsZero() {
			t.Errorf("the trigger fills the role id and the {now} instant: %+v", change)
		}
	}
	if !names["admin"] || !names["editor"] || !names["viewer"] {
		t.Errorf("the {new: id, role_name} rows are the updated roles, got %v", names)
	}
}
