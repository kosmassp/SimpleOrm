package postgres_test

// The trigger coverage of the live testing contract (Level 4, ADR-0042, spec/dialects.md "Testing contract"):
// the declared fixture trigger created from metadata, firing once per updated row, and read back through
// TriggerDefinitionSQL as exactly its rendering; plus a multi-event, narrowed, `before` declaration — the form
// PostgreSQL has and SQL Server lacks.

import (
	"context"
	"database/sql"
	"reflect"
	"testing"

	"github.com/kosmassp/SimpleOrm/go/orm"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
	"github.com/kosmassp/SimpleOrm/go/orm/migrations"
	"github.com/kosmassp/SimpleOrm/go/orm/postgres"
	"github.com/kosmassp/SimpleOrm/go/orm/sample"
)

// rawPool opens a plain pool on a database (outside any session), closed when the test ends.
func rawPool(t *testing.T, connection string) *sql.DB {
	t.Helper()
	pool, err := postgres.New().CreateConnection(connection)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(func() { _ = pool.Close() })
	return pool
}

// assertReadsBackAsRendered is the symmetric contract of spec/dialects.md "Introspection result shapes": the
// rows of TriggerDefinitionSQL, each normalized, are exactly the statements CreateTriggerSQL renders.
func assertReadsBackAsRendered(t *testing.T, db *orm.Db, pool *sql.DB, entityType reflect.Type, wantRows int) {
	t.Helper()
	ctx := context.Background()
	m, err := db.Maps().Load(entityType)
	if err != nil {
		t.Fatal(err)
	}
	dialect := postgres.New()
	for _, trigger := range m.Triggers {
		rendered, err := dialect.CreateTriggerSQL(m.RelationName, m.Kind == core.RelationView, trigger.Definition)
		if err != nil {
			t.Fatal(err)
		}
		rows, err := migrations.ReadTriggerDefinitions(ctx, pool, dialect, trigger.Definition.Name)
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != wantRows {
			t.Fatalf("%s: expected %d stored statements (function and trigger), got %d: %q", trigger.Definition.Name, wantRows, len(rows), rows)
		}
		if !migrations.TriggerStatementsMatch(rows, rendered) {
			t.Errorf("%s: the catalog reconstruction differs from the rendering\n got  %q\n want %q",
				trigger.Definition.Name, rows, rendered)
		}
	}
}

func TestLive_TheFixtureTriggerIsCreatedFromMetadataFiresOncePerUpdatedRowAndReadsBackAsItsRendering(t *testing.T) {
	ctx := context.Background()
	db, connection := openSample(t, orm.Options{})
	pool := rawPool(t, connection)

	// Replace semantics: a second call leaves the same one trigger.
	for range 2 {
		if err := orm.CreateTriggers[sample.Role](ctx, db); err != nil {
			t.Fatalf("create triggers: %v", err)
		}
	}
	assertReadsBackAsRendered(t, db, pool, reflect.TypeFor[sample.Role](), 2)

	for _, name := range []string{"admin", "editor", "viewer"} {
		if err := orm.Insert(ctx, db, &sample.Role{Name: name}); err != nil {
			t.Fatal(err)
		}
	}
	if changes, err := orm.QueryAll[sample.RoleChange](ctx, db); err != nil || len(changes) != 0 {
		t.Fatalf("an insert fires no update trigger, got %d rows (%v)", len(changes), err)
	}

	// One statement updates three rows; the row-level trigger fires once for each.
	if _, err := pool.ExecContext(ctx, `update "roles" set "is_deleted" = false`); err != nil {
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

// liveBeforeWidget carries a multi-event, narrowed, before trigger (PostgreSQL has BEFORE; SQL Server does not).
type liveBeforeWidget struct {
	ID   int64  `orm:"column,key,generated"`
	Name string `orm:"column"`
	Note string `orm:"column"`
}

func (liveBeforeWidget) Entity() orm.EntityDef {
	return orm.EntityDef{
		Source:   orm.Table("live_trigger_widgets"),
		Triggers: []orm.TriggerLink{orm.Trigger[liveBeforeWidgetTrigger]()},
	}
}

// liveBeforeWidgetTrigger fires before an insert or before an update that names the Name column.
type liveBeforeWidgetTrigger struct{}

func (liveBeforeWidgetTrigger) Trigger() orm.TriggerDef {
	return orm.TriggerDef{
		Name:     "trg_live_widgets_before",
		Timing:   orm.TimingBefore,
		Events:   orm.EventInsert | orm.EventUpdate,
		UpdateOf: []string{"Name"},
		SQL:      `insert into live_trigger_logs (widget_id, name) select n.id, n.name from {new: id, name} n;`,
	}
}

type liveTriggerLog struct {
	ID       int64  `orm:"column,key,generated"`
	WidgetID int64  `orm:"column"`
	Name     string `orm:"column"`
}

func (liveTriggerLog) Entity() orm.EntityDef {
	return orm.EntityDef{Source: orm.Table("live_trigger_logs")}
}

func TestLive_AMultiEventNarrowedBeforeTriggerFiresOnItsEventsAndOnlyForItsColumns(t *testing.T) {
	ctx := context.Background()
	db, connection := openBare(t, orm.Options{})
	pool := rawPool(t, connection)
	createTables(t, db, orm.CreateTable[liveBeforeWidget], orm.CreateTable[liveTriggerLog])

	if err := orm.CreateTriggers[liveBeforeWidget](ctx, db); err != nil {
		t.Fatalf("create triggers: %v", err)
	}
	assertReadsBackAsRendered(t, db, pool, reflect.TypeFor[liveBeforeWidget](), 2)

	widget := &liveBeforeWidget{Name: "first", Note: "n"}
	if err := orm.Insert(ctx, db, widget); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.ExecContext(ctx, `update "live_trigger_widgets" set "note" = 'changed'`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.ExecContext(ctx, `update "live_trigger_widgets" set "name" = 'renamed'`); err != nil {
		t.Fatal(err)
	}

	logs, err := orm.QueryAll[liveTriggerLog](ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 2 {
		t.Fatalf("expected the insert and the update of name to fire, and the update of note not to: got %d rows %+v", len(logs), logs)
	}
	if logs[0].WidgetID != widget.ID || logs[0].Name != "first" || logs[1].Name != "renamed" {
		t.Errorf("the before trigger saw the rows being written: %+v", logs)
	}
}
