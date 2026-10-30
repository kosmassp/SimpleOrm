package orm_test

// The performance pass's caches (ADR-0048): each is computed once and reused, and nothing it caches changes what the
// session sends or what the step-script runners observe.

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/kosmassp/SimpleOrm/go/orm"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/metadata"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/testsupport"
	"github.com/kosmassp/SimpleOrm/go/orm/postgres"
	"github.com/kosmassp/SimpleOrm/go/orm/sample"
	"github.com/kosmassp/SimpleOrm/go/orm/sqlite"
	"github.com/kosmassp/SimpleOrm/go/orm/sqlserver"
)

// renderCountingDialect counts how often the session asks the dialect for each write rendering, while its embedded
// recorder sees every rendering the session takes (core.RenderObserver).
type renderCountingDialect struct {
	*testsupport.RecordingDialect
	asked map[string]int
}

func (d renderCountingDialect) InsertSQL(m *orm.EntityMap) string {
	d.asked["insert"]++
	return d.RecordingDialect.InsertSQL(m)
}

func (d renderCountingDialect) UpdateSQL(m *orm.EntityMap) string {
	d.asked["update"]++
	return d.RecordingDialect.UpdateSQL(m)
}

func (d renderCountingDialect) UpdateOnlySQL(m *orm.EntityMap, properties []*orm.PropertyMap) string {
	d.asked["updateOnly"]++
	return d.RecordingDialect.UpdateOnlySQL(m, properties)
}

func (d renderCountingDialect) DeleteSQL(m *orm.EntityMap, checkVersion bool) string {
	d.asked["delete"]++
	return d.RecordingDialect.DeleteSQL(m, checkVersion)
}

// decimalKeyParent/decimalKeyChild: a one-to-many whose target key is a decimal — TEXT on SQLite, so the database's
// ORDER BY is text order ('10' before '2') and the post-fetch value sort must run (spec/loading.md).
type decimalKeyParent struct {
	ID       int64              `orm:"column,key"`
	Children []*decimalKeyChild `orm:"one_to_many=ParentID"`
}

func (decimalKeyParent) Entity() orm.EntityDef {
	return orm.EntityDef{Source: orm.Table("perf_decimal_parents")}
}

type decimalKeyChild struct {
	Code     orm.Decimal `orm:"column,key"`
	ParentID int64       `orm:"column"`
}

func (decimalKeyChild) Entity() orm.EntityDef {
	return orm.EntityDef{Source: orm.Table("perf_decimal_children"), ForeignKeys: []orm.ForeignKeyDef{orm.ForeignKey[decimalKeyParent]("ParentID")}}
}

