package sqlite

import (
	"strings"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
)

// The SQLite rendering of a trigger declaration (Level 4, ADR-0042, spec/dialects.md "Triggers"; pinned
// byte for byte by conformance/trigger-sql/): SQLite triggers are single-event and row-level, so one
// declaration becomes one `create trigger` per event — the declared name for a single event,
// `<name>_<event>` for several — each `for each row` with the body between `begin` and `end`.

// sqliteNowExpansion is `{now}`: UTC now in the §7.9 storage form.
const sqliteNowExpansion = "strftime('%Y-%m-%dT%H:%M:%fZ', 'now')"

// CreateTriggerSQL renders one statement per declared event. `instead of` exists on views only (DDL-005 for a
// table); `before` and `after` are native.
func (d *Dialect) CreateTriggerSQL(relation string, onView bool, trigger core.TriggerDefinition) ([]string, error) {
	if trigger.Timing == core.TimingInsteadOf && !onView {
		return nil, core.Errorf("DDL-005", trigger.Name,
			"SQLite has INSTEAD OF triggers on views only; %s is a table (use an after trigger)", relation)
	}

	body, err := core.RenderTriggerBody(trigger.Name, trigger.SQL, expandSQLiteToken)
	if err != nil {
		return nil, err
	}
	events := trigger.Events.List()
	statements := make([]string, 0, len(events))
	for _, event := range events {
		name := trigger.Name
		if len(events) > 1 {
			name += "_" + event.Token()
		}

		header := "create trigger " + d.QuoteIdentifier(name) + " " + sqliteTimingWord(trigger.Timing) + " " + event.Token()
		if event == core.EventUpdate && len(trigger.UpdateOf) > 0 {
			header += " of " + strings.Join(d.quoted(trigger.UpdateOf), ", ")
		}
		header += " on " + d.QuoteIdentifier(relation) + " for each row"

		statements = append(statements, header+"\nbegin\n"+body+"\nend")
	}
	return statements, nil
}

// DropTriggerSQL drops the declared name and every possible per-event name — a rendering of any event set
// can be replaced or removed without knowing which events it had.
func (d *Dialect) DropTriggerSQL(relation, name string) []string {
	statements := []string{"drop trigger if exists " + d.QuoteIdentifier(name)}
	for _, event := range []core.TriggerEvents{core.EventInsert, core.EventUpdate, core.EventDelete} {
		statements = append(statements, "drop trigger if exists "+d.QuoteIdentifier(name+"_"+event.Token()))
	}
	return statements
}

// TriggerDefinitionSQL reads the stored text of the declared name and its per-event forms — SQLite keeps the
// statement as submitted, the leading keywords recased.
func (*Dialect) TriggerDefinitionSQL() string {
	return "select sql as definition from sqlite_master where type = 'trigger'\n" +
		"and name in (@relation, @relation || '_insert', @relation || '_update', @relation || '_delete') order by name"
}

func sqliteTimingWord(timing core.TriggerTiming) string {
	if timing == core.TimingInsteadOf {
		return "instead of"
	}
	return timing.Token()
}

// expandSQLiteToken is the token table of spec/metadata-model.md for SQLite: a row token is a one-row derived
// table over the NEW or OLD row, `{now}` the strftime storage form.
func expandSQLiteToken(token core.TriggerToken) string {
	switch token.Kind {
	case core.TokenNew:
		return rowRelation("new", token.Columns)
	case core.TokenOld:
		return rowRelation("old", token.Columns)
	}
	return sqliteNowExpansion
}

// rowRelation is `(select new.a as a, new.b as b)`.
func rowRelation(row string, columns []string) string {
	parts := make([]string, len(columns))
	for i, column := range columns {
		parts[i] = row + "." + column + " as " + column
	}
	return "(select " + strings.Join(parts, ", ") + ")"
}

func (d *Dialect) quoted(names []string) []string {
	out := make([]string, len(names))
	for i, name := range names {
		out[i] = d.QuoteIdentifier(name)
	}
	return out
}
