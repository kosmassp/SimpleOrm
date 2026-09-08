package orm_test

import (
	"context"
	"testing"

	"github.com/kosmassp/SimpleOrm/go/orm"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/testsupport"
	"github.com/kosmassp/SimpleOrm/go/orm/sample"
	"github.com/kosmassp/SimpleOrm/go/orm/sqlite"
)

func TestBegin_CommittedTransactionPersists(t *testing.T) {
	ctx := context.Background()
	db := testsupport.OpenSample(t)

	tx, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	testsupport.InsertUser(t, db, "Ada", "ada@example.com")
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	count, err := orm.QuerySingle(ctx, db, countUsers, orm.EmptyArgs{})
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expected 1, got %d", count)
	}
}

func TestBegin_RolledBackTransactionDiscardsWork(t *testing.T) {
	ctx := context.Background()
	db := testsupport.OpenSample(t)

	tx, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	testsupport.InsertUser(t, db, "Ada", "ada@example.com")
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}

	count, err := orm.QuerySingle(ctx, db, countUsers, orm.EmptyArgs{})
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("expected 0, got %d", count)
	}
}

func TestBegin_DeferredRollbackAfterCommitIsANoOp(t *testing.T) {
	ctx := context.Background()
	db := testsupport.OpenSample(t)

	func() {
		tx, err := db.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx) // the defer-rollback idiom: a no-op once committed
		testsupport.InsertUser(t, db, "Ada", "ada@example.com")
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}()

	count, err := orm.QuerySingle(ctx, db, countUsers, orm.EmptyArgs{})
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expected 1, got %d", count)
	}
}

func TestBegin_SecondConcurrentScopeIsTX001(t *testing.T) {
	ctx := context.Background()
	db := testsupport.OpenSample(t)

	tx, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)

	_, err = db.Begin(ctx)
	if orm.CodeOf(err) != "TX-001" {
		t.Fatalf("expected TX-001, got %v", err)
	}
}

func TestClose_RollsBackAnActiveTransaction(t *testing.T) {
	ctx := context.Background()
	path := testsupport.TempDatabase(t)

	db, err := orm.Open(ctx, path, orm.Options{Dialect: sqlite.New()})
	if err != nil {
		t.Fatal(err)
	}
	if err := orm.CreateTable[sample.User](ctx, db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Begin(ctx); err != nil {
		t.Fatal(err)
	}
	testsupport.InsertUser(t, db, "Ghost", "ghost@example.com")
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	// Reopen the same file: disposal rolled the uncommitted transaction back.
	reopened, err := orm.Open(ctx, path, orm.Options{Dialect: sqlite.New()})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	count, err := orm.QuerySingle(ctx, reopened, countUsers, orm.EmptyArgs{})
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("expected 0, got %d", count)
	}
}