// The sort-skip rule (ADR-0048 item 4): integer keys everywhere and decimal keys where the dialect stores them as
// an exact numeric type skip the post-fetch sort; SQLite's TEXT decimal, strings, GUIDs, and temporals keep it.
func TestKeyOrderRule_SkipsTheSortOnlyWhereTheEngineOrdersByValue(t *testing.T) {
	loader := metadata.NewLoader(nil)
	load := func(entity any) *orm.EntityMap {
		m, err := loader.Load(reflect.TypeOf(entity))
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	user, child, profile := load(sample.User{}), load(decimalKeyChild{}), load(sample.UserRole{})
	for _, scenario := range []struct {
		dialect orm.Dialect
		m       *orm.EntityMap
		want    bool
	}{
		{sqlite.New(), user, true}, {postgres.New(), user, true}, {sqlserver.New(), user, true},
		{sqlite.New(), child, false}, {postgres.New(), child, true}, {sqlserver.New(), child, true},
		{sqlite.New(), profile, true}, // a composite of two integers
	} {
		if got := orm.KeyOrderIsValueOrder(scenario.dialect, scenario.m); got != scenario.want {
			t.Errorf("%s on %s: skip = %v, want %v", scenario.m.EntityName(), scenario.dialect.Name(), got, scenario.want)
		}
	}
	guidKeyed := load(guidKeyedProbe{})
	if orm.KeyOrderIsValueOrder(postgres.New(), guidKeyed) {
		t.Error("a GUID key must always sort")
	}
}

type guidKeyedProbe struct {
	ID orm.GUID `orm:"column,key"`
}

func (guidKeyedProbe) Entity() orm.EntityDef {
	return orm.EntityDef{Source: orm.Table("perf_guid_probe")}
}

// A decimal key on SQLite still loads value-wise in every mode, explicit loading included.
func TestDecimalKeyedCollection_LoadsInValueOrderOnSqlite(t *testing.T) {
	ctx := context.Background()
	db, err := orm.Open(ctx, testsupport.TempDatabase(t), orm.Options{Dialect: sqlite.New()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := orm.CreateTable[decimalKeyParent](ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := orm.CreateTable[decimalKeyChild](ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := orm.Insert(ctx, db, &decimalKeyParent{ID: 1}); err != nil {
		t.Fatal(err)
	}
	for _, code := range []string{"10", "9", "2", "19.90", "100"} {
		if err := orm.Insert(ctx, db, &decimalKeyChild{Code: orm.MustDecimal(code), ParentID: 1}); err != nil {
			t.Fatal(err)
		}
	}
	want := "2 9 10 19.90 100"
	codes := func(parent *decimalKeyParent) string {
		parts := make([]string, len(parent.Children))
		for i, c := range parent.Children {
			parts[i] = c.Code.String()
		}
		return strings.Join(parts, " ")
	}
	for _, mode := range []orm.FetchMode{orm.FetchMultiQuery, orm.FetchSubSelect, orm.FetchJoin} {
		parents, err := orm.From[decimalKeyParent](db).Include("Children").Fetch(mode).List(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if got := codes(&parents[0]); got != want {
			t.Errorf("fetch mode %v: loaded %s, want %s", mode, got, want)
		}
	}
	parent := &decimalKeyParent{ID: 1}
	if err := orm.LoadEach(ctx, db, []*decimalKeyParent{parent}, "Children"); err != nil {
		t.Fatal(err)
	}
	if got := codes(parent); got != want {
		t.Errorf("explicit loading: loaded %s, want %s", got, want)
	}
}

// The bind labels are built once and are the strings the call sites always built (ADR-0048 item 3): a conversion
// error names the same parameter on the first call and on the cached ones.
func TestBindLabels_AreTheSameStringsCachedOrNot(t *testing.T) {
	ctx := context.Background()
	db := testsupport.OpenSampleWith(t, orm.Options{})
	for range 2 {
		_, err := orm.From[sample.Transaction](db).Where(orm.Eq("ID", int64(1)), orm.Eq("Status", sample.TransactionStatus("Bogus"))).List(ctx)
		if orm.CodeOf(err) != "MAP-031" || !strings.Contains(err.Error(), "Transaction criteria @c1") {
			t.Fatalf("expected MAP-031 naming Transaction criteria @c1, got %v", err)
		}
		err = orm.Insert(ctx, db, &sample.Transaction{UserID: 1, Status: "Bogus", Amount: orm.MustDecimal("1")})
		if orm.CodeOf(err) != "MAP-031" || !strings.Contains(err.Error(), "Transaction.Status") {
			t.Fatalf("expected MAP-031 naming Transaction.Status, got %v", err)
		}
	}
}

func TestGeneratedCrudText_IsRenderedOncePerMapAndEveryRenderingIsObserved(t *testing.T) {
	ctx := context.Background()
	dialect := renderCountingDialect{testsupport.NewRecordingDialect(), map[string]int{}}
	db := testsupport.OpenSampleWith(t, orm.Options{Dialect: dialect})
	dialect.Reset()

	roles := []*sample.Role{{Name: "a"}, {Name: "b"}, {Name: "c"}}
	for _, role := range roles {
		if err := orm.Insert(ctx, db, role); err != nil {
			t.Fatal(err)
		}
	}
	for _, role := range roles {
		role.Name += "!"
		if err := orm.Update(ctx, db, role); err != nil {
			t.Fatal(err)
		}
		if err := orm.UpdateOnly(ctx, db, role, "Name"); err != nil {
			t.Fatal(err)
		}
	}
	if err := orm.UpdateOnly(ctx, db, roles[0], "IsDeleted"); err != nil { // a second column set: its own text
		t.Fatal(err)
	}
	for _, role := range roles {
		if err := orm.Delete[sample.UserRole](ctx, db, []any{role.ID, role.ID}); orm.CodeOf(err) != "CRUD-001" {
			t.Fatalf("expected CRUD-001 deleting a missing link, got %v", err)
		}
	}

	want := map[string]int{"insert": 1, "update": 1, "updateOnly": 2, "delete": 1}
	for op, count := range want {
		if dialect.asked[op] != count {
			t.Errorf("the dialect rendered %s %d time(s), want %d (asked: %v)", op, dialect.asked[op], count, dialect.asked)
		}
	}
	// Every rendering the session took is observed, in order, cached or not.
	got := dialect.Ops()
	wantOps := []string{"insert Role", "insert Role", "insert Role",
		"update Role", "update Role", "update Role", "update Role", "update Role", "update Role", "update Role",
		"delete UserRole", "delete UserRole", "delete UserRole"}
	if len(got) != len(wantOps) {
		t.Fatalf("observed %v, want %v", got, wantOps)
	}
	for i := range got {
		if got[i] != wantOps[i] {
			t.Fatalf("observed %v, want %v", got, wantOps)
		}
	}
	updates := dialect.Updates()
	if !updates[0].All || updates[1].All || updates[1].Properties[0] != "Name" || updates[6].Properties[0] != "IsDeleted" {
		t.Errorf("the observed updates lost their shape: %+v", updates)
	}
}
