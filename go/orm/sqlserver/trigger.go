package sqlserver

import (
	"strings"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
)

// The SQL Server rendering of a trigger declaration (Level 4, ADR-0042, spec/dialects.md "Triggers"; pinned
// byte for byte by conformance/trigger-sql/): one statement-level `create trigger` listing the events, whose
// row tokens are the `inserted` and `deleted` pseudo-tables, with `set nocount on` as rendering — never the
// author's. `create trigger` must open its batch, so the drop is a separate statement, and `create or alter`
// is not used (SQL Server 2016 SP1).

// CreateTriggerSQL renders the one create statement. SQL Server has no BEFORE trigger (DDL-005); `instead
// of` is native on tables and views. A declared update-of narrowing is the `if not (update(…)) return;`
// prologue, since SQL Server has no `update of` clause.
func (d *Dialect) CreateTriggerSQL(relation string, onView bool, trigger core.TriggerDefinition) ([]string, error) {
	if trigger.Timing == core.TimingBefore {
		return nil, core.Errorf("DDL-005", trigger.Name,
			"SQL Server has no BEFORE triggers; declare the trigger after (or instead_of) — its body cannot run before the write")
	}

	timing := "after"
	if trigger.Timing == core.TimingInsteadOf {
		timing = "instead of"
	}

	// update(col) is false in a delete, so a declaration that also fires on delete applies the narrowing only
	// when rows were inserted or updated.
	prologue := ""
	if len(trigger.UpdateOf) > 0 {
		checks := make([]string, len(trigger.UpdateOf))
		for i, column := range trigger.UpdateOf {
			checks[i] = "update(" + d.QuoteIdentifier(column) + ")"
		}
		guard := ""
		if trigger.Events.Has(core.EventDelete) {
			guard = "exists (select 1 from inserted) and "
		}
		prologue = "    if " + guard + "not (" + strings.Join(checks, " or ") + ") return;\n"
	}

	body, err := core.RenderTriggerBody(trigger.Name, trigger.SQL, expandToken)
	if err != nil {
		return nil, err
	}
	statement := "create trigger " + d.QuoteIdentifier(trigger.Name) + " on " + d.QuoteIdentifier(relation) + " " + timing + " " +
		strings.Join(trigger.Events.Tokens(), ", ") + " as\nbegin\n    set nocount on;\n" + prologue + body + "\nend"
	return []string{statement}, nil
}

// DropTriggerSQL is the idempotent drop: SQL Server's one trigger carries the declared name.
func (d *Dialect) DropTriggerSQL(relation, name string) []string {
	return []string{"if object_id(" + nLiteral(name) + ", N'TR') is not null drop trigger " + d.QuoteIdentifier(name)}
}

// TriggerDefinitionSQL reads the text as submitted from sys.sql_modules, joined to sys.triggers.
func (*Dialect) TriggerDefinitionSQL() string {
	return "select m.definition as [definition] from sys.sql_modules m join sys.triggers t on t.object_id = m.object_id\n" +
		"where t.object_id = object_id(@relation)"
}

// expandToken is the token table of spec/metadata-model.md for SQL Server: the row tokens are the
// pseudo-tables whatever columns they list, `{now}` the UTC instant as a datetimeoffset.
func expandToken(token core.TriggerToken) string {
	switch token.Kind {
	case core.TokenNew:
		return "inserted"
	case core.TokenOld:
		return "deleted"
	}
	return "todatetimeoffset(sysutcdatetime(), 0)"
}
