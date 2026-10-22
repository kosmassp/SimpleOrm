package postgres

// The PostgreSQL shadow host (ADR-0043, spec/dialects.md "Migration tooling"): the throwaway database is
// `simpleorm_shadow_<hex>`, created on the admin connection and dropped with `drop database … with (force)`
// after the pools are cleared. PostgreSQL's catalog is lossless for triggers (the function body is kept
// verbatim and the trigger header is reconstructed from the catalog), so the host also reads a trigger back:
// the inverse of CreateTriggerSQL over the rows of TriggerDefinitionSQL.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
	"github.com/kosmassp/SimpleOrm/go/orm/migrations"
)

// ShadowHost provisions a `simpleorm_shadow_<hex>` database on the admin connection and reads a trigger back
// from the reconstructed catalog rows.
type ShadowHost struct{}

var (
	_ migrations.ShadowHost      = ShadowHost{}
	_ migrations.TriggerReadBack = ShadowHost{}
)

// Provision creates the database and returns the connection string that opens it with the function that drops it.
func (ShadowHost) Provision(ctx context.Context, admin string) (string, func(context.Context) error, error) {
	dialect := New()
	name := "simpleorm_shadow_" + randomHex(8)
	quoted := dialect.QuoteIdentifier(name)

	adminPool, err := dialect.CreateConnection(admin)
	if err != nil {
		return "", nil, err
	}
	if _, err := adminPool.ExecContext(ctx, "create database "+quoted); err != nil {
		_ = adminPool.Close()
		return "", nil, err
	}
	drop := func(ctx context.Context) error {
		defer adminPool.Close()
		_, err := adminPool.ExecContext(ctx, "drop database if exists "+quoted+" with (force)")
		return err
	}
	return connectionStringForDatabase(admin, name), drop, nil
}

// connectionStringForDatabase is the admin connection string pointed at one database: the path of a URL, else
// a key/value DSN with `dbname` appended (the later setting wins).
func connectionStringForDatabase(admin, database string) string {
	if u, err := url.Parse(admin); err == nil && (u.Scheme == "postgres" || u.Scheme == "postgresql") {
		u.Path = "/" + database
		return u.String()
	}
	return admin + " dbname=" + database
}

func randomHex(n int) string {
	buf := make([]byte, n)
	_, _ = rand.Read(buf) // crypto/rand only fails when the OS entropy source is broken
	return hex.EncodeToString(buf)
}

// --- trigger read-back ------------------------------------------------------------

// The fixed grammar of what TriggerDefinitionSQL reconstructs (and CreateTriggerSQL renders), after
// NormalizeTriggerDDL.
var (
	functionRowPattern = regexp.MustCompile(
		`^create or replace function "([^"]+)"\(\) returns trigger language plpgsql as \$\$ begin (.*) end \$\$$`)
	triggerRowPattern = regexp.MustCompile(
		`^create trigger "([^"]+)" (before|after|instead of) (.+) on "([^"]+)" for each row execute function "([^"]+)"\(\)$`)
	// rowExpansionPattern is (select new."a" as "a", new."b" as "b") over new or old.
	rowExpansionPattern = regexp.MustCompile(`\(select (?:new|old)\."[^"]+" as "[^"]+"(?:, (?:new|old)\."[^"]+" as "[^"]+")*\)`)
	eventPattern        = regexp.MustCompile(`^(?:insert|update(?: of "[^"]+"(?:, "[^"]+")*)?|delete)(?: or (?:insert|update(?: of "[^"]+"(?:, "[^"]+")*)?|delete))*$`)
)

const (
	afterTail  = "return null;"
	beforeTail = "if tg_op = 'DELETE' then return old; end if; return new;"
)

