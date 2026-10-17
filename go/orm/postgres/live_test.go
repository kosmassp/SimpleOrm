package postgres_test

// The live leg of the PostgreSQL dialect (ADR-0025, spec/dialects.md "Testing
// contract"): real server, never a mock, skipped as a group where none answers.
// Each test creates its own `simpleorm_test_<hex>` database on the admin
// connection (SIMPLEORM_POSTGRES, else postgres:postgres@localhost:5432) and
// drops it afterwards with `with (force)`.

import (
	"context"
	"errors"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/kosmassp/SimpleOrm/go/orm"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/metadata"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/testsupport"
	"github.com/kosmassp/SimpleOrm/go/orm/migrations"
	"github.com/kosmassp/SimpleOrm/go/orm/postgres"
	"github.com/kosmassp/SimpleOrm/go/orm/sample"
)

// server is the probe shared by every live test: the admin connection string is tried once per process.
var server = &testsupport.LiveServer{
	EnvironmentVariable: "SIMPLEORM_POSTGRES",
	Default:             "postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable",
	Open:                postgres.New().CreateConnection,
}

const noServer = "no PostgreSQL server answered (set SIMPLEORM_POSTGRES to an admin connection string)"

// newDatabase creates a fresh test database on the admin connection, drops it on cleanup, and
// returns the connection string that reaches it.
func newDatabase(t *testing.T) string {
	t.Helper()
	server.SkipUnlessAvailable(t, noServer)
	admin := server.AdminConnectionString()
	name := testsupport.DatabaseName(t)

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
		t.Fatalf("create database %s: %v", name, err)
	}
	t.Cleanup(func() {
		if err := adminExec(`drop database if exists "` + name + `" with (force)`); err != nil {
			t.Errorf("drop database %s: %v", name, err)
		}
	})

	if u, err := url.Parse(admin); err == nil && (u.Scheme == "postgres" || u.Scheme == "postgresql") {
		u.Path = "/" + name
		return u.String()
	}
	return admin + " dbname=" + name // a key/value DSN: the later setting wins
}

// openSample opens a session on a fresh database holding every sample table and the sample view.
func openSample(t *testing.T, options orm.Options) (*orm.Db, string) {
	t.Helper()
	connection := newDatabase(t)
	options.Dialect = postgres.New()
	return testsupport.OpenSampleAt(t, connection, options), connection
}

// openBare opens a session on a fresh, empty database.
func openBare(t *testing.T, options orm.Options) (*orm.Db, string) {
	t.Helper()
	connection := newDatabase(t)
	options.Dialect = postgres.New()
	options.Clock = testsupport.FixedClock
	db, err := orm.Open(context.Background(), connection, options)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, connection
}

// execRaw runs one statement on its own connection, outside any session (DDL a model cannot say).
func execRaw(t *testing.T, connection, statement string, args ...any) {
	t.Helper()
	pool, err := postgres.New().CreateConnection(connection)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := pool.ExecContext(context.Background(), statement, args...); err != nil {
		t.Fatalf("exec %q: %v", statement, err)
	}
}

func createTables(t *testing.T, db *orm.Db, creators ...func(context.Context, *orm.Db) error) {
	t.Helper()
	for _, create := range creators {
		if err := create(context.Background(), db); err != nil {
			t.Fatalf("create table: %v", err)
		}
	}
}

func newTransaction(userID int64) *sample.Transaction {
	tx := &sample.Transaction{UserID: userID, Status: sample.Pending, Amount: orm.MustDecimal("10")}
	tx.CreatedAtUtc = testsupport.SeedTime
	return tx
}

// --- generated keys, temporals, every fixed-table type ---------------------------

