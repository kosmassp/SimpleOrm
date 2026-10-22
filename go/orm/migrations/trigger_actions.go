package migrations

import (
	"sort"
	"strings"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
)

// TriggerActions are a trigger step's actions (Level 4, ADR-0042, spec/migrations.md "Steps and actions"): they
// execute in declaration order and every one renders through the dialect from a **declaration** — the
// entity's linked one (CreateTrigger, the initial create) or the step's own literal, frozen one (Recreate,
// ExpectDefinition — the forms `diff` emits) — plus DropTrigger. There is deliberately no raw-SQL action:
// trigger DDL is dialect-specific, and a step holding one dialect's text would tie the tree to it. Every
// create is drop-if-present then create (replace semantics). Built only by Set.Render (DDL-001 when the
// entity takes no trigger, and from CreateTrigger when it does not link the step's class).
type TriggerActions struct {
	name      string
	relation  string
	onView    bool
	entityMap *core.EntityMap
	linked    *core.EntityTrigger
	dialect   core.Dialect
	actions   []*MigrationAction
	err       error
}

func newTriggerActions(m *core.EntityMap, linked *core.EntityTrigger, name string, dialect core.Dialect) *TriggerActions {
	return &TriggerActions{
		name: name, relation: m.RelationName, onView: m.Kind == core.RelationView,
		entityMap: m, linked: linked, dialect: dialect,
	}
}

// CreateTrigger renders the entity's linked declaration — legal for the trigger's initial creation, frozen
// thereafter by the checksum. A step whose entity does not link the class has no declaration to render: DDL-001
// (schema creation that does not apply) when rendered.
func (a *TriggerActions) CreateTrigger() *MigrationAction {
	if a.linked == nil {
		a.fail(core.Errorf("DDL-001", a.entityMap.EntityName(),
			"does not link a trigger declaring '%s'; CreateTrigger renders from the metadata — use Recreate with a literal declaration", a.name))
		return a.track(newAction("create trigger "+a.name, nil))
	}
	return a.recreate("create trigger "+a.name, a.linked.Definition)
}

// Recreate drops the trigger if present and creates it from the literal declaration given — the step's own,
// frozen copy, so the rendered SQL never changes when the model evolves. updateOf names columns.
func (a *TriggerActions) Recreate(timing core.TriggerTiming, events core.TriggerEvents, sql string, updateOf ...string) *MigrationAction {
	return a.recreate("recreate trigger "+a.name, a.literal(timing, events, sql, updateOf))
}

// ExpectDefinition declares the MIG-012 apply guard: when this step applies, the live trigger statements must
// be, normalized, exactly the rendering of this declaration (the previous one — generated change steps always
// carry it). On a mismatch, or absent, the run refuses unless forced (spec/migrations.md).
func (a *TriggerActions) ExpectDefinition(timing core.TriggerTiming, events core.TriggerEvents, sql string, updateOf ...string) *MigrationAction {
	action := a.track(newAction("expect trigger "+a.name, nil))
	statement, err := a.guard(a.literal(timing, events, sql, updateOf), "expect trigger "+a.name)
	if err != nil {
		a.fail(err)
		return action
	}
	action.guard = statement
	return action
}

// DropTrigger drops the trigger and whatever the dialect created beside it.
func (a *TriggerActions) DropTrigger() *MigrationAction {
	return a.track(newAction("drop trigger "+a.name, a.dialect.DropTriggerSQL(a.relation, a.name)))
}

func (a *TriggerActions) recreate(origin string, definition core.TriggerDefinition) *MigrationAction {
	creates, err := a.dialect.CreateTriggerSQL(a.relation, a.onView, definition)
	if err != nil {
		a.fail(err)
		return a.track(newAction(origin, nil))
	}
	statements := append(a.dialect.DropTriggerSQL(a.relation, a.name), creates...)
	return a.track(newAction(origin, statements))
}

func (a *TriggerActions) literal(timing core.TriggerTiming, events core.TriggerEvents, sql string, updateOf []string) core.TriggerDefinition {
	return core.TriggerDefinition{Name: a.name, Timing: timing, Events: events, UpdateOf: updateOf, SQL: sql}
}

// guard renders a declaration to the MIG-012 trigger guard statement.
func (a *TriggerActions) guard(definition core.TriggerDefinition, origin string) (*MigrationStatement, error) {
	return triggerGuard(a.dialect, a.relation, a.onView, definition, origin)
}

func (a *TriggerActions) fail(err error) {
	if a.err == nil {
		a.err = err
	}
}

func (a *TriggerActions) track(action *MigrationAction) *MigrationAction {
	a.actions = append(a.actions, action)
	return action
}

func (a *TriggerActions) build() ([]MigrationStatement, error) {
	if a.err != nil {
		return nil, a.err
	}
	var out []MigrationStatement
	for _, action := range a.actions {
		out = append(out, action.render()...)
	}
	return out, nil
}

// triggerGuard renders definition through the dialect into the guard statement the runner checks: the
// expected live statements normalized, and SQL holding them joined so the step checksum counts the
// expectation (it is the dialect's rendering, like every other statement).
func triggerGuard(
	dialect core.Dialect, relation string, onView bool, definition core.TriggerDefinition, origin string,
) (*MigrationStatement, error) {
	creates, err := dialect.CreateTriggerSQL(relation, onView, definition)
	if err != nil {
		return nil, err
	}
	expected := make([]string, len(creates))
	for i, create := range creates {
		expected[i] = NormalizeTriggerDDL(create)
	}
	sort.Strings(expected) // the guard compares sets; the checksum joins them in this one order
	return &MigrationStatement{
		SQL: strings.Join(expected, "\n;\n"), Origin: origin, GuardTrigger: definition.Name, Expected: expected,
	}, nil
}
