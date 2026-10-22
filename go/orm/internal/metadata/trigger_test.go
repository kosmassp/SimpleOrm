package metadata_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/kosmassp/SimpleOrm/go/orm"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/metadata"
	"github.com/kosmassp/SimpleOrm/go/orm/sample"
)

// Triggers in the mapping (Level 4, ADR-0042, spec/metadata-model.md "Triggers"): the declaration, the link,
// every MAP-029 rule, the scan rule, and the export.

// triggerWidget is the entity the trigger fixtures link to; a builder registration varies the links per test.
type triggerWidget struct {
	ID    int64  `orm:"column,key,generated"`
	Name  string `orm:"column"`
	Price int64  `orm:"column"`
}

func goodTrigger() orm.TriggerDef {
	return orm.TriggerDef{
		Name: "trg_widgets_audit", Timing: orm.TimingAfter, Events: orm.EventUpdate,
		SQL: "insert into widget_log (widget_id) select n.id from {new: id} n;",
	}
}

type goodTriggerClass struct{}

func (goodTriggerClass) Trigger() orm.TriggerDef { return goodTrigger() }

type blankNameTrigger struct{}

func (blankNameTrigger) Trigger() orm.TriggerDef {
	d := goodTrigger()
	d.Name = "  "
	return d
}

type noTimingTrigger struct{}

func (noTimingTrigger) Trigger() orm.TriggerDef {
	d := goodTrigger()
	d.Timing = 0
	return d
}

type noEventTrigger struct{}

func (noEventTrigger) Trigger() orm.TriggerDef {
	d := goodTrigger()
	d.Events = 0
	return d
}

type blankBodyTrigger struct{}

func (blankBodyTrigger) Trigger() orm.TriggerDef {
	d := goodTrigger()
	d.SQL = "\n  "
	return d
}

type updateOfWithoutUpdateTrigger struct{}

func (updateOfWithoutUpdateTrigger) Trigger() orm.TriggerDef {
	d := goodTrigger()
	d.Events, d.UpdateOf = orm.EventInsert, []string{"Name"}
	return d
}

type updateOfUnmappedTrigger struct{}

func (updateOfUnmappedTrigger) Trigger() orm.TriggerDef {
	d := goodTrigger()
	d.UpdateOf = []string{"Nope"}
	return d
}

type unknownTokenTrigger struct{}

func (unknownTokenTrigger) Trigger() orm.TriggerDef {
	d := goodTrigger()
	d.SQL = "select {nope} from x;"
	return d
}

type rowTokenWithoutColumnsTrigger struct{}

func (rowTokenWithoutColumnsTrigger) Trigger() orm.TriggerDef {
	d := goodTrigger()
	d.SQL = "select n.id from {new} n;"
	return d
}

type lineCommentTrigger struct{}

func (lineCommentTrigger) Trigger() orm.TriggerDef {
	d := goodTrigger()
	d.SQL = "select {now}; -- whitespace collapse would fold the rest into it"
	return d
}

type unmappedColumnTrigger struct{}

func (unmappedColumnTrigger) Trigger() orm.TriggerDef {
	d := goodTrigger()
	d.SQL = "select n.id from {new: id, nope} n;"
	return d
}

type newOnDeleteTrigger struct{}

func (newOnDeleteTrigger) Trigger() orm.TriggerDef {
	d := goodTrigger()
	d.Events = orm.EventDelete
	return d
}

type oldOnInsertTrigger struct{}

func (oldOnInsertTrigger) Trigger() orm.TriggerDef {
	d := goodTrigger()
	d.Events, d.SQL = orm.EventInsert, "select o.id from {old: id} o;"
	return d
}

// A row token needs rows on every declared event (the one body serves them all).
type newOnInsertAndDeleteTrigger struct{}

func (newOnInsertAndDeleteTrigger) Trigger() orm.TriggerDef {
	d := goodTrigger()
	d.Events, d.SQL = orm.EventInsert|orm.EventDelete, "select n.id from {new: id} n;"
	return d
}

