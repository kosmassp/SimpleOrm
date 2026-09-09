package orm_test

import (
	"context"
	"strings"
	"testing"

	"github.com/kosmassp/SimpleOrm/go/orm"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/testsupport"
	"github.com/kosmassp/SimpleOrm/go/orm/sample"
	"github.com/kosmassp/SimpleOrm/go/orm/sqlite"
)

// spec/loading.md "Key-tiebroken ordering, precisely": a paged SubSelect root
// gains every key property not already ordered on, ascending, and that
// ordering drives both the root query and the subquery. An unpaged root and
// the other modes are untouched.
func TestSubSelect_PagedRootIsKeyTiebrokenInRootAndSubquery(t *testing.T) {
	ctx := context.Background()
	var last string
	db, err := orm.Open(ctx, testsupport.TempDatabase(t), orm.Options{Dialect: capturingDialect{Dialect: sqlite.New(), last: &last}})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, create := range []func(context.Context, *orm.Db) error{
		orm.CreateTable[sample.User], orm.CreateTable[sample.Transaction],
	} {
		if err := create(ctx, db); err != nil {
			t.Fatal(err)
		}
	}
	testsupport.InsertUser(t, db, "Ada", "ada@example.com")

	_, err = orm.From[sample.User](db).
		Include("Transactions").Fetch(orm.FetchSubSelect).
		OrderBy("Name").Limit(10).
		List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// The last render is the navigation query, which embeds the root subquery.
	if !strings.Contains(last, "order by name, id limit") {
		t.Errorf("expected the subquery to carry the key tiebreak, got:\n%s", last)
	}

	_, err = orm.From[sample.User](db).
		Include("Transactions").Fetch(orm.FetchSubSelect).
		OrderBy("Name").
		List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(last, "order by name, id") {
		t.Errorf("an unpaged root must not gain a tiebreak, got:\n%s", last)
	}
}
