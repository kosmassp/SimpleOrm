package orm_test

// The performance pass's caches (ADR-0048): each is computed once and reused, and nothing it caches changes what the
// session sends or what the step-script runners observe.

import (
	"context"
	"testing"

	"github.com/kosmassp/SimpleOrm/go/orm"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/testsupport"
	"github.com/kosmassp/SimpleOrm/go/orm/sample"
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
