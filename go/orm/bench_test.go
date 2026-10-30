package orm_test

// The performance pass's benchmarks (ADR-0048): the shapes the reference measured — 100 untracked inserts in one
// transaction, a registry query returning 1000 rows, a single key read, a criteria page of 100, and the one-to-many
// graph (1000 owners × 3 children) in every fetch mode — on SQLite (a temp file) and on PostgreSQL where one answers
// (SIMPLEORM_POSTGRES, else postgres:postgres@localhost:5432; skipped otherwise, like the live legs). Run with
//
//	go test -C <go dir> -run '^$' -bench . -benchmem ./orm/
//
// Each benchmark builds its own database outside the timed loop (b.Loop).

import (
	"context"
	"net/url"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/kosmassp/SimpleOrm/go/orm"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/testsupport"
	"github.com/kosmassp/SimpleOrm/go/orm/postgres"
	"github.com/kosmassp/SimpleOrm/go/orm/sample"
)

var benchPostgres = &testsupport.LiveServer{
	EnvironmentVariable: "SIMPLEORM_POSTGRES",
	Default:             "postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable",
	Open:                postgres.New().CreateConnection,
}

// benchEmailSequence keeps every inserted user's email unique (users.email carries a unique index).
var benchEmailSequence atomic.Int64

func benchUser() *sample.User {
	return &sample.User{Name: "user", Email: "user" + strconv.FormatInt(benchEmailSequence.Add(1), 10) + "@example.com"}
}

var benchAllUsers = orm.Inline[orm.EmptyArgs, sample.User](
	"select id, name, email, display_name, created_at, updated_at from users order by id")

// benchLegs runs body once per database: SQLite always, PostgreSQL where it answers.
func benchLegs(b *testing.B, body func(b *testing.B, db *orm.Db)) {
	b.Run("sqlite", func(b *testing.B) {
		body(b, testsupport.OpenSampleWith(b, orm.Options{}))
	})
	b.Run("postgres", func(b *testing.B) {
		benchPostgres.SkipUnlessAvailable(b, "no PostgreSQL server answered (set SIMPLEORM_POSTGRES)")
		body(b, testsupport.OpenSampleAt(b, benchPostgresDatabase(b), orm.Options{Dialect: postgres.New()}))
	})
}

// benchPostgresDatabase creates a fresh simpleorm_test_<hex> database, dropped when the benchmark ends.
func benchPostgresDatabase(b *testing.B) string {
	b.Helper()
	admin := benchPostgres.AdminConnectionString()
	name := testsupport.DatabaseName(b)
	adminExec := func(statement string) error {
		pool, err := postgres.New().CreateConnection(admin)
		if err != nil {
			return err
		}
		defer pool.Close()
		_, err = pool.ExecContext(context.Background(), statement)
		return err
	}
	if err := adminExec(`create database "` + name + `"`); err != nil {
		b.Fatalf("create database %s: %v", name, err)
	}
	b.Cleanup(func() { _ = adminExec(`drop database if exists "` + name + `" with (force)`) })
	u, err := url.Parse(admin)
	if err != nil {
		b.Fatalf("parse %s: %v", admin, err)
	}
	u.Path = "/" + name
	return u.String()
}

func benchSeedUsers(b *testing.B, db *orm.Db, count int) []int64 {
	b.Helper()
	ctx := context.Background()
	tx, err := db.Begin(ctx)
	if err != nil {
		b.Fatal(err)
	}
	ids := make([]int64, count)
	for i := range count {
		user := benchUser()
		if err := orm.Insert(ctx, db, user); err != nil {
			b.Fatal(err)
		}
		ids[i] = user.ID
	}
	if err := tx.Commit(ctx); err != nil {
		b.Fatal(err)
	}
	return ids
}

func benchSeedGraph(b *testing.B, db *orm.Db, owners, children int) {
	b.Helper()
	ctx := context.Background()
	tx, err := db.Begin(ctx)
	if err != nil {
		b.Fatal(err)
	}
	for range owners {
		transaction := &sample.Transaction{UserID: 1, Status: sample.Pending, Amount: orm.MustDecimal("12.50")}
		if err := orm.Insert(ctx, db, transaction); err != nil {
			b.Fatal(err)
		}
		for range children {
			detail := &sample.TransactionDetail{TransactionID: transaction.ID, Description: "line", Quantity: 2, UnitPrice: orm.MustDecimal("6.25")}
			if err := orm.Insert(ctx, db, detail); err != nil {
				b.Fatal(err)
			}
		}
	}
	if err := tx.Commit(ctx); err != nil {
		b.Fatal(err)
	}
}