type oldOnUpdateAndInsertTrigger struct{}

func (oldOnUpdateAndInsertTrigger) Trigger() orm.TriggerDef {
	d := goodTrigger()
	d.Events, d.SQL = orm.EventUpdate|orm.EventInsert, "select o.id from {old: id} o;"
	return d
}

type beforeTrigger struct{}

func (beforeTrigger) Trigger() orm.TriggerDef {
	d := goodTrigger()
	d.Timing = orm.TimingBefore
	return d
}

type insteadOfTrigger struct{}

func (insteadOfTrigger) Trigger() orm.TriggerDef {
	d := goodTrigger()
	d.Timing, d.Events = orm.TimingInsteadOf, orm.EventInsert
	return d
}

// undeclaredClass carries no Trigger method.
type undeclaredClass struct{}

// entityTriggerClass is a trigger class that is also an entity (it has mapping tags).
type entityTriggerClass struct {
	ID int64 `orm:"column,key,generated"`
}

func (entityTriggerClass) Trigger() orm.TriggerDef { return goodTrigger() }

// ownedTriggerClass is a trigger class that is also an owned type.
type ownedTriggerClass struct{}

func (ownedTriggerClass) OwnedType()              {}
func (ownedTriggerClass) Trigger() orm.TriggerDef { return goodTrigger() }

type literalBraceTrigger struct{}

func (literalBraceTrigger) Trigger() orm.TriggerDef {
	d := goodTrigger()
	d.SQL = `insert into widget_log (payload) values ('{"a": 1}', '-- {nope}');`
	return d
}

type multiEventTrigger struct{}

func (multiEventTrigger) Trigger() orm.TriggerDef {
	return orm.TriggerDef{
		Name: "trg_widgets_changes", Timing: orm.TimingAfter, Events: orm.EventDelete | orm.EventUpdate,
		UpdateOf: []string{"Name", "Price"},
		SQL:      "insert into widget_log (widget_id, at) select o.id, {now} from {old: id} o;",
	}
}

// widgetWith loads triggerWidget through the manual builder with the given links — the builder links trigger
// classes under the descriptor's rules, so one entity type serves every table-source case.
func widgetWith(links ...orm.TriggerLink) (*core.EntityMap, error) {
	builder := orm.NewEntityMapBuilder[triggerWidget]()
	builder.ToTable("widgets")
	builder.Property("ID").Key().Generated()
	builder.Property("Name").Column("widget_name")
	builder.Property("Price")
	for _, link := range links {
		builder.Trigger(link)
	}
	options := &metadata.Options{}
	options.Register(builder)
	return metadata.NewLoader(options).Load(reflect.TypeFor[triggerWidget]())
}

func TestTriggers_AValidLinkResolvesToItsNormalizedDefinition(t *testing.T) {
	m, err := widgetWith(orm.Trigger[multiEventTrigger]())
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Triggers) != 1 {
		t.Fatalf("got %d triggers", len(m.Triggers))
	}
	definition := m.Triggers[0].Definition
	if m.Triggers[0].Type != reflect.TypeFor[multiEventTrigger]() {
		t.Errorf("the link keeps its class: %v", m.Triggers[0].Type)
	}
	if definition.Name != "trg_widgets_changes" || definition.Timing != orm.TimingAfter ||
		definition.Events != orm.EventUpdate|orm.EventDelete {
		t.Errorf("definition: %+v", definition)
	}
	// UpdateOf names properties; the definition holds columns (Name is mapped to widget_name).
	if !reflect.DeepEqual(definition.UpdateOf, []string{"widget_name", "price"}) {
		t.Errorf("UpdateOf = %v, want [widget_name price]", definition.UpdateOf)
	}
	want := "insert into widget_log (widget_id, at) select o.id, {now} from {old: id} o;"
	if got := core.NormalizeTriggerBody(definition.SQL); got != want {
		t.Errorf("normalized SQL = %q, want %q", got, want)
	}
}

