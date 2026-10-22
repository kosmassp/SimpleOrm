package orm_test

// Triggers at the session level (Level 4, ADR-0042): CreateTriggers with replace semantics, what the fixture
// trigger does on a real SQLite database, the refusals, and the registry-wide scan SchemaGuard runs.

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/kosmassp/SimpleOrm/go/orm"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/testsupport"
	"github.com/kosmassp/SimpleOrm/go/orm/sample"
	"github.com/kosmassp/SimpleOrm/go/orm/sqlite"
)

func TestCreateTriggers_TheFixtureTriggerFiresOncePerUpdatedRowAndIsReplacedNotDuplicated(t *testing.T) {
	ctx := context.Background()
	db := testsupport.OpenSample(t)

	// Replace semantics: dropped if present, then created — twice leaves one trigger.
	for range 2 {
		if err := orm.CreateTriggers[sample.Role](ctx, db); err != nil {
			t.Fatalf("create triggers: %v", err)
		}
	}

	roles := []*sample.Role{{Name: "admin"}, {Name: "editor"}}
	for _, role := range roles {
		if err := orm.Insert(ctx, db, role); err != nil {
			t.Fatal(err)
		}
	}
	if changes, err := orm.QueryAll[sample.RoleChange](ctx, db); err != nil || len(changes) != 0 {
		t.Fatalf("an insert is no update: %d rows, %v", len(changes), err)
	}

	roles[0].Name = "administrator"
	if err := orm.Update(ctx, db, roles[0]); err != nil {
		t.Fatal(err)
	}
	// One statement updating both rows: the row-level trigger fires for each.
	touch := orm.InlineCommand[orm.EmptyArgs]("update roles set is_deleted = 0")
	if affected, err := orm.Execute(ctx, db, touch, orm.EmptyArgs{}); err != nil || affected != 2 {
		t.Fatalf("touch: %d, %v", affected, err)
	}

	changes, err := orm.QueryAll[sample.RoleChange](ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 3 {
		t.Fatalf("one role_changes row per updated role (1 + 2), got %d: %+v", len(changes), changes)
	}
	if changes[0].RoleID != roles[0].ID || changes[0].RoleName != "administrator" || changes[0].ChangedAt.IsZero() {
		t.Errorf("the first change records the renamed role and the {now} instant: %+v", changes[0])
	}
}

// insteadOfOnATable asks a dialect for a form SQLite lacks (instead-of on a table).
type insteadOfOnATable struct {
	ID int64 `orm:"column,key,generated"`
}

func (insteadOfOnATable) Entity() orm.EntityDef {
	return orm.EntityDef{
		Source:   orm.Table("instead_of_tables"),
		Triggers: []orm.TriggerLink{orm.Trigger[insteadOfTrigger]()},
	}
}

type insteadOfTrigger struct{}

func (insteadOfTrigger) Trigger() orm.TriggerDef {
	return orm.TriggerDef{Name: "trg_instead", Timing: orm.TimingInsteadOf, Events: orm.EventDelete, SQL: "select 1;"}
}

func TestCreateTriggers_ARefusedFormIsDDL005AndASourceThatTakesNoTriggerIsDDL001(t *testing.T) {
	ctx := context.Background()
	db := testsupport.OpenSample(t)

	if err := orm.CreateTriggers[insteadOfOnATable](ctx, db); orm.CodeOf(err) != "DDL-005" {
		t.Errorf("expected DDL-005, got %v", err)
	}
	if err := orm.CreateTriggers[sample.UserActivityReport](ctx, db); orm.CodeOf(err) != "DDL-001" {
		t.Errorf("a procedure takes no trigger: expected DDL-001, got %v", err)
	}
	// A table with nothing linked is a no-op, not an error.
	if err := orm.CreateTriggers[sample.User](ctx, db); err != nil {
		t.Errorf("User links no trigger: %v", err)
	}
}

// twiceLinkedA and twiceLinkedB link one class: the conflict a lone load cannot see.
type twiceLinkedA struct {
	ID       int64  `orm:"column,key,generated"`
	RoleName string `orm:"column"`
}

func (twiceLinkedA) Entity() orm.EntityDef {
	return orm.EntityDef{Triggers: []orm.TriggerLink{orm.Trigger[sample.RoleAuditTrigger]()}}
}

type twiceLinkedB struct {
	ID       int64  `orm:"column,key,generated"`
	RoleName string `orm:"column"`
}

func (twiceLinkedB) Entity() orm.EntityDef {
	return orm.EntityDef{Triggers: []orm.TriggerLink{orm.Trigger[sample.RoleAuditTrigger]()}}
}

func TestSchemaGuard_TheScanRuleReportsATriggerClassLinkedByTwoEntitiesAndSkipsTriggerClasses(t *testing.T) {
	ctx := context.Background()
	db := testsupport.OpenSample(t)

	// A trigger class among the entities is skipped, like the C# scan skips it; the link conflict is one MAP-029.
	err := orm.Validate(ctx, db, orm.Registry{
		Entities: []reflect.Type{orm.TypeOf[twiceLinkedA](), orm.TypeOf[twiceLinkedB](), orm.TypeOf[sample.RoleAuditTrigger]()},
		Triggers: []reflect.Type{orm.TypeOf[sample.RoleAuditTrigger]()},
	})
	var report *orm.ValidationErrors
	if !errors.As(err, &report) {
		t.Fatalf("expected a ValidationErrors report, got %v", err)
	}
	conflicts := 0
	for _, violation := range report.Errors {
		if violation.Code == "MAP-029" {
			conflicts++
		}
		if violation.Source == "RoleAuditTrigger" && violation.Code != "MAP-029" {
			t.Errorf("the trigger class was validated as an entity: %+v", violation)
		}
	}
	if conflicts != 1 {
		t.Errorf("expected exactly one MAP-029 conflict, got %d: %v", conflicts, report.Errors)
	}
}

func TestSampleRegistry_ListsTheTriggerClassAndTheSampleDatabaseValidatesClean(t *testing.T) {
	ctx := context.Background()
	path, registry := migratedSampleDatabase(t)
	if len(registry.Triggers) != 1 {
		t.Fatalf("the sample registry lists its trigger class: %v", registry.Triggers)
	}
	db, err := orm.Open(ctx, path, orm.Options{Dialect: sqlite.New()})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := orm.Validate(ctx, db, registry); err != nil {
		t.Fatalf("the migrated sample (RoleChange and the trigger included) validates clean: %v", err)
	}
}