func BenchmarkInsert100Transaction(b *testing.B) {
	benchLegs(b, func(b *testing.B, db *orm.Db) {
		ctx := context.Background()
		for b.Loop() {
			tx, err := db.Begin(ctx)
			if err != nil {
				b.Fatal(err)
			}
			for range 100 {
				if err := orm.Insert(ctx, db, benchUser()); err != nil {
					b.Fatal(err)
				}
			}
			if err := tx.Rollback(ctx); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func BenchmarkRegistryQuery1000(b *testing.B) {
	benchLegs(b, func(b *testing.B, db *orm.Db) {
		ctx := context.Background()
		benchSeedUsers(b, db, 1000)
		for b.Loop() {
			users, err := orm.Query(ctx, db, benchAllUsers, orm.EmptyArgs{})
			if err != nil || len(users) != 1000 {
				b.Fatalf("%d users, %v", len(users), err)
			}
		}
	})
}

func BenchmarkQuerySingle(b *testing.B) {
	benchLegs(b, func(b *testing.B, db *orm.Db) {
		ctx := context.Background()
		benchSeedUsers(b, db, 10)
		user, err := orm.Get[sample.User](ctx, db, int64(5))
		if err != nil {
			b.Fatal(err)
		}
		for b.Loop() {
			if _, err := orm.QuerySingle(ctx, db, sample.UsersByEmail, sample.UsersByEmailArgs{Email: user.Email}); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func BenchmarkCommand100Transaction(b *testing.B) {
	benchLegs(b, func(b *testing.B, db *orm.Db) {
		ctx := context.Background()
		benchSeedGraph(b, db, 100, 0)
		for b.Loop() {
			tx, err := db.Begin(ctx)
			if err != nil {
				b.Fatal(err)
			}
			for id := int64(1); id <= 100; id++ {
				args := sample.MarkTransactionStatusArgs{ID: id, Status: sample.Completed, UpdatedAt: testsupport.ClockNow}
				if _, err := orm.Execute(ctx, db, sample.MarkTransactionStatus, args); err != nil {
					b.Fatal(err)
				}
			}
			if err := tx.Rollback(ctx); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func BenchmarkGet(b *testing.B) {
	benchLegs(b, func(b *testing.B, db *orm.Db) {
		ctx := context.Background()
		ids := benchSeedUsers(b, db, 10)
		for b.Loop() {
			if _, err := orm.Get[sample.User](ctx, db, ids[5]); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func BenchmarkCriteriaPage100(b *testing.B) {
	benchLegs(b, func(b *testing.B, db *orm.Db) {
		ctx := context.Background()
		benchSeedUsers(b, db, 1000)
		for b.Loop() {
			users, err := orm.From[sample.User](db).Where(orm.Ge("ID", int64(1))).OrderBy("ID").Limit(100).List(ctx)
			if err != nil || len(users) != 100 {
				b.Fatalf("%d users, %v", len(users), err)
			}
		}
	})
}

func benchGraph(b *testing.B, mode orm.FetchMode) {
	benchLegs(b, func(b *testing.B, db *orm.Db) {
		ctx := context.Background()
		benchSeedGraph(b, db, 1000, 3)
		for b.Loop() {
			transactions, err := orm.From[sample.Transaction](db).Include("Details").Fetch(mode).List(ctx)
			if err != nil || len(transactions) != 1000 || len(transactions[999].Details) != 3 {
				b.Fatalf("%d transactions, %v", len(transactions), err)
			}
		}
	})
}

func BenchmarkGraphMultiQuery(b *testing.B) { benchGraph(b, orm.FetchMultiQuery) }

func BenchmarkGraphSubSelect(b *testing.B) { benchGraph(b, orm.FetchSubSelect) }

func BenchmarkGraphJoin(b *testing.B) { benchGraph(b, orm.FetchJoin) }