func TestTriggers_LinkOrderIsTheMapsOrder(t *testing.T) {
	m, err := widgetWith(orm.Trigger[multiEventTrigger](), orm.Trigger[goodTriggerClass]())
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Triggers) != 2 || m.Triggers[0].Definition.Name != "trg_widgets_changes" || m.Triggers[1].Definition.Name != "trg_widgets_audit" {
		t.Errorf("triggers: %+v", m.Triggers)
	}
}

func TestTriggers_BracesInLiteralsAreNotTokens(t *testing.T) {
	if _, err := widgetWith(orm.Trigger[literalBraceTrigger]()); err != nil {
		t.Fatalf("a JSON literal in a body is safe: %v", err)
	}
}

func TestTriggers_EveryMisplacedOrMalformedDeclarationIsMAP029(t *testing.T) {
	cases := map[string]orm.TriggerLink{
		"a link to a class with no declaration":   orm.Trigger[undeclaredClass](),
		"a link to a class that is an entity":     orm.Trigger[entityTriggerClass](),
		"a link to a class that is an owned type": orm.Trigger[ownedTriggerClass](),
		"a blank name":                          orm.Trigger[blankNameTrigger](),
		"no timing":                             orm.Trigger[noTimingTrigger](),
		"no event":                              orm.Trigger[noEventTrigger](),
		"a blank body":                          orm.Trigger[blankBodyTrigger](),
		"UpdateOf without the update event":     orm.Trigger[updateOfWithoutUpdateTrigger](),
		"UpdateOf naming an unmapped property":  orm.Trigger[updateOfUnmappedTrigger](),
		"an unknown token":                      orm.Trigger[unknownTokenTrigger](),
		"a row token without columns":           orm.Trigger[rowTokenWithoutColumnsTrigger](),
		"a -- line comment":                     orm.Trigger[lineCommentTrigger](),
		"{new} with delete beside insert":       orm.Trigger[newOnInsertAndDeleteTrigger](),
		"{old} with insert beside update":       orm.Trigger[oldOnUpdateAndInsertTrigger](),
		"a row token naming an unmapped column": orm.Trigger[unmappedColumnTrigger](),
		"{new} on a delete-only trigger":        orm.Trigger[newOnDeleteTrigger](),
		"{old} on an insert-only trigger":       orm.Trigger[oldOnInsertTrigger](),
		"a link with no class":                  {},
	}
	for name, link := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := widgetWith(link); !core.HasCode(err, "MAP-029") {
				t.Errorf("expected MAP-029, got %v", err)
			}
		})
	}
}

func TestTriggers_AClassLinkedTwiceByOneEntityIsMAP029(t *testing.T) {
	_, err := widgetWith(orm.Trigger[goodTriggerClass](), orm.Trigger[goodTriggerClass]())
	if !core.HasCode(err, "MAP-029") {
		t.Errorf("expected MAP-029, got %v", err)
	}
}

func TestTriggers_EveryViolationIsCollectedNeverTheFirstOnly(t *testing.T) {
	_, err := widgetWith(orm.Trigger[blankNameTrigger](), orm.Trigger[noEventTrigger](), orm.Trigger[unknownTokenTrigger]())
	var mapping *core.MappingErrors
	if !asMappingErrors(err, &mapping) {
		t.Fatalf("expected MappingErrors, got %v", err)
	}
	if len(mapping.Errors) < 3 {
		t.Errorf("expected at least three collected errors, got %d: %v", len(mapping.Errors), mapping)
	}
}

func asMappingErrors(err error, target **core.MappingErrors) bool {
	mapping, ok := err.(*core.MappingErrors)
	*target = mapping
	return ok
}

// --- sources ----------------------------------------------------------------------------------------------

type triggerStatementEntity struct {
	ID int64 `orm:"column"`
}

func (triggerStatementEntity) Entity() orm.EntityDef {
	return orm.EntityDef{
		Source:   orm.Statement("select 1 as id"),
		Triggers: []orm.TriggerLink{orm.Trigger[goodTriggerClass]()},
	}
}

