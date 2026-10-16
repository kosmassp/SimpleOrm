package testsupport

// This file may import orm, sample, and sqlite: orm's own tests live in an
// external test package (package orm_test), so there is no import cycle.

import (
	"context"
	"testing"
	"time"

	"github.com/kosmassp/SimpleOrm/go/orm"
	"github.com/kosmassp/SimpleOrm/go/orm/sample"
	"github.com/kosmassp/SimpleOrm/go/orm/sqlite"
)

// SeedTime is the fixed instant test fixtures use for created_at (mirrors
// dotnet's TestDb.SeedTime / PHP's SampleDatabase).
var SeedTime = time.Date(2026, 8, 28, 9, 30, 0, 0, time.UTC)

// OpenSample opens a temp-file database with every sample table and the
// UserTransactionTotal view created from metadata (ADR-0011, ADR-0008 add.3) —
// no hand-written DDL and no dependency on the migrations runner, which lands
// in a parallel phase of this port. Closed on test cleanup.
func OpenSample(t testing.TB) *orm.Db {
	t.Helper()
	return OpenSampleWith(t, orm.Options{})
}

// OpenSampleWith is OpenSample with session options (Level 3 M1: the tracking
// mode); a nil Dialect defaults to SQLite.
func OpenSampleWith(t testing.TB, options orm.Options) *orm.Db {
	t.Helper()
	return OpenSampleAt(t, TempDatabase(t), options)
}

// OpenSampleAt is OpenSampleWith on a given database path (the schema creation
// is idempotent), for tests that open a second session on the same file.
func OpenSampleAt(t testing.TB, path string, options orm.Options) *orm.Db {
	t.Helper()
	ctx := context.Background()

	if options.Dialect == nil {
		options.Dialect = sqlite.New()
	}
	db, err := orm.Open(ctx, path, options)
	if err != nil {
		t.Fatalf("open sample database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	for _, create := range []func(context.Context, *orm.Db) error{
		orm.CreateTable[sample.User],
		orm.CreateTable[sample.Role],
		orm.CreateTable[sample.UserRole],
		orm.CreateTable[sample.UserProfile],
		orm.CreateTable[sample.Transaction],
		orm.CreateTable[sample.TransactionDetail],
	} {
		if err := create(ctx, db); err != nil {
			t.Fatalf("create sample table: %v", err)
		}
	}
	if err := orm.CreateView[sample.UserTransactionTotal](ctx, db); err != nil {
		t.Fatalf("create sample view: %v", err)
	}

	return db
}

// InsertUser inserts a User with SeedTime as its created_at (mirrors dotnet's
// TestDb.InsertUserAsync / PHP's SampleDatabase::insertUser).
func InsertUser(t testing.TB, db *orm.Db, name, email string) *sample.User {
	t.Helper()
	user := &sample.User{Name: name, Email: email}
	user.CreatedAtUtc = SeedTime
	if err := orm.Insert(context.Background(), db, user); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	return user
}
