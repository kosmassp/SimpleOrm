package postgres

import (
	"strings"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
)

// The PostgreSQL rendering of a trigger declaration (Level 4, ADR-0042, spec/dialects.md "Triggers"; pinned
// byte for byte by conformance/trigger-sql/): a trigger function `<name>_fn` holding the body between
// `begin`/`end` with the rendering's own `return`, and a row-level `create trigger` calling it. The function is
// replaced (`create or replace`), the trigger dropped first by the step.

// pgNowExpansion is `{now}`; the session's `timestamptz` is UTC-marked on read (spec/dialects.md Temporals).
const pgNowExpansion = "now()"

// CreateTriggerSQL renders the function, then the trigger. `instead of` exists on views only (DDL-005 for a
// table); `before` and `after` are native, and every event narrows with PostgreSQL's native `update of`.
func (d *Dialect) CreateTriggerSQL(relation string, onView bool, trigger core.TriggerDefinition) ([]string, error) {
	if trigger.Timing == core.TimingInsteadOf && !onView {
		return nil, core.Errorf("DDL-005", trigger.Name,
			"PostgreSQL has INSTEAD OF triggers on views only; %s is a table (use a before or after trigger)", relation)
	}

	function := d.QuoteIdentifier(trigger.Name + "_fn")
	body, err := core.RenderTriggerBody(trigger.Name, trigger.SQL, expandToken(d))
	if err != nil {
		return nil, err
	}

	tail := "\n    return null;\nend\n"
	if trigger.Timing != core.TimingAfter {
		// A row-level before/instead-of trigger's return value is the row the write proceeds with.
		tail = "\n    if tg_op = 'DELETE' then return old; end if;\n    return new;\nend\n"
	}

	events := make([]string, 0, 3)
	for _, event := range trigger.Events.List() {
		spelled := event.Token()
		if event == core.EventUpdate && len(trigger.UpdateOf) > 0 {
			spelled += " of " + strings.Join(d.quotedNames(trigger.UpdateOf), ", ")
		}
		events = append(events, spelled)
	}

	timing := trigger.Timing.Token()
	if trigger.Timing == core.TimingInsteadOf {
		timing = "instead of"
	}

	return []string{
		"create or replace function " + function + "() returns trigger language plpgsql as $$\nbegin\n" + body + tail + "$$",
		"create trigger " + d.QuoteIdentifier(trigger.Name) + " " + timing + " " + strings.Join(events, " or ") +
			" on " + d.QuoteIdentifier(relation) + " for each row execute function " + function + "()",
	}, nil
}

// DropTriggerSQL drops the trigger on its table, then its function.
func (d *Dialect) DropTriggerSQL(relation, name string) []string {
	return []string{
		"drop trigger if exists " + d.QuoteIdentifier(name) + " on " + d.QuoteIdentifier(relation),
		"drop function if exists " + d.QuoteIdentifier(name+"_fn") + "()",
	}
}

// TriggerDefinitionSQL reconstructs both statements from the catalog in the dialect's own spelling — the
// function from pg_proc.prosrc (the body verbatim) and the trigger from pg_trigger.tgtype and tgattr — so
// each row, whitespace-collapsed, equals the matching CreateTriggerSQL statement.
func (*Dialect) TriggerDefinitionSQL() string {
	return "select 'create or replace function \"' || p.proname || '\"() returns trigger language plpgsql as $$' || p.prosrc || '$$' as definition\n" +
		"from pg_trigger t\n" +
		"join pg_proc p on p.oid = t.tgfoid\n" +
		"where t.tgname = @relation and not t.tgisinternal\n" +
		"union all\n" +
		"select 'create trigger \"' || t.tgname || '\" '\n" +
		"    || case when (t.tgtype & 2) <> 0 then 'before' when (t.tgtype & 64) <> 0 then 'instead of' else 'after' end\n" +
		"    || ' '\n" +
		"    || array_to_string(array_remove(array[\n" +
		"           case when (t.tgtype & 4) <> 0 then 'insert' end,\n" +
		"           case when (t.tgtype & 16) <> 0 then 'update' || coalesce((\n" +
		"               select ' of ' || string_agg('\"' || a.attname || '\"', ', ' order by k.ord)\n" +
		"               from unnest(t.tgattr::smallint[]) with ordinality as k(attnum, ord)\n" +
		"               join pg_attribute a on a.attrelid = t.tgrelid and a.attnum = k.attnum), '') end,\n" +
		"           case when (t.tgtype & 8) <> 0 then 'delete' end], null), ' or ')\n" +
		"    || ' on \"' || c.relname || '\" for each row execute function \"' || p.proname || '\"()' as definition\n" +
		"from pg_trigger t\n" +
		"join pg_class c on c.oid = t.tgrelid\n" +
		"join pg_proc p on p.oid = t.tgfoid\n" +
		"where t.tgname = @relation and not t.tgisinternal"
}

// expandToken is the token table of spec/metadata-model.md for PostgreSQL: a row token is a one-row derived
// table over NEW or OLD with quoted names, `{now}` is now().
func expandToken(d *Dialect) func(core.TriggerToken) string {
	return func(token core.TriggerToken) string {
		switch token.Kind {
		case core.TokenNew:
			return d.rowRelation("new", token.Columns)
		case core.TokenOld:
			return d.rowRelation("old", token.Columns)
		}
		return pgNowExpansion
	}
}

// rowRelation is `(select new."a" as "a", new."b" as "b")`.
func (d *Dialect) rowRelation(row string, columns []string) string {
	parts := make([]string, len(columns))
	for i, column := range columns {
		quoted := d.QuoteIdentifier(column)
		parts[i] = row + "." + quoted + " as " + quoted
	}
	return "(select " + strings.Join(parts, ", ") + ")"
}

func (d *Dialect) quotedNames(names []string) []string {
	out := make([]string, len(names))
	for i, name := range names {
		out[i] = d.QuoteIdentifier(name)
	}
	return out
}
