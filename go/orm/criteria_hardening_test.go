package orm_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/kosmassp/SimpleOrm/go/orm"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/testsupport"
	"github.com/kosmassp/SimpleOrm/go/orm/sample"
)

// ADR-0020 add.1 — the adversarial-review fixes: degenerate composites render
// truth-values, negative paging refuses (QRY-008), the string-IN case
// (Go's In[T] closes the C# string-is-IEnumerable<char> trap by construction),
// null IN lists are named errors, ambiguous property names refuse, and IN
// expansion leaves literals/comments alone.

func TestFrom_EmptyCompositesRunAsTruthValues(t *testing.T) {
	ctx := context.Background()
	db := testsupport.OpenSample(t)
	testsupport.InsertUser(t, db, "EmptyComposite", "empty-composite@example.com")

	// Empty AND is true — the row comes back; empty OR is false — none do.
	all, err := orm.From[sample.User](db).
		Where(orm.And(), orm.Like("Email", "empty-composite@%")).List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 {
		t.Fatalf("expected 1, got %d", len(all))
	}

	none, err := orm.From[sample.User](db).
		Where(orm.Or(), orm.Like("Email", "empty-composite@%")).List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(none) != 0 {
		t.Fatalf("expected 0, got %d", len(none))
	}
}

func TestFrom_NegativePagingIsRefusedNotSilentlyUnlimited(t *testing.T) {
	ctx := context.Background()
	db := testsupport.OpenSample(t)

	_, err := orm.From[sample.User](db).Limit(-1).List(ctx)
	if orm.CodeOf(err) != "QRY-008" {
		t.Fatalf("expected QRY-008, got %v", err)
	}
	_, err = orm.From[sample.User](db).Offset(-3).List(ctx)
	if orm.CodeOf(err) != "QRY-008" {
		t.Fatalf("expected QRY-008, got %v", err)
	}
}

func TestFrom_SingleStringInMeansOneValueNotCharacters(t *testing.T) {
	ctx := context.Background()
	db := testsupport.OpenSample(t)
	ada := testsupport.InsertUser(t, db, "InTrap", "in-trap@example.com")

	// Go's In[T] is a plain generic over T — there is no IEnumerable<char> trap to guard against.
	rows, err := orm.From[sample.User](db).Where(orm.In("Name", "InTrap")).List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ID != ada.ID {
		t.Fatalf("got %+v", rows)
	}
}

func TestFrom_InExpansionLeavesLiteralsAndCommentsAlone(t *testing.T) {
	ctx := context.Background()
	db := testsupport.OpenSample(t)
	ada := testsupport.InsertUser(t, db, "ExpandLit", "expand-lit@ids")

	// '@ids' appears inside a string literal and a comment: only the real
	// placeholder expands (a naive rewrite would touch the literal too).
	type idsArgs struct{ Ids []int64 }
	query := orm.Inline[idsArgs, sample.User](
		"select id, name, email, display_name, created_at, updated_at from users " +
			"where email like '%@ids' -- narrows by @ids\n and id in (@ids)")
	rows, err := orm.Query(ctx, db, query, idsArgs{Ids: []int64{ada.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ID != ada.ID {
		t.Fatalf("got %+v", rows)
	}
}

// caseCollision pins Resolve's exact-case-wins / ambiguity-refuses rule
// (spec/query-ast.md) against a real map: "ID" and "Id" are distinct Go
// identifiers, so both can be mapped properties of the same entity.
type caseCollision struct {
	ID int64 `orm:"column=id_upper,key,generated"`
	Id int64 `orm:"column=id_lower"`
}

func (caseCollision) Entity() orm.EntityDef {
	return orm.EntityDef{Source: orm.Table("case_collisions")}
}

func TestFrom_AmbiguousCaseInsensitivePropertyRefusesInsteadOfFirstWins(t *testing.T) {
	ctx := context.Background()
	db := testsupport.OpenSample(t)
	if err := orm.CreateTable[caseCollision](ctx, db); err != nil {
		t.Fatal(err)
	}

	// An exact-case name always wins outright.
	if _, err := orm.From[caseCollision](db).Where(orm.Eq("ID", int64(1))).List(ctx); err != nil {
		t.Fatalf("expected the exact-case match to succeed, got %v", err)
	}

	// "iD" matches neither "ID" nor "Id" exactly, but both case-insensitively: ambiguous, never first-wins.
	_, err := orm.From[caseCollision](db).Where(orm.Eq("iD", int64(1))).List(ctx)
	if orm.CodeOf(err) != "QRY-006" {
		t.Fatalf("expected QRY-006, got %v", err)
	}
}

// stampedRow pins that UPDATE never writes a generated non-key column (§7.15/ADR-0020 add.1).
type stampedRow struct {
	ID    int64   `orm:"column,key,generated"`
	Label *string `orm:"column"`
	// Stamp is database-owned (e.g. a trigger-maintained timestamp): read, never written.
	Stamp *string `orm:"column,generated"`
}

func (stampedRow) Entity() orm.EntityDef { return orm.EntityDef{Source: orm.Table("stamped_rows")} }

func TestUpdateSQL_NeverTouchesGeneratedNonKeyColumns(t *testing.T) {
	db := testsupport.OpenSample(t)
	m, err := db.Maps().Load(reflect.TypeFor[stampedRow]())
	if err != nil {
		t.Fatal(err)
	}
	sqlText := db.Dialect().UpdateSQL(m)
	if strings.Contains(sqlText, "stamp = @stamp") {
		t.Errorf("update should never set the generated stamp column: %s", sqlText)
	}
	if !strings.Contains(sqlText, "label = @label") {
		t.Errorf("update should set label: %s", sqlText)
	}
}

// guidGenerated pins MAP-019: a database-generated key must be an integer type.
type guidGenerated struct {
	ID orm.GUID `orm:"column,key,generated"`
}

func (guidGenerated) Entity() orm.EntityDef {
	return orm.EntityDef{Source: orm.Table("guid_generated_rows")}
}

func TestLoad_DatabaseGeneratedKeyMustBeAnInteger(t *testing.T) {
	db := testsupport.OpenSample(t)
	_, err := db.Maps().Load(reflect.TypeFor[guidGenerated]())
	if orm.CodeOf(err) != "MAP-019" {
		t.Fatalf("expected MAP-019, got %v", err)
	}
}