type triggerProcedureEntity struct {
	ID int64 `orm:"column"`
}

func (triggerProcedureEntity) Entity() orm.EntityDef {
	return orm.EntityDef{
		Source:   orm.Procedure("p", "select 1 as id"),
		Triggers: []orm.TriggerLink{orm.Trigger[goodTriggerClass]()},
	}
}

type triggerMaterializedEntity struct {
	ID int64 `orm:"column"`
}

func (triggerMaterializedEntity) Entity() orm.EntityDef {
	return orm.EntityDef{
		Source:   orm.MaterializedView("mv", "select 1 as id"),
		Triggers: []orm.TriggerLink{orm.Trigger[goodTriggerClass]()},
	}
}

type triggerViewEntity struct {
	ID int64 `orm:"column"`
}

func (triggerViewEntity) Entity() orm.EntityDef {
	return orm.EntityDef{
		Source:   orm.View("widget_summaries", "select 1 as id"),
		Triggers: []orm.TriggerLink{orm.Trigger[insteadOfTrigger]()},
	}
}

type triggerViewAfterEntity struct {
	ID int64 `orm:"column"`
}

func (triggerViewAfterEntity) Entity() orm.EntityDef {
	return orm.EntityDef{
		Source:   orm.View("widget_summaries", "select 1 as id"),
		Triggers: []orm.TriggerLink{orm.Trigger[goodTriggerClass]()},
	}
}

type triggerViewBeforeEntity struct {
	ID int64 `orm:"column"`
}

func (triggerViewBeforeEntity) Entity() orm.EntityDef {
	return orm.EntityDef{
		Source:   orm.View("widget_summaries", "select 1 as id"),
		Triggers: []orm.TriggerLink{orm.Trigger[beforeTrigger]()},
	}
}

func TestTriggers_OnlyTablesAndViewsTakeATriggerAndAViewOnlyInsteadOf(t *testing.T) {
	loader := metadata.NewLoader(nil)

	for name, entity := range map[string]reflect.Type{
		"a statement":                reflect.TypeFor[triggerStatementEntity](),
		"a procedure":                reflect.TypeFor[triggerProcedureEntity](),
		"a materialized view":        reflect.TypeFor[triggerMaterializedEntity](),
		"an after trigger on a view": reflect.TypeFor[triggerViewAfterEntity](),
		"a before trigger on a view": reflect.TypeFor[triggerViewBeforeEntity](),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := loader.Load(entity); !core.HasCode(err, "MAP-029") {
				t.Errorf("expected MAP-029, got %v", err)
			}
		})
	}

	m, err := loader.Load(reflect.TypeFor[triggerViewEntity]())
	if err != nil {
		t.Fatalf("an instead-of trigger on a view is valid: %v", err)
	}
	if len(m.Triggers) != 1 || m.Triggers[0].Definition.Timing != orm.TimingInsteadOf {
		t.Errorf("triggers: %+v", m.Triggers)
	}
}

func TestTriggers_ALoadOfATriggerClassIsMAP029(t *testing.T) {
	loader := metadata.NewLoader(nil)
	for _, class := range []reflect.Type{reflect.TypeFor[goodTriggerClass](), reflect.TypeFor[*goodTriggerClass](), reflect.TypeFor[entityTriggerClass]()} {
		if _, err := loader.Load(class); !core.HasCode(err, "MAP-029") {
			t.Errorf("loading %v: expected MAP-029, got %v", class, err)
		}
	}
}

// --- the scan rule ----------------------------------------------------------------------------------------

type sameNameTriggerA struct{}

func (sameNameTriggerA) Trigger() orm.TriggerDef { return goodTrigger() }

type sameNameTriggerB struct{}

func (sameNameTriggerB) Trigger() orm.TriggerDef { return goodTrigger() }

type scanEntityOne struct {
	ID int64 `orm:"column,key,generated"`
}