// ReadBackTrigger parses the two reconstructed rows — the function and the trigger — back to the declaration
// they spell: the header's timing and events (`update of "a", "b"` carrying the narrowing), the relation, the
// function body between `begin` and the rendering's own `return` tail, `(select new."a" as "a", …)` folded
// back to `{new: a, …}` and `now()` to `{now}`. nil with an empty note when there are no rows; nil with a
// note when a row does not match the grammar (a hand-written trigger), the pair disagrees with itself, or the
// tail is not the rendering's own.
func (ShadowHost) ReadBackTrigger(name string, rows []string) (*migrations.TriggerDeclaration, string) {
	if len(rows) == 0 {
		return nil, ""
	}
	skip := func(why string) (*migrations.TriggerDeclaration, string) {
		return nil, fmt.Sprintf("trigger %s: %s; skipped", name, why)
	}

	var function, trigger []string
	for _, row := range rows {
		normalized := migrations.NormalizeTriggerDDL(row)
		switch {
		case functionRowPattern.MatchString(normalized):
			function = functionRowPattern.FindStringSubmatch(normalized)
		case triggerRowPattern.MatchString(normalized):
			trigger = triggerRowPattern.FindStringSubmatch(normalized)
		default:
			return skip("the stored statement does not match the rendering grammar (a hand-written trigger)")
		}
	}
	if function == nil || trigger == nil {
		return skip("the function and the trigger are not both present")
	}
	if trigger[1] != name || trigger[5] != function[1] || function[1] != name+"_fn" {
		return skip("the stored names are not the ones the renderer would have given it")
	}

	timing := core.TimingAfter
	tail := afterTail
	switch trigger[2] {
	case "before":
		timing, tail = core.TimingBefore, beforeTail
	case "instead of":
		timing, tail = core.TimingInsteadOf, beforeTail
	}

	body := strings.TrimSpace(function[2])
	if !strings.HasSuffix(body, tail) {
		return skip("the function does not end with the rendering's own return")
	}
	body = strings.TrimSpace(strings.TrimSuffix(body, tail))

	events, updateOf, ok := parseEvents(trigger[3])
	if !ok {
		return skip("the event list does not match the rendering grammar")
	}

	return &migrations.TriggerDeclaration{
		Name: name, Relation: trigger[4], Timing: timing, Events: events, UpdateOf: updateOf,
		SQL: core.NormalizeTriggerBody(foldPostgresExpansions(body)),
	}, ""
}

// parseEvents reads `insert or update of "a", "b" or delete` back to the event set and the update-of list.
func parseEvents(phrase string) (core.TriggerEvents, []string, bool) {
	if !eventPattern.MatchString(phrase) {
		return 0, nil, false
	}
	var events core.TriggerEvents
	var updateOf []string
	for _, part := range splitEvents(phrase) {
		switch {
		case part == "insert":
			events |= core.EventInsert
		case part == "delete":
			events |= core.EventDelete
		default: // update[ of "a", "b"]
			events |= core.EventUpdate
			if columns, narrowed := strings.CutPrefix(part, "update of "); narrowed {
				for _, column := range strings.Split(columns, ", ") {
					updateOf = append(updateOf, strings.Trim(column, `"`))
				}
			}
		}
	}
	return events, updateOf, true
}

// splitEvents splits on " or " between events; a quoted column name never contains the separator's keyword
// followed by an event word in the grammar the renderer writes, so the plain split is exact for what matched
// eventPattern.
func splitEvents(phrase string) []string {
	return strings.Split(phrase, " or ")
}

// foldPostgresExpansions is the inverse of expandToken: `now()` back to `{now}`, a one-row derived table over
// new or old with quoted names back to `{new: cols}` / `{old: cols}`. An occurrence that is not exactly the
// expansion (mixed rows, an alias that differs from its column) is left as written.
func foldPostgresExpansions(body string) string {
	body = strings.ReplaceAll(body, pgNowExpansion, "{now}")
	return rowExpansionPattern.ReplaceAllStringFunc(body, func(expansion string) string {
		inner := strings.TrimSuffix(strings.TrimPrefix(expansion, "(select "), ")")
		var kind string
		columns := make([]string, 0, 4)
		for _, part := range strings.Split(inner, ", ") {
			source, alias, found := strings.Cut(part, " as ")
			row, column, dotted := strings.Cut(source, ".")
			if !found || !dotted || column != alias || (kind != "" && kind != row) {
				return expansion
			}
			kind = row
			columns = append(columns, strings.Trim(column, `"`))
		}
		return "{" + kind + ": " + strings.Join(columns, ", ") + "}"
	})
}
