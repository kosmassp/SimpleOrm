package postgres_test

import (
	"strings"
	"testing"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
	"github.com/kosmassp/SimpleOrm/go/orm/postgres"
)

// The PostgreSQL trigger rendering beyond the pinned strings of conformance/trigger-sql/ (Level 4, ADR-0042):
// the refusal, the return value of a before trigger, and the native narrowing.

func TestTrigger_InsteadOfOnATableIsDDL005AndOnAViewIsRendered(t *testing.T) {
	dialect := postgres.New()
	definition := core.TriggerDefinition{Name: "trg_i", Timing: core.TimingInsteadOf, Events: core.EventDelete, SQL: "select 1;"}
	if _, err := dialect.CreateTriggerSQL("widgets", false, definition); core.CodeOf(err) != "DDL-005" {
		t.Errorf("instead of on a table: expected DDL-005, got %v", err)
	}
	statements, err := dialect.CreateTriggerSQL("widget_summaries", true, definition)
	if err != nil || len(statements) != 2 || !strings.Contains(statements[1], `instead of delete on "widget_summaries"`) {
		t.Errorf("instead of on a view: %q, %v", statements, err)
	}
}

func TestTrigger_OnlyAnAfterTriggerReturnsNull(t *testing.T) {
	dialect := postgres.New()
	for _, timing := range []core.TriggerTiming{core.TimingBefore, core.TimingAfter} {
		definition := core.TriggerDefinition{Name: "t", Timing: timing, Events: core.EventInsert, SQL: "select 1;"}
		statements, err := dialect.CreateTriggerSQL("r", false, definition)
		if err != nil {
			t.Fatal(err)
		}
		returnsNull := strings.Contains(statements[0], "return null;")
		returnsRow := strings.Contains(statements[0], "return new;")
		if returnsNull != (timing == core.TimingAfter) || returnsRow != (timing == core.TimingBefore) {
			t.Errorf("%v: function body %q", timing, statements[0])
		}
	}
}
