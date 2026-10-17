package sqlserver_test

// The SQL Server live leg (spec/dialects.md "Testing contract", ADR-0024):
// every test below runs against a real server — never a mock — in a database
// of its own (`simpleorm_test_<hex>`, created and dropped on the admin
// connection), and skips as a group where nothing answers, so `go test ./...`
// still needs only the Go toolchain.
//
// The admin connection is the environment variable SIMPLEORM_SQLSERVER (a
// go-mssqldb connection string with the right to create databases), else
// `sqlserver://localhost:1433?database=master&encrypt=disable` — which, with
// no user id, signs in with Windows integrated authentication on Windows.
//
// UNTESTED against a server on the machine this file was written on (LocalDB
// would not start, nothing listened on 1433): it compiles, vets, and skips;
// the first green run happens where a server answers.

import (
	"context"
	"database/sql"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/kosmassp/SimpleOrm/go/orm"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/metadata"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/testsupport"
	"github.com/kosmassp/SimpleOrm/go/orm/migrations"
	"github.com/kosmassp/SimpleOrm/go/orm/sample"
	"github.com/kosmassp/SimpleOrm/go/orm/sqlserver"
)

// liveServer is the once-per-process probe of the admin connection (an
// immutable configuration plus the probe's own once-guarded result).
var liveServer = &testsupport.LiveServer{
	EnvironmentVariable: "SIMPLEORM_SQLSERVER",
	Default:             "sqlserver://localhost:1433?database=master&encrypt=disable",
	Open:                sqlserver.New().CreateConnection,
}

// --- fixture ------------------------------------------------------------------

// connectionStringFor is the admin connection string pointed at one database.
func connectionStringFor(admin, database string) string {
	if strings.Contains(admin, "://") {
		if u, err := url.Parse(admin); err == nil {
			query := u.Query()
			query.Set("database", database)
			u.RawQuery = query.Encode()
			return u.String()
		}
	}
	return strings.TrimRight(admin, "; ") + ";database=" + database
}

// newLiveDatabase creates a database for the calling test on the admin
// connection, drops it when the test ends, and returns its connection string.
// It skips the test where no server answers.
func newLiveDatabase(t *testing.T) string {
	t.Helper()
	liveServer.SkipUnlessAvailable(t, "no SQL Server answers (set SIMPLEORM_SQLSERVER to a go-mssqldb connection string)")
	ctx := context.Background()
	admin := liveServer.AdminConnectionString()
	name := testsupport.DatabaseName(t)

	adminPool, err := sqlserver.New().CreateConnection(admin)
	if err != nil {
		t.Fatalf("open the admin connection: %v", err)
	}
	if _, err := adminPool.ExecContext(ctx, "create database "+sqlserver.New().QuoteIdentifier(name)); err != nil {
		_ = adminPool.Close()
		t.Fatalf("create database %s: %v", name, err)
	}
	t.Cleanup(func() {
		defer adminPool.Close()
		quoted := sqlserver.New().QuoteIdentifier(name)
		// Open sessions are closed by their own (later-registered) cleanups first; this evicts any straggler.
		_, _ = adminPool.ExecContext(ctx, "alter database "+quoted+" set single_user with rollback immediate")
		if _, err := adminPool.ExecContext(ctx, "drop database "+quoted); err != nil {
			t.Logf("drop database %s: %v", name, err)
		}
	})
	return connectionStringFor(admin, name)
}

