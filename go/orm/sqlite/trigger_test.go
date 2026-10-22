package sqlite_test

import (
	"context"
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/testsupport"
	"github.com/kosmassp/SimpleOrm/go/orm/migrations"
	"github.com/kosmassp/SimpleOrm/go/orm/sqlite"
)

// The SQLite trigger rendering, its inverse, and its behavior on a real database (Level 4, ADR-0042):
// conformance/trigger-sql/ pins the strings; these tests pin that what is rendered runs, fires as declared,
// reads back as the same declaration, and that the parse is the exact inverse of the token expansions.

func triggerDatabase(t *testing.T) (*sql.DB, context.Context) {
	t.Helper()
	pool, err := sqlite.New().CreateConnection("Data Source=" + testsupport.TempDatabase(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pool.Close() })
	ctx := context.Background()
	for _, statement := range []string{
		"create table widgets (id INTEGER PRIMARY KEY, name TEXT NOT NULL, price INTEGER NOT NULL, note TEXT) STRICT",
		"create table widget_log (id INTEGER PRIMARY KEY, widget_id INTEGER NOT NULL, kind TEXT NOT NULL, at TEXT) STRICT",
	} {
		if _, err := pool.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	return pool, ctx
}

func installTrigger(t *testing.T, ctx context.Context, pool *sql.DB, relation string, definition core.TriggerDefinition) {
	t.Helper()
	dialect := sqlite.New()
	creates, err := dialect.CreateTriggerSQL(relation, false, definition)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range append(dialect.DropTriggerSQL(relation, definition.Name), creates...) {
		if _, err := pool.ExecContext(ctx, statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
}

func logRows(t *testing.T, ctx context.Context, pool *sql.DB) []string {
	t.Helper()
	rows, err := pool.QueryContext(ctx, "select kind || ':' || widget_id from widget_log order by id")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var row string
		if err := rows.Scan(&row); err != nil {
			t.Fatal(err)
		}
		out = append(out, row)
	}
	return out
}

func TestTrigger_AMultiEventNarrowedDeclarationRendersOneTriggerPerEventFiresAsDeclaredAndReadsBackWhole(t *testing.T) {
	pool, ctx := triggerDatabase(t)
	definition := core.TriggerDefinition{
		Name: "trg_widgets_changes", Timing: core.TimingAfter, Events: core.EventUpdate | core.EventDelete,
		UpdateOf: []string{"name", "price"},
		SQL:      "insert into widget_log (widget_id, kind, at) select o.id, 'old', {now} from {old: id, name} o;",
	}
	installTrigger(t, ctx, pool, "widgets", definition)

	// SQLite stores one trigger per event: <name>_update (narrowed) and <name>_delete.
	rows, err := migrations.ReadTriggerDefinitions(ctx, pool, sqlite.New(), definition.Name)
	if err != nil || len(rows) != 2 {
		t.Fatalf("expected two stored triggers, got %d: %v (%v)", len(rows), rows, err)
	}
	if !strings.Contains(rows[0], "trg_widgets_changes_delete") || !strings.Contains(rows[1], "after update of name, price on widgets") {
		t.Errorf("rows are ordered by name, the narrowing is on the update one only: %q", rows)
	}

	declaration, note := sqlite.ParseTriggerRows(definition.Name, rows)
	if declaration == nil {
		t.Fatalf("the stored triggers must parse back to one declaration, note %q", note)
	}
	if declaration.Relation != "widgets" || declaration.Timing != core.TimingAfter ||
		declaration.Events != core.EventUpdate|core.EventDelete ||
		!reflect.DeepEqual(declaration.UpdateOf, []string{"name", "price"}) || declaration.SQL != core.NormalizeTriggerBody(definition.SQL) {
		t.Errorf("read back as %+v", declaration)
	}

	exec := func(statement string) {
		t.Helper()
		if _, err := pool.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	exec("insert into widgets (id, name, price, note) values (1, 'a', 10, 'n'), (2, 'b', 20, 'n')")
	exec("update widgets set note = 'changed'") // names neither narrowed column: no firing
	if got := logRows(t, ctx, pool); len(got) != 0 {
		t.Fatalf("an update outside UpdateOf must not fire, got %v", got)
	}
	exec("update widgets set price = price + 1") // two rows, the row-level trigger fires for each
	exec("delete from widgets where id = 1")
	if got, want := logRows(t, ctx, pool), []string{"old:1", "old:2", "old:1"}; !reflect.DeepEqual(got, want) {
		t.Errorf("log = %v, want %v", got, want)
	}
}

func TestTrigger_TheParseIsTheInverseOfEveryTokenExpansion(t *testing.T) {
	bodies := []string{
		"insert into widget_log (widget_id, kind, at) select n.id, 'new', {now} from {new: id} n;",
		"insert into widget_log (widget_id, kind) select n.id, 'x' from {new: id, name, price} n;",
		"insert into widget_log (widget_id, kind) select o.id, 'x' from {old: id} o;",
		"insert into widget_log (widget_id, kind) select n.id, 'x' from {new: id} n join {old: id} o on o.id = n.id;",
		"insert into widget_log (widget_id, kind) values (1, '{not a token}'); /* a comment */",
	}
	for _, body := range bodies {
		events := core.EventUpdate
		pool, ctx := triggerDatabase(t)
		definition := core.TriggerDefinition{Name: "trg_round_trip", Timing: core.TimingBefore, Events: events, SQL: body}
		installTrigger(t, ctx, pool, "widgets", definition)

		rows, err := migrations.ReadTriggerDefinitions(ctx, pool, sqlite.New(), definition.Name)
		if err != nil {
			t.Fatal(err)
		}
		declaration, note := sqlite.ParseTriggerRows(definition.Name, rows)
		if declaration == nil {
			t.Fatalf("%q: no declaration read back (%s)", body, note)
		}
		if declaration.SQL != core.NormalizeTriggerBody(body) {
			t.Errorf("round trip changed the body:\n got  %q\n want %q", declaration.SQL, core.NormalizeTriggerBody(body))
		}
		if declaration.Timing != core.TimingBefore || len(declaration.UpdateOf) != 0 {
			t.Errorf("header fields: %+v", declaration)
		}
	}
}

func TestParseTriggerRows_AbsentHandWrittenAndInconsistentRows(t *testing.T) {
	if declaration, note := sqlite.ParseTriggerRows("t", nil); declaration != nil || note != "" {
		t.Errorf("no rows is absent, got %v %q", declaration, note)
	}

	handWritten := "CREATE TRIGGER t AFTER UPDATE ON widgets BEGIN select 1; END"
	if declaration, note := sqlite.ParseTriggerRows("t", []string{handWritten}); declaration != nil || !strings.Contains(note, "hand-written") {
		t.Errorf("a trigger outside the grammar is skipped with a note, got %v %q", declaration, note)
	}

	renamed := "CREATE TRIGGER other after update on widgets for each row begin select 1; end"
	if declaration, note := sqlite.ParseTriggerRows("t", []string{renamed}); declaration != nil || note == "" {
		t.Errorf("a trigger the renderer would not have named so is skipped, got %v %q", declaration, note)
	}

	disagreeing := []string{
		"create trigger t_insert after insert on widgets for each row begin select 1; end",
		"create trigger t_update before update on widgets for each row begin select 1; end",
	}
	if declaration, note := sqlite.ParseTriggerRows("t", disagreeing); declaration != nil || note == "" {
		t.Errorf("per-event triggers that disagree on timing are no declaration, got %v %q", declaration, note)
	}

	merged := []string{
		"CREATE TRIGGER t_delete after delete on widgets for each row begin select 1; end",
		"CREATE TRIGGER t_insert after insert on widgets for each row begin select 1; end",
		"CREATE TRIGGER t_update after update of name on widgets for each row begin select 1; end",
	}
	declaration, note := sqlite.ParseTriggerRows("t", merged)
	if declaration == nil {
		t.Fatalf("per-event triggers merge, note %q", note)
	}
	if declaration.Events != core.EventInsert|core.EventUpdate|core.EventDelete || !reflect.DeepEqual(declaration.UpdateOf, []string{"name"}) {
		t.Errorf("merged: %+v", declaration)
	}
}

func TestTrigger_InsteadOfOnATableIsDDL005AndOnAViewIsRendered(t *testing.T) {
	dialect := sqlite.New()
	definition := core.TriggerDefinition{
		Name: "trg_i", Timing: core.TimingInsteadOf, Events: core.EventDelete, SQL: "select 1;",
	}
	if _, err := dialect.CreateTriggerSQL("widgets", false, definition); core.CodeOf(err) != "DDL-005" {
		t.Errorf("instead of on a table: expected DDL-005, got %v", err)
	}
	statements, err := dialect.CreateTriggerSQL("widget_summaries", true, definition)
	if err != nil || len(statements) != 1 || !strings.HasPrefix(statements[0], "create trigger trg_i instead of delete on widget_summaries") {
		t.Errorf("instead of on a view: %q, %v", statements, err)
	}
}