func (scanEntityOne) Entity() orm.EntityDef {
	return orm.EntityDef{Triggers: []orm.TriggerLink{orm.Trigger[goodTriggerClass]()}}
}

type scanEntityTwo struct {
	ID int64 `orm:"column,key,generated"`
}

func (scanEntityTwo) Entity() orm.EntityDef {
	return orm.EntityDef{Triggers: []orm.TriggerLink{orm.Trigger[goodTriggerClass]()}}
}

type scanEntityThree struct {
	ID int64 `orm:"column,key,generated"`
}

func (scanEntityThree) Entity() orm.EntityDef {
	return orm.EntityDef{Triggers: []orm.TriggerLink{orm.Trigger[sameNameTriggerA]()}}
}

func TestScanTriggers_AClassLinkedByTwoEntitiesIsOneMAP029(t *testing.T) {
	loader := metadata.NewLoader(nil)
	conflicts := loader.ScanTriggers([]reflect.Type{reflect.TypeFor[scanEntityOne](), reflect.TypeFor[scanEntityTwo]()}, nil)
	if len(conflicts) != 1 || conflicts[0].Code != "MAP-029" || !strings.Contains(conflicts[0].Message, "scanEntityOne and scanEntityTwo") {
		t.Fatalf("conflicts: %v", conflicts)
	}
}

func TestScanTriggers_TwoClassesDeclaringOneNameAreOneMAP029EvenWhenOneIsUnlinked(t *testing.T) {
	loader := metadata.NewLoader(nil)
	// scanEntityThree links sameNameTriggerA; sameNameTriggerB is only registered (a class awaiting removal).
	conflicts := loader.ScanTriggers(
		[]reflect.Type{reflect.TypeFor[scanEntityThree]()},
		[]reflect.Type{reflect.TypeFor[sameNameTriggerA](), reflect.TypeFor[sameNameTriggerB]()})
	if len(conflicts) != 1 || conflicts[0].Code != "MAP-029" || !strings.Contains(conflicts[0].Message, "trg_widgets_audit") {
		t.Fatalf("conflicts: %v", conflicts)
	}
}

func TestScanTriggers_ASoundRegistrySeesNothingAndTriggerClassesAreSkippedAsEntities(t *testing.T) {
	loader := metadata.NewLoader(nil)
	entities := []reflect.Type{
		reflect.TypeFor[sample.Role](), reflect.TypeFor[sample.RoleChange](),
		reflect.TypeFor[sample.RoleAuditTrigger](), // a trigger class listed among the entities: skipped
	}
	if conflicts := loader.ScanTriggers(entities, []reflect.Type{reflect.TypeFor[sample.RoleAuditTrigger]()}); len(conflicts) != 0 {
		t.Errorf("conflicts: %v", conflicts)
	}
}

// --- the export -------------------------------------------------------------------------------------------

func TestTriggers_TheExportCarriesTheBlockAfterIndexesOnlyWhenDeclared(t *testing.T) {
	loader := metadata.NewLoader(nil)

	plain, err := widgetWith()
	if err != nil {
		t.Fatal(err)
	}
	exported, err := metadata.Export(plain, loader)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(exported, "triggers") {
		t.Errorf("an entity with no trigger exports none:\n%s", exported)
	}

	m, err := widgetWith(orm.Trigger[multiEventTrigger]())
	if err != nil {
		t.Fatal(err)
	}
	exported, err = metadata.Export(m, loader)
	if err != nil {
		t.Fatal(err)
	}
	want := `"triggers": [
    {
      "name": "trg_widgets_changes",
      "timing": "after",
      "events": [
        "update",
        "delete"
      ],
      "updateOf": [
        "widget_name",
        "price"
      ],
      "sql": "insert into widget_log (widget_id, at) select o.id, {now} from {old: id} o;"
    }
  ]`
	if !strings.Contains(exported, want) {
		t.Errorf("export:\n%s\nwant the block:\n%s", exported, want)
	}
	if strings.Contains(exported, "multiEventTrigger") {
		t.Error("the trigger class's name never appears in the export")
	}
}