// openLive opens a session on a database; closed when the test ends.
func openLive(t *testing.T, connectionString string, options orm.Options) *orm.Db {
	t.Helper()
	if options.Dialect == nil {
		options.Dialect = sqlserver.New()
	}
	if options.Clock == nil {
		options.Clock = testsupport.FixedClock
	}
	db, err := orm.Open(context.Background(), connectionString, options)
	if err != nil {
		t.Fatalf("open session: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// rawPool opens a plain pool on a database (outside any session), closed when the test ends.
func rawPool(t *testing.T, connectionString string) *sql.DB {
	t.Helper()
	pool, err := sqlserver.New().CreateConnection(connectionString)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(func() { _ = pool.Close() })
	return pool
}

// createSampleSchema creates every sample table and the view from metadata (ADR-0011).
func createSampleSchema(t *testing.T, db *orm.Db) {
	t.Helper()
	ctx := context.Background()
	for _, create := range []func(context.Context, *orm.Db) error{
		orm.CreateTable[sample.User],
		orm.CreateTable[sample.Role],
		orm.CreateTable[sample.UserRole],
		orm.CreateTable[sample.Transaction],
		orm.CreateTable[sample.TransactionDetail],
		orm.CreateTable[sample.UserProfile],
	} {
		if err := create(ctx, db); err != nil {
			t.Fatalf("create sample table: %v", err)
		}
	}
	if err := orm.CreateView[sample.UserTransactionTotal](ctx, db); err != nil {
		t.Fatalf("create sample view: %v", err)
	}
}

// newSampleSession is a fresh database holding the sample schema, and a session on it.
func newSampleSession(t *testing.T, options orm.Options) (*orm.Db, string) {
	t.Helper()
	connectionString := newLiveDatabase(t)
	db := openLive(t, connectionString, options)
	createSampleSchema(t, db)
	return db, connectionString
}

func names(users []sample.User) []string {
	out := make([]string, len(users))
	for i, u := range users {
		out[i] = u.Name
	}
	return out
}

func mustInsert[T any](t *testing.T, db *orm.Db, entity *T) {
	t.Helper()
	if err := orm.Insert(context.Background(), db, entity); err != nil {
		t.Fatalf("insert %T: %v", entity, err)
	}
}

// --- identity key, temporals, decimals ----------------------------------------------

func TestLive_IdentityKeyWriteBackAndDatetimeOffsetRoundTrip(t *testing.T) {
	ctx := context.Background()
	db, _ := newSampleSession(t, orm.Options{})

	ada := testsupport.InsertUser(t, db, "Ada", "ada@example.com")
	grace := testsupport.InsertUser(t, db, "Grace", "grace@example.com")
	if ada.ID <= 0 || grace.ID != ada.ID+1 {
		t.Fatalf("the identity key is written back onto the entity: ada %d, grace %d", ada.ID, grace.ID)
	}

	got, err := orm.Get[sample.User](ctx, db, ada.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.CreatedAtUtc.Equal(testsupport.SeedTime) || got.CreatedAtUtc.Location() != time.UTC {
		t.Errorf("datetimeoffset reads back as the same instant in UTC: %v (%v)", got.CreatedAtUtc, got.CreatedAtUtc.Location())
	}
	if got.UpdatedAtUtc != nil || got.DisplayName != nil {
		t.Errorf("NULL columns read back as nil pointers: %+v", got)
	}

	// A temporal criteria comparison binds the ISO-8601 string; datetimeoffset converts it implicitly.
	later, err := orm.From[sample.User](db).Where(orm.Ge("CreatedAtUtc", testsupport.SeedTime.Add(-time.Hour))).List(ctx)
	if err != nil || len(later) != 2 {
		t.Fatalf("created_at >= (seed - 1h): %d rows, %v", len(later), err)
	}
	none, err := orm.From[sample.User](db).Where(orm.Gt("CreatedAtUtc", testsupport.SeedTime)).List(ctx)
	if err != nil || len(none) != 0 {
		t.Fatalf("created_at > seed: %d rows, %v", len(none), err)
	}
}

func TestLive_DecimalAndEnumRoundTrip(t *testing.T) {
	ctx := context.Background()
	db, _ := newSampleSession(t, orm.Options{})
	ada := testsupport.InsertUser(t, db, "Ada", "ada@example.com")

	order := &sample.Transaction{UserID: ada.ID, Status: sample.Completed, Amount: orm.MustDecimal("12.50")}
	mustInsert(t, db, order)

	got, err := orm.Get[sample.Transaction](ctx, db, order.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Amount.Equal(orm.MustDecimal("12.5")) || got.Status != sample.Completed {
		t.Errorf("decimal(38, 9) and nvarchar(100) enum read back: %v %v", got.Amount, got.Status)
	}
	if got.Version != 0 || got.CreatedAtUtc.IsZero() {
		t.Errorf("version starts at 0 and the created-at hook stamped the row: %+v", got)
	}

	// The view's coalesce(sum(decimal), 0) and count(...) read through the same pipeline.
	totals, err := orm.Get[sample.UserTransactionTotal](ctx, db, ada.ID)
	if err != nil {
		t.Fatal(err)
	}
	if totals.TransactionCount != 1 || !totals.TotalAmount.Equal(orm.MustDecimal("12.5")) || totals.LastTransactionAtUtc == nil {
		t.Errorf("view row: %+v", totals)
	}
}

// liveBlob is a client-GUID-keyed row with the remaining scalar kinds.
type liveBlob struct {
	ID    orm.GUID `orm:"column,key"`
	Data  []byte   `orm:"column"`
	Flag  bool     `orm:"column"`
	Score float64  `orm:"column"`
	Small int16    `orm:"column"`
	Note  *string  `orm:"column"`
}

func (liveBlob) Entity() orm.EntityDef { return orm.EntityDef{Source: orm.Table("live_blobs")} }

func TestLive_ClientGuidKeyAndScalarKindsRoundTrip(t *testing.T) {
	ctx := context.Background()
	connectionString := newLiveDatabase(t)
	db := openLive(t, connectionString, orm.Options{})
	if err := orm.CreateTable[liveBlob](ctx, db); err != nil {
		t.Fatal(err)
	}

	blob := &liveBlob{Data: []byte{0, 1, 2, 250, 251}, Flag: true, Score: 2.5, Small: -7}
	mustInsert(t, db, blob)
	if blob.ID.IsZero() {
		t.Fatal("an empty client GUID key is assigned before the insert")
	}

	got, err := orm.Get[liveBlob](ctx, db, blob.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != blob.ID {
		t.Errorf("uniqueidentifier reads back in RFC 4122 order (guid conversion): %v, want %v", got.ID, blob.ID)
	}
	if !reflect.DeepEqual(got.Data, blob.Data) || !got.Flag || got.Score != 2.5 || got.Small != -7 || got.Note != nil {
		t.Errorf("varbinary, bit, float, smallint, NULL nvarchar: %+v", got)
	}
}

// --- criteria paging ------------------------------------------------------------------------

func TestLive_CriteriaPagingWithAndWithoutOrderBy(t *testing.T) {
	ctx := context.Background()
	db, _ := newSampleSession(t, orm.Options{})
	for _, name := range []string{"Ed", "Ada", "Cy", "Bob", "Di"} {
		testsupport.InsertUser(t, db, name, strings.ToLower(name)+"@example.com")
	}

	page, err := orm.From[sample.User](db).OrderBy("Name").Limit(2).Offset(1).List(ctx)
	if err != nil || !reflect.DeepEqual(names(page), []string{"Bob", "Cy"}) {
		t.Fatalf("limit + offset after order by: %v, %v", names(page), err)
	}
	limited, err := orm.From[sample.User](db).OrderBy("Name", orm.Desc).Limit(2).List(ctx)
	if err != nil || !reflect.DeepEqual(names(limited), []string{"Ed", "Di"}) {
		t.Fatalf("limit only (offset 0 rows fetch next): %v, %v", names(limited), err)
	}
	tail, err := orm.From[sample.User](db).OrderBy("Name").Offset(3).List(ctx)
	if err != nil || !reflect.DeepEqual(names(tail), []string{"Di", "Ed"}) {
		t.Fatalf("offset only: %v, %v", names(tail), err)
	}

	// Without orderings the renderer adds order by (select null): the page is legal, if order-arbitrary.
	unordered, err := orm.From[sample.User](db).Limit(3).List(ctx)
	if err != nil || len(unordered) != 3 {
		t.Fatalf("limit without order by: %d rows, %v", len(unordered), err)
	}
	unorderedOffset, err := orm.From[sample.User](db).Offset(4).List(ctx)
	if err != nil || len(unorderedOffset) != 1 {
		t.Fatalf("offset without order by: %d rows, %v", len(unorderedOffset), err)
	}

	in, err := orm.From[sample.User](db).Where(orm.In("Name", "Ada", "Di", "Nobody")).OrderBy("Name").List(ctx)
	if err != nil || !reflect.DeepEqual(names(in), []string{"Ada", "Di"}) {
		t.Fatalf("in (@c0, @c1, @c2): %v, %v", names(in), err)
	}
}

// --- reserved-word identifiers ---------------------------------------------------------------------

// liveOrder is a table named order with a column named desc (ADR-0024: a
// legacy schema is full of reserved words).
type liveOrder struct {
	ID    int64  `orm:"column,key,generated"`
	Desc  string `orm:"column"`
	Group string `orm:"column"`
}

func (liveOrder) Entity() orm.EntityDef {
	return orm.EntityDef{Source: orm.Table("order"), Indexes: []orm.IndexDef{orm.Index("Desc").Named("ix_order_desc")}}
}

func TestLive_ReservedWordIdentifiersEndToEnd(t *testing.T) {
	ctx := context.Background()
	connectionString := newLiveDatabase(t)
	db := openLive(t, connectionString, orm.Options{})
	if err := orm.CreateTable[liveOrder](ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := orm.CreateTable[liveOrder](ctx, db); err != nil { // idempotent: the object_id guards
		t.Fatalf("CreateTable is idempotent: %v", err)
	}

	first := &liveOrder{Desc: "b", Group: "g1"}
	second := &liveOrder{Desc: "a", Group: "g2"}
	mustInsert(t, db, first)
	mustInsert(t, db, second)

	got, err := orm.Get[liveOrder](ctx, db, first.ID)
	if err != nil || got.Desc != "b" || got.Group != "g1" {
		t.Fatalf("get by key: %+v, %v", got, err)
	}
	sorted, err := orm.From[liveOrder](db).Where(orm.Ne("Group", "nobody")).OrderBy("Desc").List(ctx)
	if err != nil || len(sorted) != 2 || sorted[0].Desc != "a" {
		t.Fatalf("where/order by on reserved words: %+v, %v", sorted, err)
	}

	first.Desc = "c"
	if err := orm.Update(ctx, db, first); err != nil {
		t.Fatal(err)
	}
	if err := orm.UpdateOnly(ctx, db, first, "Group"); err != nil {
		t.Fatal(err)
	}
	if err := orm.Delete[liveOrder](ctx, db, second.ID); err != nil {
		t.Fatal(err)
	}
	remaining, err := orm.QueryAll[liveOrder](ctx, db)
	if err != nil || len(remaining) != 1 || remaining[0].Desc != "c" {
		t.Fatalf("after update and delete: %+v, %v", remaining, err)
	}
}

// --- version-column concurrency ---------------------------------------------------------------------------

func TestLive_VersionColumnConcurrencyOnUpdateAndDelete(t *testing.T) {
	ctx := context.Background()
	db, _ := newSampleSession(t, orm.Options{})
	ada := testsupport.InsertUser(t, db, "Ada", "ada@example.com")
	order := &sample.Transaction{UserID: ada.ID, Status: sample.Pending, Amount: orm.MustDecimal("5")}
	mustInsert(t, db, order)

	winner, err := orm.Get[sample.Transaction](ctx, db, order.ID)
	if err != nil {
		t.Fatal(err)
	}
	loser, err := orm.Get[sample.Transaction](ctx, db, order.ID)
	if err != nil {
		t.Fatal(err)
	}

	winner.Status = sample.Completed
	if err := orm.Update(ctx, db, &winner); err != nil {
		t.Fatal(err)
	}
	if winner.Version != 1 {
		t.Errorf("the in-memory version is bumped on success: %d", winner.Version)
	}

	loser.Status = sample.Cancelled
	if err := orm.Update(ctx, db, &loser); orm.CodeOf(err) != "CRUD-010" {
		t.Fatalf("a stale update is CRUD-010, got %v", err)
	}
	if err := orm.DeleteEntity(ctx, db, &loser); orm.CodeOf(err) != "CRUD-010" {
		t.Fatalf("a stale delete is CRUD-010, got %v", err)
	}
	if err := orm.DeleteEntity(ctx, db, &winner); err != nil {
		t.Fatalf("the current version deletes: %v", err)
	}
	if _, err := orm.Get[sample.Transaction](ctx, db, order.ID); orm.CodeOf(err) != "CRUD-001" {
		t.Fatalf("the row is gone: %v", err)
	}
}

// --- transaction scopes ------------------------------------------------------------------------------------------

func TestLive_TransactionScopeCommitAndRollback(t *testing.T) {
	ctx := context.Background()
	db, _ := newSampleSession(t, orm.Options{})

	rolledBack, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	testsupport.InsertUser(t, db, "Ada", "ada@example.com")
	if err := rolledBack.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if users, err := orm.QueryAll[sample.User](ctx, db); err != nil || len(users) != 0 {
		t.Fatalf("rollback undid the insert: %d rows, %v", len(users), err)
	}

	committed, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	testsupport.InsertUser(t, db, "Grace", "grace@example.com")
	if err := committed.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if users, err := orm.QueryAll[sample.User](ctx, db); err != nil || len(users) != 1 || users[0].Name != "Grace" {
		t.Fatalf("commit kept the insert: %v, %v", names(users), err)
	}
}

// --- loading: explicit, batch, link -----------------------------------------------------------------------------------

func TestLive_ExplicitBatchAndLinkLoading(t *testing.T) {
	ctx := context.Background()
	db, _ := newSampleSession(t, orm.Options{})
	ada := testsupport.InsertUser(t, db, "Ada", "ada@example.com")
	bob := testsupport.InsertUser(t, db, "Bob", "bob@example.com")
	for _, owner := range []*sample.User{ada, ada, bob} {
		mustInsert(t, db, &sample.Transaction{UserID: owner.ID, Status: sample.Pending, Amount: orm.MustDecimal("1")})
	}
	admin, member := &sample.Role{Name: "admin"}, &sample.Role{Name: "member"}
	mustInsert(t, db, admin)
	mustInsert(t, db, member)
	mustInsert(t, db, &sample.UserRole{UserID: ada.ID, RoleID: admin.ID})
	mustInsert(t, db, &sample.UserRole{UserID: ada.ID, RoleID: member.ID})

	// Explicit: one query for one owner.
	one, err := orm.Get[*sample.User](ctx, db, ada.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := orm.Load(ctx, db, one, "Transactions"); err != nil || len(one.Transactions) != 2 {
		t.Fatalf("explicit one-to-many load: %d, %v", len(one.Transactions), err)
	}

	// Batch: one query for many owners; each gets its own children.
	users := []*sample.User{ada, bob}
	if err := orm.LoadEach(ctx, db, users, "Transactions"); err != nil {
		t.Fatal(err)
	}
	if len(users[0].Transactions) != 2 || len(users[1].Transactions) != 1 {
		t.Errorf("batch load partitions by owner: %d and %d", len(users[0].Transactions), len(users[1].Transactions))
	}

	// Many-to-many: link rows, then the targets.
	if err := orm.Load(ctx, db, ada, "Roles"); err != nil || len(ada.Roles) != 2 || ada.Roles[0].Name != "admin" {
		t.Fatalf("many-to-many load: %+v, %v", ada.Roles, err)
	}
	if err := orm.Load(ctx, db, bob, "Roles"); err != nil || len(bob.Roles) != 0 {
		t.Fatalf("a user with no links loads an empty collection: %+v, %v", bob.Roles, err)
	}

	// Many-to-one.
	order, err := orm.Get[*sample.Transaction](ctx, db, one.Transactions[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := orm.Load(ctx, db, order, "User"); err != nil || order.User == nil || order.User.Name != "Ada" {
		t.Fatalf("many-to-one load: %+v, %v", order.User, err)
	}
}

// --- eager loading ---------------------------------------------------------------------------------------------------------

func TestLive_SubSelectIncludeOverAPagedRoot(t *testing.T) {
	ctx := context.Background()
	db, _ := newSampleSession(t, orm.Options{})
	for _, name := range []string{"Ada", "Bob", "Cy", "Di", "Ed"} {
		user := testsupport.InsertUser(t, db, name, strings.ToLower(name)+"@example.com")
		for range len(name) { // Ada 3, Bob 3, Cy 2, Di 2, Ed 2
			mustInsert(t, db, &sample.Transaction{UserID: user.ID, Status: sample.Pending, Amount: orm.MustDecimal("1")})
		}
	}

	page, err := orm.From[sample.User](db).Include("Transactions").Fetch(orm.FetchSubSelect).
		OrderBy("Name").Limit(2).Offset(1).List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(names(page), []string{"Bob", "Cy"}) || len(page[0].Transactions) != 3 || len(page[1].Transactions) != 2 {
		t.Errorf("a paged subselect include loads exactly the page's children: %v, %d, %d",
			names(page), len(page[0].Transactions), len(page[1].Transactions))
	}
}

func TestLive_EveryFetchModeLoadsTheSameGraph(t *testing.T) {
	ctx := context.Background()
	db, _ := newSampleSession(t, orm.Options{})
	for _, name := range []string{"Ada", "Bob", "Cy"} {
		user := testsupport.InsertUser(t, db, name, strings.ToLower(name)+"@example.com")
		for range len(name) {
			mustInsert(t, db, &sample.Transaction{UserID: user.ID, Status: sample.Pending, Amount: orm.MustDecimal("1")})
		}
	}

	counts := map[orm.FetchMode][]int{}
	for _, mode := range []orm.FetchMode{orm.FetchMultiQuery, orm.FetchSubSelect, orm.FetchJoin} {
		users, err := orm.From[sample.User](db).Include("Transactions").Fetch(mode).OrderBy("Name").List(ctx)
		if err != nil {
			t.Fatalf("fetch mode %v: %v", mode, err)
		}
		for _, user := range users {
			counts[mode] = append(counts[mode], len(user.Transactions))
		}
	}
	want := []int{3, 3, 2}
	for mode, got := range counts {
		if !reflect.DeepEqual(got, want) {
			t.Errorf("fetch mode %v loaded %v, want %v", mode, got, want)
		}
	}
}

// liveParent / liveChild are a two-column key and the foreign key list that
// references it: the composite-key subselect has no row-value IN on SQL
// Server, so the renderer rewrites it as a correlated EXISTS.
type liveParent struct {
	A        int64        `orm:"column,key"`
	B        int64        `orm:"column,key"`
	Name     string       `orm:"column"`
	Children []*liveChild `orm:"one_to_many=ParentA+ParentB"`
}

func (liveParent) Entity() orm.EntityDef { return orm.EntityDef{Source: orm.Table("live_parents")} }

type liveChild struct {
	ID      int64       `orm:"column,key,generated"`
	ParentA int64       `orm:"column"`
	ParentB int64       `orm:"column"`
	Note    string      `orm:"column"`
	Parent  *liveParent `orm:"many_to_one=ParentA+ParentB"`
}

func (liveChild) Entity() orm.EntityDef { return orm.EntityDef{Source: orm.Table("live_children")} }

func TestLive_CompositeKeySubSelectThroughTheExistsRewrite(t *testing.T) {
	ctx := context.Background()
	connectionString := newLiveDatabase(t)
	db := openLive(t, connectionString, orm.Options{})
	if err := orm.CreateTable[liveParent](ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := orm.CreateTable[liveChild](ctx, db); err != nil {
		t.Fatal(err)
	}

	for _, parent := range []*liveParent{{A: 1, B: 1, Name: "P11"}, {A: 1, B: 2, Name: "P12"}, {A: 2, B: 1, Name: "P21"}} {
		mustInsert(t, db, parent)
	}
	for _, child := range []*liveChild{
		{ParentA: 1, ParentB: 1, Note: "x"}, {ParentA: 1, ParentB: 1, Note: "y"}, {ParentA: 1, ParentB: 2, Note: "z"},
	} {
		mustInsert(t, db, child)
	}

	for _, paged := range []bool{false, true} {
		query := orm.From[liveParent](db).Include("Children").Fetch(orm.FetchSubSelect).OrderBy("Name")
		if paged {
			query = query.Limit(2)
		}
		parents, err := query.List(ctx)
		if err != nil {
			t.Fatalf("paged=%v: %v", paged, err)
		}
		if len(parents) < 2 || len(parents[0].Children) != 2 || len(parents[1].Children) != 1 {
			t.Errorf("paged=%v: the composite subselect loads each parent's children: %+v", paged, parents)
		}
	}

	// The reverse direction: a many-to-one through the two-part foreign key.
	child, err := orm.Get[*liveChild](ctx, db, int64(3))
	if err != nil {
		t.Fatal(err)
	}
	if err := orm.Load(ctx, db, child, "Parent"); err != nil || child.Parent == nil || child.Parent.Name != "P12" {
		t.Fatalf("composite many-to-one: %+v, %v", child.Parent, err)
	}
}

// --- unit of work with hooks -------------------------------------------------------------------------------------------------------

func TestLive_TrackedGraphPersistAndFlushStampsTheHooks(t *testing.T) {
	ctx := context.Background()
	db, connectionString := newSampleSession(t, orm.Options{Tracking: orm.TrackingTracked})

	user := &sample.User{Name: "Ada", Email: "ada@example.com"}
	order := &sample.Transaction{User: user, Status: sample.Pending, Amount: orm.MustDecimal("9.99")}
	order.Details = []*sample.TransactionDetail{
		{Description: "widget", Quantity: 2, UnitPrice: orm.MustDecimal("4.995")},
		{Description: "gadget", Quantity: 1, UnitPrice: orm.MustDecimal("0")},
	}
	if err := orm.Persist(db, user); err != nil {
		t.Fatal(err)
	}
	if err := orm.Persist(db, order); err != nil {
		t.Fatal(err)
	}

	summary, err := db.Flush(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Inserted != 4 {
		t.Errorf("one user, one transaction, two details: %+v", summary)
	}
	if user.ID == 0 || order.UserID != user.ID || order.Details[0].TransactionID != order.ID {
		t.Errorf("generated keys flow into dependents: user %d, order.UserID %d, order %d, detail.TransactionID %d",
			user.ID, order.UserID, order.ID, order.Details[0].TransactionID)
	}
	if !order.CreatedAtUtc.Equal(testsupport.ClockNow) || user.UpdatedAtUtc != nil {
		t.Errorf("the created-at hook stamped the insert from the session clock: %v", order.CreatedAtUtc)
	}

	order.Status = sample.Completed
	summary, err = db.Flush(ctx)
	if err != nil || summary.Updated != 1 {
		t.Fatalf("a dirty tracked instance flushes one update: %+v, %v", summary, err)
	}
	if order.UpdatedAtUtc == nil || !order.UpdatedAtUtc.Equal(testsupport.ClockNow) || order.Version != 1 {
		t.Errorf("the updated-at hook stamped and the version moved: %v, v%d", order.UpdatedAtUtc, order.Version)
	}

	// A second session sees the committed rows.
	other := openLive(t, connectionString, orm.Options{})
	stored, err := orm.Get[sample.Transaction](ctx, other, order.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != sample.Completed || stored.Version != 1 || stored.UpdatedAtUtc == nil || !stored.UpdatedAtUtc.Equal(testsupport.ClockNow) {
		t.Errorf("stored row: %+v", stored)
	}
	details, err := orm.From[sample.TransactionDetail](other).Where(orm.Eq("TransactionID", order.ID)).OrderBy("Description").List(ctx)
	if err != nil || len(details) != 2 || !details[1].UnitPrice.Equal(orm.MustDecimal("4.995")) {
		t.Errorf("details: %+v, %v", details, err)
	}
}

// --- registry queries -------------------------------------------------------------------------------------------------------------

func TestLive_RegistryQueriesAndCommands(t *testing.T) {
	ctx := context.Background()
	db, _ := newSampleSession(t, orm.Options{})
	ada := testsupport.InsertUser(t, db, "Ada", "ada@example.com")
	bob := testsupport.InsertUser(t, db, "Bob", "bob@example.com")
	order := &sample.Transaction{UserID: ada.ID, Status: sample.Pending, Amount: orm.MustDecimal("3")}
	mustInsert(t, db, order)

	byEmail, err := orm.QuerySingle(ctx, db, sample.UsersByEmail, sample.UsersByEmailArgs{Email: "bob@example.com"})
	if err != nil || byEmail.ID != bob.ID {
		t.Fatalf("named parameter: %+v, %v", byEmail, err)
	}
	byIDs, err := orm.Query(ctx, db, sample.UsersByIDs, sample.UsersByIDsArgs{IDs: []int64{bob.ID, ada.ID, 9999}})
	if err != nil || len(byIDs) != 2 || byIDs[0].ID != ada.ID {
		t.Fatalf("IN-list expansion (@IDs_0, @IDs_1, @IDs_2): %+v, %v", byIDs, err)
	}
	empty, err := orm.Query(ctx, db, sample.UsersByIDs, sample.UsersByIDsArgs{IDs: nil})
	if err != nil || len(empty) != 0 {
		t.Fatalf("an empty collection matches no rows: %+v, %v", empty, err)
	}

	affected, err := orm.Execute(ctx, db, sample.MarkTransactionStatus, sample.MarkTransactionStatusArgs{
		ID: order.ID, Status: sample.Completed, UpdatedAt: testsupport.ClockNow,
	})
	if err != nil || affected != 1 {
		t.Fatalf("command: %d, %v", affected, err)
	}
	marked, err := orm.Get[sample.Transaction](ctx, db, order.ID)
	if err != nil || marked.Status != sample.Completed || marked.UpdatedAtUtc == nil || !marked.UpdatedAtUtc.Equal(testsupport.ClockNow) {
		t.Fatalf("the command's temporal parameter reached the datetimeoffset column: %+v, %v", marked, err)
	}
}

// --- migrations under the run lock ----------------------------------------------------------------------------------------------------------

// liveWidget is the migrated table; its steps are the V000N types below (the
// names MIG-001 parses). V0001 creates it from metadata, V0002 adds a column,
// V0003 renames it — each with a hand-written Down, because derived rollbacks
// need snapshot tooling SQL Server does not have yet (MIG-020).
type liveWidget struct {
	ID   int64  `orm:"column,key,generated"`
	Name string `orm:"column"`
}

func (liveWidget) Entity() orm.EntityDef { return orm.EntityDef{Source: orm.Table("live_widgets")} }

type V0001_CreateWidgets struct {
	migrations.TableMigration[liveWidget]
}

func (V0001_CreateWidgets) Action(a *migrations.TableActions) { a.CreateTable() }
func (V0001_CreateWidgets) Down(a *migrations.TableActions)   { a.DropTable() }

type V0001 struct{}

func (V0001) Compose(v *migrations.VersionBuilder) { v.Apply(V0001_CreateWidgets{}) }

type V0002_AddNotes struct {
	migrations.TableMigration[liveWidget]
}

func (V0002_AddNotes) Action(a *migrations.TableActions) { a.AddColumn("notes", "nvarchar(200)") }
func (V0002_AddNotes) Down(a *migrations.TableActions)   { a.RemoveColumn("notes") }

type V0002 struct{}

func (V0002) Compose(v *migrations.VersionBuilder) { v.Apply(V0002_AddNotes{}) }

type V0003_RenameNotes struct {
	migrations.TableMigration[liveWidget]
}

func (V0003_RenameNotes) Action(a *migrations.TableActions) { a.RenameColumn("notes", "remarks") }
func (V0003_RenameNotes) Down(a *migrations.TableActions)   { a.RenameColumn("remarks", "notes") }

type V0003 struct{}

func (V0003) Compose(v *migrations.VersionBuilder) { v.Apply(V0003_RenameNotes{}) }

func widgetColumns(t *testing.T, pool *sql.DB) []string {
	t.Helper()
	ctx := context.Background()
	conn, err := pool.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	columns, err := migrations.ReadLiveColumns(ctx, conn, sqlserver.New(), "live_widgets")
	if err != nil {
		t.Fatal(err)
	}
	var found []string
	for _, column := range columns {
		found = append(found, column.Name)
	}
	return found
}

func recordedVersions(t *testing.T, pool *sql.DB) int {
	t.Helper()
	var count int
	if err := pool.QueryRowContext(context.Background(), "select count(*) from schema_version").Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestLive_MigrationsUpRerunAndDownUnderTheRunLock(t *testing.T) {
	ctx := context.Background()
	connectionString := newLiveDatabase(t)
	pool := rawPool(t, connectionString)

	set, err := migrations.NewSet(V0001{}, V0002{}, V0003{})
	if err != nil {
		t.Fatal(err)
	}
	runner := migrations.NewRunner(pool, sqlserver.New(), metadata.NewLoader(nil), set, nil)

	if pending, err := runner.HasPending(ctx); err != nil || !pending {
		t.Fatalf("a fresh database has pending versions: %v, %v", pending, err)
	}
	applied, err := runner.Migrate(ctx, migrations.RunOptions{})
	if err != nil || applied != 3 {
		t.Fatalf("migrate applies all three versions: %d, %v", applied, err)
	}
	if got := widgetColumns(t, pool); len(got) != 3 || recordedVersions(t, pool) != 3 {
		t.Fatalf("after migrate: columns %v, %d schema_version rows", got, recordedVersions(t, pool))
	}
	var remarks int
	if err := pool.QueryRowContext(ctx, "select count([remarks]) from [live_widgets]").Scan(&remarks); err != nil {
		t.Fatalf("sp_rename made the column readable under its new name: %v", err)
	}

	again, err := runner.Migrate(ctx, migrations.RunOptions{})
	if err != nil || again != 0 {
		t.Fatalf("re-running applies nothing: %d, %v", again, err)
	}
	if pending, err := runner.HasPending(ctx); err != nil || pending {
		t.Fatalf("nothing pending after the run: %v, %v", pending, err)
	}

	reverted, err := runner.MigrateDown(ctx, 1, migrations.RunOptions{})
	if err != nil || reverted != 2 {
		t.Fatalf("down to V0001 reverts two versions through their Down overrides: %d, %v", reverted, err)
	}
	if got := widgetColumns(t, pool); len(got) != 2 || recordedVersions(t, pool) != 1 {
		t.Fatalf("after down to 1: columns %v, %d rows", got, recordedVersions(t, pool))
	}
	if _, err := runner.MigrateDown(ctx, 0, migrations.RunOptions{}); err != nil {
		t.Fatal(err)
	}
	if got := widgetColumns(t, pool); len(got) != 0 || recordedVersions(t, pool) != 0 {
		t.Fatalf("after down to 0 the table is gone: columns %v, %d rows", got, recordedVersions(t, pool))
	}
}

func TestLive_AFailedRunAppliesNothing(t *testing.T) {
	ctx := context.Background()
	connectionString := newLiveDatabase(t)
	pool := rawPool(t, connectionString)

	set, err := migrations.NewSet(
		migrations.NewSQLVersion(1, migrations.SQLVersionStep{
			ObjectName: "boom", Description: "create", Up: []string{"create table boom (id int not null)"},
		}),
		migrations.NewSQLVersion(2, migrations.SQLVersionStep{
			ObjectName: "boom", Description: "fail", Up: []string{"this is not sql"},
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	runner := migrations.NewRunner(pool, sqlserver.New(), metadata.NewLoader(nil), set, nil)
	if _, err := runner.Migrate(ctx, migrations.RunOptions{}); orm.CodeOf(err) != "MIG-021" {
		t.Fatalf("a failing statement is MIG-021, got %v", err)
	}

	var tables int
	if err := pool.QueryRowContext(ctx, "select count(*) from sys.tables where name in (N'boom', N'schema_version')").Scan(&tables); err != nil {
		t.Fatal(err)
	}
	if tables != 0 {
		t.Errorf("DDL is transactional: a failed run applies nothing, found %d tables", tables)
	}
}

func TestLive_TheRunLockIsHeldForTheLifeOfItsTransaction(t *testing.T) {
	ctx := context.Background()
	connectionString := newLiveDatabase(t)
	pool := rawPool(t, connectionString)

	holder, err := pool.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	tx, err := sqlserver.New().BeginMigrationRunLock(ctx, holder)
	if err != nil {
		t.Fatalf("the first run takes the lock: %v", err)
	}

	grantable := func() int {
		t.Helper()
		probe, err := pool.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer probe.Rollback()
		var granted int
		if err := probe.QueryRowContext(ctx,
			"select applock_test('public', N'simpleorm:migrations', 'Exclusive', 'Transaction')").Scan(&granted); err != nil {
			t.Fatal(err)
		}
		return granted
	}
	if grantable() != 0 {
		t.Error("another run cannot take the exclusive lock while the first transaction lives")
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if grantable() != 1 {
		t.Error("the lock dies with its transaction: it can never leak past a rollback")
	}
}

// --- VAL-020 on markerless storage ----------------------------------------------------------------------------------------------------------------

// liveStamp reads a datetime2 column into a time.Time: type-compatible, but
// the stored value carries no UTC marker, so the read refuses (VAL-020).
type liveStamp struct {
	ID int64     `orm:"column,key,generated"`
	At time.Time `orm:"column"`
}

func (liveStamp) Entity() orm.EntityDef { return orm.EntityDef{Source: orm.Table("live_stamps")} }

func TestLive_MarkerlessDatetime2RefusesVAL020(t *testing.T) {
	ctx := context.Background()
	connectionString := newLiveDatabase(t)
	pool := rawPool(t, connectionString)
	for _, statement := range []string{
		"create table [live_stamps] ([id] bigint identity(1,1) primary key, [at] datetime2 not null)",
		"insert into [live_stamps] ([at]) values ('2026-08-28T09:30:00')",
	} {
		if _, err := pool.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}

	db := openLive(t, connectionString, orm.Options{})
	if _, err := orm.QueryAll[liveStamp](ctx, db); orm.CodeOf(err) != "VAL-020" {
		t.Fatalf("a datetime2 column read into a temporal property is VAL-020, got %v", err)
	}
}

// --- driver facts the dialect relies on ---------------------------------------------------------------------------------------------------------------

func TestLive_DriverReportsTheTypeNamesTheDialectKeysOn(t *testing.T) {
	ctx := context.Background()
	connectionString := newLiveDatabase(t)
	pool := rawPool(t, connectionString)

	rows, err := pool.QueryContext(ctx, "select cast(null as datetime), cast(null as datetime2), cast(null as smalldatetime), "+
		"cast(null as datetimeoffset), cast(null as date), cast(null as time), cast(1 as bigint), cast(1.5 as decimal(18,2)), "+
		"cast(1 as bit), cast(newid() as uniqueidentifier), N'x', cast(1 as varbinary(8))")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	types, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"DATETIME", "DATETIME2", "SMALLDATETIME", "DATETIMEOFFSET", "DATE", "TIME", "BIGINT", "DECIMAL",
		"BIT", "UNIQUEIDENTIFIER", "NVARCHAR", "VARBINARY",
	}
	dialect := sqlserver.New()
	for i, columnType := range types {
		if got := columnType.DatabaseTypeName(); got != want[i] {
			t.Errorf("column %d: DatabaseTypeName %q, want %q", i, got, want[i])
		}
		markerless := i <= 2
		if dialect.IsMarkerlessTemporal(columnType.DatabaseTypeName()) != markerless {
			t.Errorf("column %d (%s): IsMarkerlessTemporal should be %v", i, want[i], markerless)
		}
	}

	if !rows.Next() {
		t.Fatal("expected one row")
	}
	var cells [12]any
	targets := make([]any, len(cells))
	for i := range cells {
		targets[i] = &cells[i]
	}
	if err := rows.Scan(targets...); err != nil {
		t.Fatal(err)
	}
	if text, ok := cells[7].([]byte); !ok || string(text) != "1.50" {
		t.Errorf("decimal reads as its ASCII digits: %T %v", cells[7], cells[7])
	}
	if raw, ok := cells[9].([]byte); !ok || len(raw) != 16 {
		t.Errorf("uniqueidentifier reads as 16 bytes: %T %v", cells[9], cells[9])
	}
}

func TestLive_DriverBindsGoValuesAsTheTypesTheDialectAssumes(t *testing.T) {
	ctx := context.Background()
	connectionString := newLiveDatabase(t)
	pool := rawPool(t, connectionString)

	var textType, timeType, intType, boolType, floatType, bytesType string
	err := pool.QueryRowContext(ctx,
		"select cast(sql_variant_property(@a, 'BaseType') as nvarchar(40)), cast(sql_variant_property(@b, 'BaseType') as nvarchar(40)), "+
			"cast(sql_variant_property(@c, 'BaseType') as nvarchar(40)), cast(sql_variant_property(@d, 'BaseType') as nvarchar(40)), "+
			"cast(sql_variant_property(@e, 'BaseType') as nvarchar(40)), cast(sql_variant_property(@f, 'BaseType') as nvarchar(40))",
		sql.Named("a", "2026-08-28T09:30:00.0000000Z"), sql.Named("b", time.Date(2026, 8, 28, 9, 30, 0, 0, time.UTC)),
		sql.Named("c", int64(7)), sql.Named("d", true), sql.Named("e", 2.5), sql.Named("f", []byte{1, 2}),
	).Scan(&textType, &timeType, &intType, &boolType, &floatType, &bytesType)
	if err != nil {
		t.Fatal(err)
	}
	got := []string{textType, timeType, intType, boolType, floatType, bytesType}
	want := []string{"nvarchar", "datetimeoffset", "bigint", "bit", "float", "varbinary"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parameter types: got %v, want %v", got, want)
	}

	// A string parameter converts to datetimeoffset implicitly, and a datetimeoffset value keeps its instant.
	var instant time.Time
	if err := pool.QueryRowContext(ctx, "select cast(@a as datetimeoffset)", sql.Named("a", "2026-08-28T09:30:00.0000000Z")).Scan(&instant); err != nil {
		t.Fatal(err)
	}
	if !instant.Equal(time.Date(2026, 8, 28, 9, 30, 0, 0, time.UTC)) {
		t.Errorf("ISO-8601 text with a Z converts to the same instant: %v", instant)
	}
}

// --- introspection and describing statements ----------------------------------------------------------------------------------------------------------------------

func TestLive_IntrospectionQueriesReportTheSpecifiedShapes(t *testing.T) {
	ctx := context.Background()
	db, connectionString := newSampleSession(t, orm.Options{})
	_ = db
	pool := rawPool(t, connectionString)
	conn, err := pool.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	dialect := sqlserver.New()

	columns, err := migrations.ReadLiveColumns(ctx, conn, dialect, "users")
	if err != nil {
		t.Fatal(err)
	}
	for column, want := range map[string]struct {
		declared string
		notNull  bool
		key      bool
	}{
		"id":           {"bigint", true, true},
		"name":         {"nvarchar(max)", true, false},
		"email":        {"nvarchar(450)", true, false}, // listed by a unique index: the indexable length
		"display_name": {"nvarchar(max)", false, false},
		"created_at":   {"datetimeoffset", true, false},
		"updated_at":   {"datetimeoffset", false, false},
	} {
		got, ok := columns[column]
		if !ok {
			t.Errorf("users.%s missing from %v", column, columns)
			continue
		}
		if got.DeclaredType != want.declared || got.NotNull != want.notNull || got.PrimaryKey != want.key {
			t.Errorf("users.%s: %+v, want %+v", column, got, want)
		}
	}
	amount, err := migrations.ReadLiveColumns(ctx, conn, dialect, "transactions")
	if err != nil || amount["amount"].DeclaredType != "decimal(38,9)" {
		t.Errorf("decimal reconstructs (precision,scale): %+v, %v", amount["amount"], err)
	}
	if missing, err := migrations.ReadLiveColumns(ctx, conn, dialect, "no_such_table"); err != nil || len(missing) != 0 {
		t.Errorf("an absent relation is an empty result: %v, %v", missing, err)
	}

	indexes, err := migrations.ReadLiveIndexes(ctx, conn, dialect, "transactions")
	if err != nil {
		t.Fatal(err)
	}
	var composite *migrations.TableIndex
	for i := range indexes {
		if len(indexes[i].Columns) == 2 {
			composite = &indexes[i]
		}
	}
	if composite == nil || composite.Columns[0].ColumnName != "status" || composite.Columns[1].ColumnName != "created_at" || !composite.Columns[1].Descending {
		t.Errorf("explicit indexes report columns in order with direction: %+v", indexes)
	}

	var definition string
	if err := conn.QueryRowContext(ctx, dialect.ViewDefinitionSQL(), sql.Named("relation", "user_transaction_totals")).Scan(&definition); err != nil {
		t.Fatalf("view definition: %v", err)
	}
	if !strings.Contains(strings.ToLower(definition), "user_transaction_totals") {
		t.Errorf("the stored definition names the view: %q", definition)
	}
}

func TestLive_DescribeStatementWithoutExecutingIt(t *testing.T) {
	ctx := context.Background()
	db, connectionString := newSampleSession(t, orm.Options{})
	_ = db
	pool := rawPool(t, connectionString)
	conn, err := pool.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	dialect := sqlserver.New()

	columns, err := dialect.DescribeStatement(ctx, conn, "select id, name as who, count(*) over () as total from users where id = @id or email = @Email")
	if err != nil {
		t.Fatal(err)
	}
	if len(columns) != 3 {
		t.Fatalf("three result columns: %+v", columns)
	}
	if columns[0].Name != "id" || columns[0].Table != "users" || columns[0].OriginColumn != "id" {
		t.Errorf("a plain column traces to its base table and column: %+v", columns[0])
	}
	if columns[1].Name != "who" || columns[1].OriginColumn != "name" {
		t.Errorf("an aliased column keeps its origin: %+v", columns[1])
	}
	if columns[2].Name != "total" || columns[2].Table != "" || columns[2].OriginColumn != "" {
		t.Errorf("an expression column has no origin: %+v", columns[2])
	}

	// Describing never executes: a statement that would fail or write is only analysed.
	if _, err := dialect.DescribeStatement(ctx, conn, "delete from users where id = @id"); err != nil {
		t.Errorf("a delete describes as no result set: %v", err)
	}
	if _, err := dialect.DescribeStatement(ctx, conn, "select * from no_such_table"); err == nil {
		t.Error("SQL the server cannot compile fails to describe (VAL-001 upstream)")
	}
}

type displayNameRow struct{ DisplayName string }
type nameAsNumberRow struct{ Name int64 }

func TestLive_SchemaGuardOverASmallRegistry(t *testing.T) {
	ctx := context.Background()
	db, _ := newSampleSession(t, orm.Options{})

	clean := orm.ValidateTypes(ctx, db,
		[]orm.Entry{sample.UsersByEmail, sample.UsersByIDs, sample.TransactionsByUser, sample.TransactionsByStatus, sample.MarkTransactionStatus},
		[]reflect.Type{
			reflect.TypeFor[sample.User](), reflect.TypeFor[sample.Role](), reflect.TypeFor[sample.UserRole](),
			reflect.TypeFor[sample.Transaction](), reflect.TypeFor[sample.TransactionDetail](),
			reflect.TypeFor[sample.UserTransactionTotal](),
		})
	if clean != nil {
		t.Fatalf("the sample registry validates cleanly against the metadata-created schema: %v", clean)
	}

	broken := orm.ValidateTypes(ctx, db, []orm.Entry{
		orm.Inline[orm.EmptyArgs, displayNameRow]("select display_name from users"),
		orm.Inline[orm.EmptyArgs, nameAsNumberRow]("select name from users"),
		orm.InlineCommand[orm.EmptyArgs]("update no_such_table_at_all set x = 1"),
		orm.InlineCommand[sample.UsersByEmailArgs]("select 1 from users where 1 = @Bogus"),
	}, []reflect.Type{reflect.TypeFor[liveStamp]()})
	for _, code := range []string{"VAL-010", "VAL-011", "VAL-001", "PRM-001", "PRM-002", "VAL-012"} {
		if !orm.HasCode(broken, code) {
			t.Errorf("expected %s in the report, got: %v", code, broken)
		}
	}
}

// TestLive_BytesRoundTrip writes and matches varbinary values (ADR-0040 add.1), ending with a NULL
// into varbinary(max) — bound as a typed []byte(nil), see TestLive_ANullBytesWriteIsATypedNull.
func TestLive_BytesRoundTrip(t *testing.T) {
	db, _ := newSampleSession(t, orm.Options{})
	testsupport.BytesRoundTrip(t, db)
}

// TestLive_ANullBytesWriteIsATypedNull verifies the typed-null ruling (ADR-0040 add.1, spec/dialects.md
// "Typed nulls from the metadata token") the day an engine exists: an untyped nil would reach SQL Server
// as an nvarchar(1) NULL, which it refuses to convert into varbinary (error 257); the binder sends a
// []byte(nil), which go-mssqldb announces as varbinary. Insert and update both write the NULL.
func TestLive_ANullBytesWriteIsATypedNull(t *testing.T) {
	ctx := context.Background()
	db, _ := newSampleSession(t, orm.Options{})
	user := testsupport.InsertUser(t, db, "Ada", "ada@example.com")
	profile := &sample.UserProfile{UserID: user.ID}
	profile.CreatedAtUtc = testsupport.SeedTime
	if err := orm.Insert(ctx, db, profile); err != nil {
		t.Fatalf("insert with a NULL avatar: %v", err)
	}
	avatar := []byte{0x01}
	profile.Avatar = &avatar
	if err := orm.UpdateOnly(ctx, db, profile, "Avatar"); err != nil {
		t.Fatal(err)
	}
	profile.Avatar = nil
	if err := orm.UpdateOnly(ctx, db, profile, "Avatar"); err != nil {
		t.Fatalf("update to a NULL avatar: %v", err)
	}
	read, err := orm.Get[sample.UserProfile](ctx, db, profile.ID)
	if err != nil || read.Avatar != nil {
		t.Fatalf("expected a NULL avatar back, got %v, %v", read, err)
	}
}