func TestLive_InsertWritesTheReturningKeyBackAndTimestamptzReadsBackAsUTC(t *testing.T) {
	ctx := context.Background()
	db, _ := openSample(t, orm.Options{})

	ada := testsupport.InsertUser(t, db, "Ada", "ada@example.com")
	grace := testsupport.InsertUser(t, db, "Grace", "grace@example.com")
	if ada.ID <= 0 || grace.ID != ada.ID+1 {
		t.Fatalf("expected generated keys written back, got %d and %d", ada.ID, grace.ID)
	}

	loaded, err := orm.Get[sample.User](ctx, db, ada.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.CreatedAtUtc.Equal(testsupport.SeedTime) || loaded.CreatedAtUtc.Location() != time.UTC {
		t.Errorf("expected the UTC instant %v, got %v in %v", testsupport.SeedTime, loaded.CreatedAtUtc, loaded.CreatedAtUtc.Location())
	}
	if loaded.UpdatedAtUtc != nil {
		t.Errorf("expected a NULL updated_at to read back nil, got %v", loaded.UpdatedAtUtc)
	}

	// An instant held in another zone is the same instant in the database.
	plusFive := time.FixedZone("+05:00", 5*60*60)
	zoned := &sample.User{Name: "Zoned", Email: "zoned@example.com"}
	zoned.CreatedAtUtc = testsupport.SeedTime.In(plusFive)
	if err := orm.Insert(ctx, db, zoned); err != nil {
		t.Fatal(err)
	}
	reloaded, err := orm.Get[sample.User](ctx, db, zoned.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reloaded.CreatedAtUtc.Equal(testsupport.SeedTime) || reloaded.CreatedAtUtc.Location() != time.UTC {
		t.Errorf("expected %v in UTC, got %v in %v", testsupport.SeedTime, reloaded.CreatedAtUtc, reloaded.CreatedAtUtc.Location())
	}
}

func TestLive_ClientGuidKeyAndExplicitIdentityKeyRoundTrip(t *testing.T) {
	ctx := context.Background()
	db, connection := openBare(t, orm.Options{})
	createTables(t, db, orm.CreateTable[liveWidget])

	widget := &liveWidget{Note: "first"}
	if err := orm.Insert(ctx, db, widget); err != nil {
		t.Fatal(err)
	}
	if widget.ID.IsZero() {
		t.Fatal("expected a client-generated GUID key")
	}
	loaded, err := orm.Get[liveWidget](ctx, db, widget.ID)
	if err != nil || loaded.Note != "first" || loaded.ID != widget.ID {
		t.Fatalf("got %+v, %v", loaded, err)
	}

	// "generated by default" lets a fixture insert an explicit identity value.
	createTables(t, db, orm.CreateTable[sample.User])
	execRaw(t, connection, `insert into "users" ("id", "name", "email", "created_at") values (500, 'Explicit', 'explicit@example.com', now())`)
	if got, err := orm.Get[sample.User](ctx, db, int64(500)); err != nil || got.Name != "Explicit" {
		t.Fatalf("got %+v, %v", got, err)
	}
}

type liveWidget struct {
	ID   orm.GUID `orm:"column,key"`
	Note string   `orm:"column"`
}

func (liveWidget) Entity() orm.EntityDef { return orm.EntityDef{Source: orm.Table("live_widgets")} }

// typeZoo carries every fixed-table type (§7.9), created from metadata and round-tripped.
type typeZoo struct {
	ID        int64                     `orm:"column,key,generated"`
	I32       int32                     `orm:"column"`
	I16       int16                     `orm:"column"`
	Price     orm.Decimal               `orm:"column"`
	D         float64                   `orm:"column"`
	F         float32                   `orm:"column"`
	Flag      bool                      `orm:"column"`
	Text      string                    `orm:"column"`
	MaybeText *string                   `orm:"column"`
	Token     orm.GUID                  `orm:"column"`
	Blob      []byte                    `orm:"column"`
	At        time.Time                 `orm:"column"`
	AtOffset  time.Time                 `orm:"column,type=datetimeoffset"`
	Day       time.Time                 `orm:"column,type=date"`
	Clock     time.Time                 `orm:"column,type=time"`
	AsText    sample.TransactionStatus  `orm:"column"`
	AsInt     sample.TransactionStatus  `orm:"column,enum_int"`
	MaybeEnum *sample.TransactionStatus `orm:"column"`
}

func TestLive_EveryFixedTableTypeRoundTripsThroughNativeColumns(t *testing.T) {
	ctx := context.Background()
	db, _ := openBare(t, orm.Options{})
	createTables(t, db, orm.CreateTable[typeZoo])

	at := time.Date(2026, 8, 28, 10, 0, 0, 0, time.UTC)
	atOffset := time.Date(2026, 8, 28, 12, 0, 0, 0, time.FixedZone("+02:00", 2*60*60))
	day := time.Date(2026, 8, 28, 0, 0, 0, 0, time.UTC)
	clock := time.Date(1, 1, 1, 13, 30, 15, 0, time.UTC)
	token, err := orm.ParseGUID("6f9619ff-8b86-d011-b42d-00cf4fc964ff")
	if err != nil {
		t.Fatal(err)
	}
	zoo := &typeZoo{
		I32: 42, I16: 7, Price: orm.MustDecimal("1234.56"), D: 2.25, F: 1.5, Flag: true, Text: "hello",
		Token: token, Blob: []byte{1, 2, 3}, At: at, AtOffset: atOffset, Day: day, Clock: clock,
		AsText: sample.Completed, AsInt: sample.Cancelled,
	}
	if err := orm.Insert(ctx, db, zoo); err != nil {
		t.Fatal(err)
	}

	rows, err := orm.QueryAll[typeZoo](ctx, db)
	if err != nil || len(rows) != 1 {
		t.Fatalf("got %d rows, %v", len(rows), err)
	}
	loaded := rows[0]
	switch {
	case loaded.I32 != 42 || loaded.I16 != 7:
		t.Errorf("integers: %+v", loaded)
	case !loaded.Price.Equal(zoo.Price):
		t.Errorf("numeric: expected %s, got %s", zoo.Price, loaded.Price)
	case loaded.D != 2.25 || loaded.F != 1.5:
		t.Errorf("floats: %+v", loaded)
	case !loaded.Flag || loaded.Text != "hello" || loaded.MaybeText != nil:
		t.Errorf("bool/text/null: %+v", loaded)
	case loaded.Token != token:
		t.Errorf("uuid: expected %s, got %s", token, loaded.Token)
	case string(loaded.Blob) != "\x01\x02\x03":
		t.Errorf("bytea: %v", loaded.Blob)
	case !loaded.At.Equal(at) || loaded.At.Location() != time.UTC:
		t.Errorf("timestamptz: expected the UTC instant %v, got %v in %v", at, loaded.At, loaded.At.Location())
	case !loaded.AtOffset.Equal(atOffset):
		t.Errorf("datetimeoffset: expected the instant %v, got %v", atOffset, loaded.AtOffset)
	case !loaded.Day.Equal(day):
		t.Errorf("date: expected %v, got %v", day, loaded.Day)
	case loaded.Clock.Hour() != 13 || loaded.Clock.Minute() != 30 || loaded.Clock.Second() != 15:
		t.Errorf("time: expected 13:30:15, got %v", loaded.Clock)
	case loaded.AsText != sample.Completed || loaded.AsInt != sample.Cancelled || loaded.MaybeEnum != nil:
		t.Errorf("enums: %+v", loaded)
	}

	// The storage representation is native: integer enum, a calendar date, and a time of day.
	asInt, err := orm.QuerySingle(ctx, db, orm.Inline[orm.EmptyArgs, int64]("select as_int::bigint from type_zoo"), orm.EmptyArgs{})
	if err != nil || asInt != 2 {
		t.Errorf("expected the integer position 2, got %d, %v", asInt, err)
	}
	dayText, err := orm.QuerySingle(ctx, db, orm.Inline[orm.EmptyArgs, string]("select day::text from type_zoo"), orm.EmptyArgs{})
	if err != nil || dayText != "2026-08-28" {
		t.Errorf("expected the calendar date, got %q, %v", dayText, err)
	}

	// A date held in another zone keeps its own calendar date.
	late := time.Date(2026, 8, 28, 23, 30, 0, 0, time.FixedZone("-05:00", -5*60*60))
	zoo2 := *zoo
	zoo2.ID = 0
	zoo2.Day = late
	if err := orm.Insert(ctx, db, &zoo2); err != nil {
		t.Fatal(err)
	}
	reread, err := orm.Get[typeZoo](ctx, db, zoo2.ID)
	if err != nil || reread.Day.Format("2006-01-02") != "2026-08-28" {
		t.Errorf("expected 2026-08-28, got %v, %v", reread.Day, err)
	}
}

// --- array parameters, temporal criteria, paging ---------------------------------

type idsArgs struct{ Ids []int64 }

type namesArgs struct{ Names []string }

var usersByIds = orm.Inline[idsArgs, sample.User](
	"select id, name, email, display_name, created_at, updated_at from users where id = any(@Ids) order by id")

var usersByNames = orm.Inline[namesArgs, sample.User](
	"select id, name, email, display_name, created_at, updated_at from users where name = any(@Names) order by id")

func TestLive_ArrayParametersBindOneTypedArrayFilledAndEmpty(t *testing.T) {
	ctx := context.Background()
	db, _ := openSample(t, orm.Options{})
	ada := testsupport.InsertUser(t, db, "Ada", "ada@example.com")
	testsupport.InsertUser(t, db, "Grace", "grace@example.com")
	linus := testsupport.InsertUser(t, db, "Linus", "linus@example.com")

	found, err := orm.Query(ctx, db, usersByIds, idsArgs{Ids: []int64{ada.ID, linus.ID, 99_999}})
	if err != nil || len(found) != 2 || found[0].Name != "Ada" || found[1].Name != "Linus" {
		t.Fatalf("filled: %+v, %v", found, err)
	}

	for label, empty := range map[string][]int64{"empty": {}, "nil": nil} {
		none, err := orm.Query(ctx, db, usersByIds, idsArgs{Ids: empty})
		if err != nil || len(none) != 0 {
			t.Errorf("%s collection should match nothing and never error, got %+v, %v", label, none, err)
		}
	}

	named, err := orm.Query(ctx, db, usersByNames, namesArgs{Names: []string{"Grace", "Nobody"}})
	if err != nil || len(named) != 1 || named[0].Name != "Grace" {
		t.Fatalf("strings: %+v, %v", named, err)
	}
	if none, err := orm.Query(ctx, db, usersByNames, namesArgs{}); err != nil || len(none) != 0 {
		t.Errorf("an empty string array should match nothing, got %+v, %v", none, err)
	}
}

func TestLive_TemporalCriteriaCompareAgainstTimestamptz(t *testing.T) {
	ctx := context.Background()
	db, _ := openSample(t, orm.Options{})
	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	for i, name := range []string{"Early", "Middle", "Late"} {
		user := &sample.User{Name: name, Email: name + "@example.com"}
		user.CreatedAtUtc = base.Add(time.Duration(i) * time.Hour)
		if err := orm.Insert(ctx, db, user); err != nil {
			t.Fatal(err)
		}
	}

	// The cutoff is the middle row's instant, spelled in another zone: native binding keeps the instant.
	cutoff := base.Add(time.Hour).In(time.FixedZone("-08:00", -8*60*60))
	later, err := orm.From[sample.User](db).Where(orm.Ge("CreatedAtUtc", cutoff)).OrderBy("CreatedAtUtc").List(ctx)
	if err != nil || len(later) != 2 || later[0].Name != "Middle" || later[1].Name != "Late" {
		t.Fatalf("Ge: %+v, %v", later, err)
	}
	earlier, err := orm.From[sample.User](db).Where(orm.Lt("CreatedAtUtc", cutoff)).List(ctx)
	if err != nil || len(earlier) != 1 || earlier[0].Name != "Early" {
		t.Fatalf("Lt: %+v, %v", earlier, err)
	}
	none, err := orm.From[sample.User](db).Where(orm.In("CreatedAtUtc", base.Add(90*time.Minute))).List(ctx)
	if err != nil || len(none) != 0 {
		t.Fatalf("In: %+v, %v", none, err)
	}
}

func TestLive_CriteriaPagingWithAndWithoutOrderBy(t *testing.T) {
	ctx := context.Background()
	db, _ := openSample(t, orm.Options{})
	for _, name := range []string{"Echo", "Alpha", "Delta", "Bravo", "Charlie"} {
		testsupport.InsertUser(t, db, name, name+"@example.com")
	}

	// PostgreSQL pages without an ordering (the plain limit/offset clause).
	unordered, err := orm.From[sample.User](db).Limit(2).Offset(1).List(ctx)
	if err != nil || len(unordered) != 2 {
		t.Fatalf("unordered: %+v, %v", unordered, err)
	}

	names := func(users []sample.User) string {
		var out []string
		for _, u := range users {
			out = append(out, u.Name)
		}
		return strings.Join(out, ",")
	}
	ordered, err := orm.From[sample.User](db).OrderBy("Name").Limit(2).Offset(1).List(ctx)
	if err != nil || names(ordered) != "Bravo,Charlie" {
		t.Fatalf("ordered: %s, %v", names(ordered), err)
	}
	offsetOnly, err := orm.From[sample.User](db).OrderBy("Name", orm.Desc).Offset(3).List(ctx)
	if err != nil || names(offsetOnly) != "Bravo,Alpha" {
		t.Fatalf("offset only: %s, %v", names(offsetOnly), err)
	}
	limitOnly, err := orm.From[sample.User](db).OrderBy("Name").Limit(1).List(ctx)
	if err != nil || names(limitOnly) != "Alpha" {
		t.Fatalf("limit only: %s, %v", names(limitOnly), err)
	}
}

// --- reserved words ----------------------------------------------------------------

// reservedOrder is table "order" with columns "desc" and "group": every identifier is a reserved word.
type reservedOrder struct {
	ID    int64  `orm:"column,key,generated"`
	Desc  string `orm:"column=desc"`
	Group string `orm:"column=group"`
}

func (reservedOrder) Entity() orm.EntityDef {
	return orm.EntityDef{Source: orm.Table("order"), Indexes: []orm.IndexDef{orm.Index("Desc").Unique()}}
}

func TestLive_ReservedWordIdentifiersWorkEndToEnd(t *testing.T) {
	ctx := context.Background()
	db, _ := openBare(t, orm.Options{})
	createTables(t, db, orm.CreateTable[reservedOrder])

	first := &reservedOrder{Desc: "first", Group: "a"}
	second := &reservedOrder{Desc: "second", Group: "b"}
	for _, row := range []*reservedOrder{first, second} {
		if err := orm.Insert(ctx, db, row); err != nil {
			t.Fatal(err)
		}
	}

	second.Group = "c"
	if err := orm.Update(ctx, db, second); err != nil {
		t.Fatal(err)
	}
	if err := orm.UpdateOnly(ctx, db, first, "Group"); err != nil {
		t.Fatal(err)
	}
	loaded, err := orm.Get[reservedOrder](ctx, db, second.ID)
	if err != nil || loaded.Group != "c" {
		t.Fatalf("got %+v, %v", loaded, err)
	}

	found, err := orm.From[reservedOrder](db).Where(orm.Ne("Desc", "first")).OrderBy("Group", orm.Desc).Limit(5).List(ctx)
	if err != nil || len(found) != 1 || found[0].Desc != "second" {
		t.Fatalf("criteria: %+v, %v", found, err)
	}
	if err := orm.Delete[reservedOrder](ctx, db, first.ID); err != nil {
		t.Fatal(err)
	}
	all, err := orm.QueryAll[reservedOrder](ctx, db)
	if err != nil || len(all) != 1 {
		t.Fatalf("select all: %+v, %v", all, err)
	}
}

// --- concurrency and transactions -----------------------------------------------------

func TestLive_VersionColumnDetectsStaleUpdatesAndDeletes(t *testing.T) {
	ctx := context.Background()
	db, _ := openSample(t, orm.Options{})
	ada := testsupport.InsertUser(t, db, "Ada", "ada@example.com")
	if err := orm.Insert(ctx, db, newTransaction(ada.ID)); err != nil {
		t.Fatal(err)
	}

	fresh, err := orm.From[sample.Transaction](db).Where(orm.Eq("UserID", ada.ID)).Single(ctx)
	if err != nil {
		t.Fatal(err)
	}
	stale, err := orm.Get[sample.Transaction](ctx, db, fresh.ID)
	if err != nil {
		t.Fatal(err)
	}

	fresh.Status = sample.Completed
	if err := orm.Update(ctx, db, &fresh); err != nil {
		t.Fatal(err)
	}
	if fresh.Version != 1 {
		t.Fatalf("expected the version bumped to 1, got %d", fresh.Version)
	}

	stale.Amount = orm.MustDecimal("99")
	if err := orm.Update(ctx, db, &stale); orm.CodeOf(err) != "CRUD-010" {
		t.Fatalf("expected CRUD-010 on the stale update, got %v", err)
	}
	if err := orm.UpdateOnly(ctx, db, &stale, "Note"); orm.CodeOf(err) != "CRUD-010" {
		t.Fatalf("expected CRUD-010 on the stale column-list update, got %v", err)
	}
	if err := orm.DeleteEntity(ctx, db, &stale); orm.CodeOf(err) != "CRUD-010" {
		t.Fatalf("expected CRUD-010 on the stale delete, got %v", err)
	}
	current, err := orm.Get[sample.Transaction](ctx, db, fresh.ID)
	if err != nil || current.Version != 1 || !current.Amount.Equal(orm.MustDecimal("10")) {
		t.Fatalf("the stale writes should have changed nothing, got %+v, %v", current, err)
	}
	if err := orm.DeleteEntity(ctx, db, &fresh); err != nil {
		t.Fatalf("the current version deletes: %v", err)
	}
}

func TestLive_TransactionScopesCommitAndRollBack(t *testing.T) {
	ctx := context.Background()
	db, _ := openSample(t, orm.Options{})
	count := func() int {
		users, err := orm.QueryAll[sample.User](ctx, db)
		if err != nil {
			t.Fatal(err)
		}
		return len(users)
	}

	rolledBack, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	testsupport.InsertUser(t, db, "Discarded", "discarded@example.com")
	if _, err := db.Begin(ctx); orm.CodeOf(err) != "TX-001" {
		t.Errorf("a second scope should be TX-001, got %v", err)
	}
	if err := rolledBack.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if count() != 0 {
		t.Fatalf("expected the rolled-back insert to be gone, got %d", count())
	}

	committed, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	testsupport.InsertUser(t, db, "Kept", "kept@example.com")
	if err := committed.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if count() != 1 {
		t.Fatalf("expected the committed insert, got %d", count())
	}

	// A deferred rollback after commit is a no-op.
	again, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Rollback(ctx)
	if err := again.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}

// --- loading ------------------------------------------------------------------------

func seedGraph(t *testing.T, db *orm.Db) (ada, grace *sample.User) {
	t.Helper()
	ctx := context.Background()
	ada = testsupport.InsertUser(t, db, "Ada", "ada@example.com")
	grace = testsupport.InsertUser(t, db, "Grace", "grace@example.com")
	testsupport.InsertUser(t, db, "Linus", "linus@example.com")
	for _, owner := range []int64{ada.ID, ada.ID, grace.ID} {
		if err := orm.Insert(ctx, db, newTransaction(owner)); err != nil {
			t.Fatal(err)
		}
	}
	admin, editor := &sample.Role{Name: "admin"}, &sample.Role{Name: "editor"}
	for _, role := range []*sample.Role{admin, editor} {
		if err := orm.Insert(ctx, db, role); err != nil {
			t.Fatal(err)
		}
	}
	for _, link := range []*sample.UserRole{
		{UserID: ada.ID, RoleID: admin.ID}, {UserID: ada.ID, RoleID: editor.ID}, {UserID: grace.ID, RoleID: editor.ID},
	} {
		if err := orm.Insert(ctx, db, link); err != nil {
			t.Fatal(err)
		}
	}
	return ada, grace
}

func TestLive_ExplicitBatchAndLinkLoading(t *testing.T) {
	ctx := context.Background()
	db, _ := openSample(t, orm.Options{})
	ada, grace := seedGraph(t, db)

	adaCopy, err := orm.Get[sample.User](ctx, db, ada.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := orm.Load(ctx, db, &adaCopy, "Transactions"); err != nil || len(adaCopy.Transactions) != 2 {
		t.Fatalf("explicit: %d transactions, %v", len(adaCopy.Transactions), err)
	}
	if err := orm.Load(ctx, db, &adaCopy, "Roles"); err != nil || len(adaCopy.Roles) != 2 {
		t.Fatalf("many-to-many: %d roles, %v", len(adaCopy.Roles), err)
	}

	owners, err := orm.From[sample.User](db).Where(orm.In("Id", ada.ID, grace.ID)).OrderBy("Name").List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	pointers := []*sample.User{&owners[0], &owners[1]}
	if err := orm.LoadEach(ctx, db, pointers, "Transactions"); err != nil {
		t.Fatal(err)
	}
	if len(owners[0].Transactions) != 2 || len(owners[1].Transactions) != 1 {
		t.Errorf("batch: Ada has %d, Grace has %d transactions", len(owners[0].Transactions), len(owners[1].Transactions))
	}

	transactions, err := orm.From[sample.Transaction](db).OrderBy("Id").List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	refs := []*sample.Transaction{&transactions[0], &transactions[1], &transactions[2]}
	if err := orm.LoadEach(ctx, db, refs, "User"); err != nil {
		t.Fatal(err)
	}
	if transactions[0].User == nil || transactions[0].User != transactions[1].User || transactions[2].User.Name != "Grace" {
		t.Errorf("many-to-one owners should share their target instance: %+v", transactions)
	}
}

func TestLive_EagerLoadingInEveryFetchModeLoadsTheSameGraph(t *testing.T) {
	ctx := context.Background()
	db, _ := openSample(t, orm.Options{})
	seedGraph(t, db)

	summary := func(users []sample.User) string {
		var parts []string
		for _, u := range users {
			parts = append(parts, u.Name+":"+string(rune('0'+len(u.Transactions))))
		}
		return strings.Join(parts, ",")
	}
	for _, mode := range []orm.FetchMode{orm.FetchMultiQuery, orm.FetchSubSelect, orm.FetchJoin} {
		users, err := orm.From[sample.User](db).Include("Transactions").Fetch(mode).OrderBy("Name").List(ctx)
		if err != nil {
			t.Fatalf("mode %v: %v", mode, err)
		}
		if got := summary(users); got != "Ada:2,Grace:1,Linus:0" {
			t.Errorf("mode %v: %s", mode, got)
		}
	}
}

func TestLive_SubSelectIncludeOverAPagedRoot(t *testing.T) {
	ctx := context.Background()
	db, _ := openSample(t, orm.Options{})
	seedGraph(t, db)

	users, err := orm.From[sample.User](db).
		Include("Transactions").Fetch(orm.FetchSubSelect).
		OrderBy("Name").Limit(2).Offset(1).
		List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 2 || users[0].Name != "Grace" || len(users[0].Transactions) != 1 ||
		users[1].Name != "Linus" || users[1].Transactions == nil || len(users[1].Transactions) != 0 {
		t.Errorf("unexpected page: %+v", users)
	}
}

type compParent struct {
	TenantID int64        `orm:"column,key"`
	Code     int64        `orm:"column,key"`
	Name     string       `orm:"column"`
	Children []*compChild `orm:"one_to_many=ParentTenantID+ParentCode"`
}

func (compParent) Entity() orm.EntityDef { return orm.EntityDef{Source: orm.Table("comp_parents")} }

type compChild struct {
	ID             int64  `orm:"column,key,generated"`
	ParentTenantID int64  `orm:"column"`
	ParentCode     int64  `orm:"column"`
	Label          string `orm:"column"`
}

func (compChild) Entity() orm.EntityDef { return orm.EntityDef{Source: orm.Table("comp_children")} }

func TestLive_CompositeKeySubSelectUsesNativeRowValueIn(t *testing.T) {
	ctx := context.Background()
	db, _ := openBare(t, orm.Options{})
	createTables(t, db, orm.CreateTable[compParent], orm.CreateTable[compChild])

	for _, parent := range []*compParent{{TenantID: 1, Code: 10, Name: "a"}, {TenantID: 1, Code: 11, Name: "b"}, {TenantID: 2, Code: 10, Name: "c"}} {
		if err := orm.Insert(ctx, db, parent); err != nil {
			t.Fatal(err)
		}
	}
	for _, child := range []*compChild{
		{ParentTenantID: 1, ParentCode: 10, Label: "a1"}, {ParentTenantID: 1, ParentCode: 10, Label: "a2"},
		{ParentTenantID: 2, ParentCode: 10, Label: "c1"},
	} {
		if err := orm.Insert(ctx, db, child); err != nil {
			t.Fatal(err)
		}
	}

	for _, mode := range []orm.FetchMode{orm.FetchSubSelect, orm.FetchMultiQuery, orm.FetchJoin} {
		query := orm.From[compParent](db).Include("Children").Fetch(mode).OrderBy("TenantID").OrderBy("Code")
		if mode != orm.FetchJoin {
			query = query.Limit(2) // a join refuses paging (REL-005)
		}
		parents, err := query.List(ctx)
		if err != nil {
			t.Fatalf("mode %v: %v", mode, err)
		}
		if len(parents) < 2 || len(parents[0].Children) != 2 || len(parents[1].Children) != 0 {
			t.Errorf("mode %v: %+v", mode, parents)
		}
	}
}

// --- materialized and plain views --------------------------------------------------

type liveUserDirectory struct {
	UserID   int64  `orm:"column"`
	UserName string `orm:"column"`
}

func (liveUserDirectory) Entity() orm.EntityDef {
	return orm.EntityDef{
		Source:  orm.MaterializedView("live_user_directory", "select id as user_id, name as user_name from users"),
		Indexes: []orm.IndexDef{orm.Index("UserID").Unique()},
	}
}

func TestLive_MaterializedViewIsCreatedAndRead(t *testing.T) {
	ctx := context.Background()
	db, _ := openSample(t, orm.Options{})
	testsupport.InsertUser(t, db, "Ada", "ada@example.com")
	testsupport.InsertUser(t, db, "Grace", "grace@example.com")

	// A materialized view holds its rows from creation on (REFRESH is Level 4).
	if err := orm.CreateView[liveUserDirectory](ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := orm.CreateView[liveUserDirectory](ctx, db); err != nil { // idempotent: if not exists
		t.Fatalf("second create: %v", err)
	}
	rows, err := orm.QueryAll[liveUserDirectory](ctx, db)
	if err != nil || len(rows) != 2 {
		t.Fatalf("got %+v, %v", rows, err)
	}
	found, err := orm.From[liveUserDirectory](db).Where(orm.Eq("UserName", "Grace")).Single(ctx)
	if err != nil || found.UserName != "Grace" {
		t.Fatalf("criteria over the view: %+v, %v", found, err)
	}
}

func TestLive_SampleViewReadsNumericCountAndTimestampAggregates(t *testing.T) {
	ctx := context.Background()
	db, _ := openSample(t, orm.Options{})
	ada, _ := seedGraph(t, db)

	total, err := orm.Get[sample.UserTransactionTotal](ctx, db, ada.ID)
	if err != nil {
		t.Fatal(err)
	}
	if total.TransactionCount != 2 || !total.TotalAmount.Equal(orm.MustDecimal("20")) ||
		total.LastTransactionAtUtc == nil || !total.LastTransactionAtUtc.Equal(testsupport.SeedTime) {
		t.Errorf("unexpected totals: %+v", total)
	}
}

// --- migrations ------------------------------------------------------------------------

func TestLive_MigrationsRunRerunAndDownUnderTheAdvisoryLock(t *testing.T) {
	ctx := context.Background()
	connection := newDatabase(t)
	dialect := postgres.New()
	pool, err := dialect.CreateConnection(connection)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	set, err := orm.NewMigrationSet(
		orm.NewSQLVersion(1, orm.SQLVersionStep{
			ObjectName: "widgets", Description: "Create widgets",
			Up:   []string{`create table "widgets" ("id" bigint primary key, "name" text not null)`},
			Down: []string{`drop table "widgets"`},
		}),
		orm.NewSQLVersion(2, orm.SQLVersionStep{
			ObjectName: "widgets", Description: "Add note",
			Up:   []string{`alter table "widgets" add column "note" text`, `insert into "widgets" ("id", "name", "note") values (1, 'seed', 'n')`},
			Down: []string{`alter table "widgets" drop column "note"`},
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	runner := migrations.NewRunner(pool, dialect, metadata.NewLoader(nil), set, nil)

	applied, err := runner.Migrate(ctx, migrations.RunOptions{})
	if err != nil || applied != 2 {
		t.Fatalf("expected 2 applied, got %d, %v", applied, err)
	}
	if again, err := runner.Migrate(ctx, migrations.RunOptions{}); err != nil || again != 0 {
		t.Fatalf("a re-run should apply nothing, got %d, %v", again, err)
	}

	scalar := func(statement string) int64 {
		var n int64
		if err := pool.QueryRowContext(ctx, statement).Scan(&n); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
		return n
	}
	if n := scalar(`select count(*) from schema_version`); n != 2 {
		t.Errorf("expected 2 schema_version rows, got %d", n)
	}
	if n := scalar(`select count(*) from information_schema.columns where table_name = 'widgets' and column_name = 'note'`); n != 1 {
		t.Errorf("expected the added column, got %d", n)
	}

	reverted, err := runner.MigrateDown(ctx, 1, migrations.RunOptions{})
	if err != nil || reverted != 1 {
		t.Fatalf("expected 1 reverted, got %d, %v", reverted, err)
	}
	if n := scalar(`select count(*) from schema_version`); n != 1 {
		t.Errorf("expected 1 schema_version row after the rollback, got %d", n)
	}
	if n := scalar(`select count(*) from information_schema.columns where table_name = 'widgets' and column_name = 'note'`); n != 0 {
		t.Errorf("expected the column gone, got %d", n)
	}
	if reverted, err := runner.MigrateDown(ctx, 0, migrations.RunOptions{}); err != nil || reverted != 1 {
		t.Fatalf("down to zero: %d, %v", reverted, err)
	}
	if n := scalar(`select count(*) from information_schema.tables where table_name = 'widgets'`); n != 0 {
		t.Errorf("expected the table gone, got %d", n)
	}
}

func TestLive_AFailedMigrationRunAppliesNothing(t *testing.T) {
	ctx := context.Background()
	connection := newDatabase(t)
	dialect := postgres.New()
	pool, err := dialect.CreateConnection(connection)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	set, err := orm.NewMigrationSet(
		orm.NewSQLVersion(1, orm.SQLVersionStep{ObjectName: "good", Description: "Create", Up: []string{`create table "good" ("id" bigint)`}}),
		orm.NewSQLVersion(2, orm.SQLVersionStep{ObjectName: "bad", Description: "Fails", Up: []string{`create table "bad" ("id" bigint)`, `select nope from nowhere`}}),
	)
	if err != nil {
		t.Fatal(err)
	}
	runner := migrations.NewRunner(pool, dialect, metadata.NewLoader(nil), set, nil)
	if _, err := runner.Migrate(ctx, migrations.RunOptions{}); orm.CodeOf(err) != "MIG-021" {
		t.Fatalf("expected MIG-021, got %v", err)
	}

	var existing int64
	if err := pool.QueryRowContext(ctx, `select count(*) from information_schema.tables where table_name in ('good', 'bad', 'schema_version')`).Scan(&existing); err != nil {
		t.Fatal(err)
	}
	if existing != 0 {
		t.Errorf("transactional DDL: a failed run should apply nothing, found %d of its tables", existing)
	}
}

func TestLive_TheRunLockIsHeldByTheRunsTransactionAndReleasedWithIt(t *testing.T) {
	ctx := context.Background()
	connection := newDatabase(t)
	dialect := postgres.New()
	pool, err := dialect.CreateConnection(connection)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	holder, err := pool.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	other, err := pool.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()

	tryLock := func() bool {
		var got bool
		tx, err := other.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		if err := tx.QueryRowContext(ctx, `select pg_try_advisory_xact_lock(1399418224, 1818578802)`).Scan(&got); err != nil {
			t.Fatal(err)
		}
		return got
	}

	if !tryLock() {
		t.Fatal("the lock should be free before the run")
	}
	tx, err := dialect.BeginMigrationRunLock(ctx, holder)
	if err != nil {
		t.Fatal(err)
	}
	if tryLock() {
		t.Error("another session got the lock while the run's transaction holds it")
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if !tryLock() {
		t.Error("the lock should be released with the transaction")
	}
}

// --- the VAL-020 read rule by column type ------------------------------------------------

type legacyRow struct {
	ID    int64      `orm:"column,key,generated"`
	Stamp time.Time  `orm:"column"`
	Maybe *time.Time `orm:"column"`
}

func (legacyRow) Entity() orm.EntityDef { return orm.EntityDef{Source: orm.Table("legacy_rows")} }

// legacyInstant is the handler escape for a markerless column: the registered type declares the column's actual kind.
type legacyInstant struct{ time.Time }

type legacyInstantHandler struct{}

func (legacyInstantHandler) Parse(value any) (legacyInstant, error) {
	instant, ok := value.(time.Time)
	if !ok {
		return legacyInstant{}, errors.New("not a time.Time")
	}
	return legacyInstant{Time: time.Date(instant.Year(), instant.Month(), instant.Day(), instant.Hour(), instant.Minute(),
		instant.Second(), instant.Nanosecond(), time.UTC)}, nil
}

func (legacyInstantHandler) Format(value legacyInstant) (any, error) { return value.Time, nil }

type legacyHandledRow struct {
	ID    int64         `orm:"column,key,generated"`
	Stamp legacyInstant `orm:"column"`
}

func (legacyHandledRow) Entity() orm.EntityDef {
	return orm.EntityDef{Source: orm.Table("legacy_rows")}
}

func TestLive_AMarkerlessTimestampColumnRefusesVAL020AndAHandlerIsTheEscape(t *testing.T) {
	ctx := context.Background()
	db, connection := openBare(t, orm.Options{})
	execRaw(t, connection, `create table "legacy_rows" ("id" bigint generated by default as identity primary key, `+
		`"stamp" timestamp without time zone not null, "maybe" timestamp without time zone)`)
	execRaw(t, connection, `insert into "legacy_rows" ("stamp") values ('2026-01-02 03:04:05')`)

	if _, err := orm.QueryAll[legacyRow](ctx, db); orm.CodeOf(err) != "VAL-020" {
		t.Fatalf("expected VAL-020 from select-all, got %v", err)
	}
	if _, err := orm.Get[legacyRow](ctx, db, int64(1)); orm.CodeOf(err) != "VAL-020" {
		t.Fatalf("expected VAL-020 from read-by-key, got %v", err)
	}
	if _, err := orm.From[legacyRow](db).Where(orm.Eq("Id", int64(1))).List(ctx); orm.CodeOf(err) != "VAL-020" {
		t.Fatalf("expected VAL-020 from a criteria query, got %v", err)
	}
	scalar := orm.Inline[orm.EmptyArgs, time.Time]("select stamp from legacy_rows")
	if _, err := orm.QuerySingle(ctx, db, scalar, orm.EmptyArgs{}); orm.CodeOf(err) != "VAL-020" {
		t.Fatalf("expected VAL-020 from a scalar read, got %v", err)
	}

	// A NULL in a markerless column is no value to refuse; a timestamptz expression over it is marked.
	execRaw(t, connection, `update "legacy_rows" set "stamp" = '2026-01-02 03:04:05'`)
	marked := orm.Inline[orm.EmptyArgs, time.Time](`select stamp at time zone 'UTC' from legacy_rows`)
	if got, err := orm.QuerySingle(ctx, db, marked, orm.EmptyArgs{}); err != nil || got.Format("2006-01-02T15:04:05") != "2026-01-02T03:04:05" {
		t.Fatalf("a timestamptz expression should read: %v, %v", got, err)
	}

	handlers := orm.NewTypeHandlerRegistry()
	orm.RegisterHandler[legacyInstant](handlers, legacyInstantHandler{})
	handled, _ := openLegacy(t, connection, orm.Options{TypeHandlers: handlers})
	rows, err := orm.QueryAll[legacyHandledRow](ctx, handled)
	if err != nil || len(rows) != 1 || rows[0].Stamp.Format("2006-01-02T15:04:05") != "2026-01-02T03:04:05" {
		t.Fatalf("the handler should read the legacy column: %+v, %v", rows, err)
	}
}

func openLegacy(t *testing.T, connection string, options orm.Options) (*orm.Db, string) {
	t.Helper()
	options.Dialect = postgres.New()
	db, err := orm.Open(context.Background(), connection, options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, connection
}

func TestLive_JoinModeRefusesAMarkerlessColumnToo(t *testing.T) {
	ctx := context.Background()
	db, connection := openBare(t, orm.Options{})
	createTables(t, db, orm.CreateTable[joinOwner])
	execRaw(t, connection, `create table "legacy_children" ("id" bigint generated by default as identity primary key, `+
		`"owner_id" bigint not null, "stamp" timestamp without time zone not null)`)
	execRaw(t, connection, `insert into "legacy_children" ("owner_id", "stamp") values (1, '2026-01-02 03:04:05')`)
	if err := orm.Insert(ctx, db, &joinOwner{ID: 1}); err != nil {
		t.Fatal(err)
	}

	for _, mode := range []orm.FetchMode{orm.FetchMultiQuery, orm.FetchJoin} {
		if _, err := orm.From[joinOwner](db).Include("Items").Fetch(mode).List(ctx); orm.CodeOf(err) != "VAL-020" {
			t.Errorf("mode %v: expected VAL-020 from the markerless segment, got %v", mode, err)
		}
	}
}

type joinOwner struct {
	ID    int64        `orm:"column,key"`
	Items []*joinChild `orm:"one_to_many=OwnerID"`
}

func (joinOwner) Entity() orm.EntityDef { return orm.EntityDef{Source: orm.Table("join_owners")} }

type joinChild struct {
	ID      int64     `orm:"column,key,generated"`
	OwnerID int64     `orm:"column"`
	Stamp   time.Time `orm:"column"`
}

func (joinChild) Entity() orm.EntityDef { return orm.EntityDef{Source: orm.Table("legacy_children")} }

// --- tracked sessions, the unit of work, and the hooks ------------------------------------

func TestLive_TrackedSessionFlushesAGraphAndTheHooksStamp(t *testing.T) {
	ctx := context.Background()
	db, connection := openSample(t, orm.Options{Tracking: orm.TrackingTracked})
	ada := testsupport.InsertUser(t, db, "Ada", "ada@example.com")

	transaction := &sample.Transaction{UserID: ada.ID, User: ada, Status: sample.Pending, Amount: orm.MustDecimal("5")}
	transaction.Details = []*sample.TransactionDetail{
		{Description: "widget", Quantity: 2, UnitPrice: orm.MustDecimal("1.5")},
		{Description: "gadget", Quantity: 1, UnitPrice: orm.MustDecimal("2")},
	}
	if err := orm.Persist(db, transaction); err != nil {
		t.Fatal(err)
	}
	summary, err := db.Flush(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Inserted != 3 || summary.Updated != 0 || summary.Deleted != 0 {
		t.Fatalf("unexpected summary %+v", summary)
	}
	if transaction.ID == 0 || transaction.Details[0].TransactionID != transaction.ID || transaction.Details[1].ID == 0 {
		t.Errorf("generated keys should flow to the dependents: %+v / %+v", transaction, transaction.Details)
	}
	if !transaction.CreatedAtUtc.Equal(testsupport.ClockNow) {
		t.Errorf("the created_at hook should stamp the session clock, got %v", transaction.CreatedAtUtc)
	}

	transaction.Status = sample.Completed
	summary, err = db.Flush(ctx)
	if err != nil || summary.Updated != 1 || summary.Inserted != 0 {
		t.Fatalf("expected one dirty update, got %+v, %v", summary, err)
	}
	if transaction.Version != 1 || transaction.UpdatedAtUtc == nil || !transaction.UpdatedAtUtc.Equal(testsupport.ClockNow) {
		t.Errorf("the update should bump the version and stamp updated_at: %+v", transaction)
	}

	// Read it back through a second, untracked session: what was flushed is what the database holds.
	other, err := orm.Open(ctx, connection, orm.Options{Dialect: postgres.New()})
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	stored, err := orm.Get[sample.Transaction](ctx, other, transaction.ID)
	if err != nil || stored.Status != sample.Completed || stored.Version != 1 || stored.UpdatedAtUtc == nil {
		t.Fatalf("stored: %+v, %v", stored, err)
	}
	details, err := orm.From[sample.TransactionDetail](other).Where(orm.Eq("TransactionID", transaction.ID)).OrderBy("Id").List(ctx)
	if err != nil || len(details) != 2 || !details[0].UnitPrice.Equal(orm.MustDecimal("1.5")) {
		t.Fatalf("details: %+v, %v", details, err)
	}

	// Soft delete through a hook is a flag-setting update, not a DELETE.
	role := &sample.Role{Name: "soft"}
	if err := orm.Insert(ctx, db, role); err != nil {
		t.Fatal(err)
	}
	if err := orm.Delete[sample.Role](ctx, db, role.ID); err != nil {
		t.Fatal(err)
	}
	kept, err := orm.Get[sample.Role](ctx, other, role.ID)
	if err != nil || !kept.IsDeleted {
		t.Fatalf("expected the soft-deleted row to remain flagged, got %+v, %v", kept, err)
	}
}

// --- SchemaGuard against the live schema -------------------------------------------------------

type mismatchedUser struct {
	ID   string
	Name string
}

func TestLive_SchemaGuardPassesTheSampleAndReportsOneMismatchAndABrokenStatement(t *testing.T) {
	ctx := context.Background()
	db, _ := openSample(t, orm.Options{})

	clean := orm.Registry{
		Entries: []orm.Entry{usersByIds, usersByNames, orm.Inline[orm.EmptyArgs, int64]("select count(*) as total -- notnull: total\nfrom users")},
		Entities: []reflect.Type{
			reflect.TypeFor[sample.User](), reflect.TypeFor[sample.Role](), reflect.TypeFor[sample.UserRole](),
			reflect.TypeFor[sample.UserProfile](), reflect.TypeFor[sample.Transaction](),
			reflect.TypeFor[sample.TransactionDetail](), reflect.TypeFor[sample.UserTransactionTotal](),
		},
	}
	if err := orm.Validate(ctx, db, clean); err != nil {
		t.Fatalf("expected a clean validation of the live schema, got: %v", err)
	}

	broken := orm.Registry{Entries: []orm.Entry{
		orm.Inline[orm.EmptyArgs, mismatchedUser]("select nope, name from users"),
		orm.Inline[orm.EmptyArgs, mismatchedUser]("select id, name from users"),
		usersByIds,
	}}
	err := orm.Validate(ctx, db, broken)
	if !orm.HasCode(err, "VAL-001") {
		t.Errorf("expected VAL-001 for the unparseable statement, got %v", err)
	}
	if !orm.HasCode(err, "VAL-011") {
		t.Errorf("expected VAL-011 (a bigint id mapped to a string) reported after the broken statement, got %v", err)
	}
	var report *orm.ValidationErrors
	if !errors.As(err, &report) || len(report.Errors) != 2 {
		t.Errorf("expected exactly the two violations (the savepoint keeps later statements describable), got %v", err)
	}
}

func TestLive_DescribeStatementReadsTheResultColumnsWithoutExecuting(t *testing.T) {
	ctx := context.Background()
	_, connection := openSample(t, orm.Options{})
	dialect := postgres.New()
	pool, err := dialect.CreateConnection(connection)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	conn, err := pool.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	described, err := dialect.DescribeStatement(ctx, conn,
		"select id, name || '' as shout, created_at from users where id = @id and name <> @id::text")
	if err != nil {
		t.Fatal(err)
	}
	want := []core.ColumnDescription{
		{Name: "id", DeclaredType: "bigint", Table: "users", OriginColumn: "id"},
		{Name: "shout", DeclaredType: "text"},
		{Name: "created_at", DeclaredType: "timestamp with time zone", Table: "users", OriginColumn: "created_at"},
	}
	if !reflect.DeepEqual(described, want) {
		t.Errorf("got %+v, want %+v", described, want)
	}

	// A write is described (its RETURNING columns), never executed.
	described, err = dialect.DescribeStatement(ctx, conn,
		`insert into "users" ("name", "email", "created_at") values (@name, @email, @created_at) returning "id"`)
	if err != nil || len(described) != 1 || described[0].OriginColumn != "id" {
		t.Fatalf("got %+v, %v", described, err)
	}
	var rows int64
	if err := conn.QueryRowContext(ctx, `select count(*) from users`).Scan(&rows); err != nil || rows != 0 {
		t.Errorf("describing must not execute: %d rows, %v", rows, err)
	}

	if _, err := dialect.DescribeStatement(ctx, conn, "select nope from users"); err == nil {
		t.Error("a statement the server cannot parse should be an error")
	}
	// The connection is still usable afterwards, and a statement with no result describes as no columns.
	described, err = dialect.DescribeStatement(ctx, conn, "update users set name = @name")
	if err != nil || len(described) != 0 {
		t.Errorf("got %+v, %v", described, err)
	}
}

// TestLive_BytesRoundTrip writes and matches bytea values text binding would corrupt (ADR-0040 add.1).
func TestLive_BytesRoundTrip(t *testing.T) {
	db, _ := openSample(t, orm.Options{})
	testsupport.BytesRoundTrip(t, db)
}

type planArgs struct{ ID int64 }

var (
	noteByID      = orm.Inline[planArgs, string]("select note from plan_rows where id = @ID")
	noteAsVarchar = orm.InlineCommand[orm.EmptyArgs]("alter table plan_rows alter column note type varchar(40)")
	noteAsText    = orm.InlineCommand[orm.EmptyArgs]("alter table plan_rows alter column note type text")
)

// openPlanSession is a session on a fresh database holding plan_rows(1, 'kept'), opened with the
// given dialect — one pinned connection, so pgx's statement cache lives as long as the session.
func openPlanSession(t *testing.T, dialect *postgres.Dialect) *orm.Db {
	t.Helper()
	connection := newDatabase(t)
	execRaw(t, connection, "create table plan_rows (id bigint primary key, note text not null)")
	execRaw(t, connection, "insert into plan_rows (id, note) values (1, 'kept')")
	db, err := orm.Open(context.Background(), connection, orm.Options{Dialect: dialect, Clock: testsupport.FixedClock})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// isCachedPlanError reports SQLSTATE 0A000 "cached plan must not change result type".
func isCachedPlanError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "cached plan must not change result type") && strings.Contains(err.Error(), "0A000")
}

// TestLive_ACachedPlanAfterASchemaChangeRetriesOnceOutsideATransaction pins the default
// CachedPlanRetryOnce (ADR-0040 add.1, spec/dialects.md Clarifications): a statement pgx prepared and
// cached meets a table whose column type changed — outside a transaction it is run once more and
// succeeds; inside one PostgreSQL has aborted the transaction, the error surfaces unchanged, and
// after the rollback the statement works again (pgx invalidated its cache entry).
func TestLive_ACachedPlanAfterASchemaChangeRetriesOnceOutsideATransaction(t *testing.T) {
	ctx := context.Background()
	db := openPlanSession(t, postgres.New())

	if note, err := orm.QuerySingle(ctx, db, noteByID, planArgs{ID: 1}); err != nil || note != "kept" {
		t.Fatalf("first read (prepares and caches): %q, %v", note, err)
	}
	if _, err := orm.Execute(ctx, db, noteAsVarchar, orm.EmptyArgs{}); err != nil {
		t.Fatal(err)
	}
	if note, err := orm.QuerySingle(ctx, db, noteByID, planArgs{ID: 1}); err != nil || note != "kept" {
		t.Fatalf("outside a transaction the stale plan should be retried once: %q, %v", note, err)
	}

	// The cache now holds the varchar plan; change the type back, then read inside a transaction.
	if _, err := orm.Execute(ctx, db, noteAsText, orm.EmptyArgs{}); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := orm.QuerySingle(ctx, db, noteByID, planArgs{ID: 1}); !isCachedPlanError(err) {
		t.Fatalf("inside a transaction the 0A000 error should surface unchanged, got %v", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if note, err := orm.QuerySingle(ctx, db, noteByID, planArgs{ID: 1}); err != nil || note != "kept" {
		t.Fatalf("after the rollback the statement re-prepares: %q, %v", note, err)
	}
}

// TestLive_TheSurfacePolicyNeverRetriesACachedPlan pins CachedPlanSurface: the 0A000 error surfaces
// outside a transaction too, and the next run re-prepares (pgx's own invalidation).
func TestLive_TheSurfacePolicyNeverRetriesACachedPlan(t *testing.T) {
	ctx := context.Background()
	db := openPlanSession(t, postgres.New(postgres.WithCachedPlanPolicy(postgres.CachedPlanSurface)))

	if _, err := orm.QuerySingle(ctx, db, noteByID, planArgs{ID: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := orm.Execute(ctx, db, noteAsVarchar, orm.EmptyArgs{}); err != nil {
		t.Fatal(err)
	}
	if _, err := orm.QuerySingle(ctx, db, noteByID, planArgs{ID: 1}); !isCachedPlanError(err) {
		t.Fatalf("CachedPlanSurface should return the 0A000 error, got %v", err)
	}
	if note, err := orm.QuerySingle(ctx, db, noteByID, planArgs{ID: 1}); err != nil || note != "kept" {
		t.Fatalf("the next run re-prepares: %q, %v", note, err)
	}
}
