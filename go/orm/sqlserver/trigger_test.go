package sqlserver_test

import (
	"strings"
	"testing"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
	"github.com/kosmassp/SimpleOrm/go/orm/sqlserver"
)

// The SQL Server trigger rendering beyond the pinned strings of conformance/trigger-sql/ (Level 4, ADR-0042):
// the refusal that has no analog elsewhere, and the narrowing prologue.

func TestTrigger_BeforeIsDDL005BecauseSQLServerHasNoBeforeTrigger(t *testing.T) {
	definition := core.TriggerDefinition{Name: "trg_b", Timing: core.TimingBefore, Events: core.EventInsert, SQL: "select 1;"}
	statements, err := sqlserver.New().CreateTriggerSQL("widgets", false, definition)
	if core.CodeOf(err) != "DDL-005" || statements != nil {
		t.Errorf("expected DDL-005 and no statements, got %q, %v", statements, err)
	}
}

func TestTrigger_InsteadOfIsNativeOnTablesAndViewsAndAnUpdateOfListIsAPrologue(t *testing.T) {
	dialect := sqlserver.New()
	definition := core.TriggerDefinition{
		Name: "trg_i", Timing: core.TimingInsteadOf, Events: core.EventUpdate | core.EventInsert,
		UpdateOf: []string{"a"}, SQL: "select 1 from {new: a} n;",
	}
	for _, onView := range []bool{false, true} {
		statements, err := dialect.CreateTriggerSQL("r", onView, definition)
		if err != nil || len(statements) != 1 {
			t.Fatalf("onView=%v: %q, %v", onView, statements, err)
		}
		want := "create trigger [trg_i] on [r] instead of insert, update as\nbegin\n    set nocount on;\n" +
			"    if not (update([a])) return;\nselect 1 from inserted n;\nend"
		if statements[0] != want {
			t.Errorf("onView=%v:\n got  %q\n want %q", onView, statements[0], want)
		}
	}
	if drops := dialect.DropTriggerSQL("r", "o'neil"); len(drops) != 1 || !strings.Contains(drops[0], "N'o''neil'") {
		t.Errorf("the drop doubles a single quote in the object_id literal: %q", drops)
	}
}
