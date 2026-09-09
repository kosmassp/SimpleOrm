package orm_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/kosmassp/SimpleOrm/go/orm"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/testsupport"
	"github.com/kosmassp/SimpleOrm/go/orm/sample"
	"github.com/kosmassp/SimpleOrm/go/orm/sqlite"
)

// This file covers what the conformance/load-cases/*.json format cannot
// express (spec/loading.md: "REL-002/REL-003 need drifted data or shape-broken
// metadata the case format cannot seed, so each implementation pins them in
// its own tests") plus the Go-shape rules of CODING-STANDARD §10 (nil vs.
// empty slice, shared instances, dead links, reload, null FK exclusion, and
// the chunk boundary).

// --- REL-002: a drifted duplicate one-to-one row ---------------------------

// oneOneOwner/oneOneTarget are a test-local one-to-one pair deliberately
// without the unique index a real 1:1 needs (spec/loading.md: "the unique
// index on the target FK is what makes a 1:1" — this fixture is what "drift"
// looks like without one), so two target rows can share an owner's FK.
type oneOneOwner struct {
	ID     int64         `orm:"column,key,generated"`
	Name   string        `orm:"column"`
	Target *oneOneTarget `orm:"one_to_one=OwnerID"`
}

func (oneOneOwner) Entity() orm.EntityDef { return orm.EntityDef{Source: orm.Table("rel002_owners")} }

type oneOneTarget struct {
	ID      int64  `orm:"column,key,generated"`
	OwnerID int64  `orm:"column"`
	Note    string `orm:"column"`
}

func (oneOneTarget) Entity() orm.EntityDef { return orm.EntityDef{Source: orm.Table("rel002_targets")} }

func TestLoadEach_OneToOne_DriftedDuplicateRow_IsREL002(t *testing.T) {
	ctx := context.Background()
	db, err := orm.Open(ctx, testsupport.TempDatabase(t), orm.Options{Dialect: sqlite.New()})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if err := orm.CreateTable[oneOneOwner](ctx, db); err != nil {
		t.Fatalf("create owners: %v", err)
	}
	if err := orm.CreateTable[oneOneTarget](ctx, db); err != nil {
		t.Fatalf("create targets: %v", err)
	}

	owner := &oneOneOwner{Name: "Owner"}
	if err := orm.Insert(ctx, db, owner); err != nil {
		t.Fatalf("insert owner: %v", err)
	}
	for _, note := range []string{"first", "second"} {
		target := &oneOneTarget{OwnerID: owner.ID, Note: note}
		if err := orm.Insert(ctx, db, target); err != nil {
			t.Fatalf("insert target: %v", err)
		}
	}

	err = orm.LoadEach(ctx, db, []*oneOneOwner{owner}, "Target")
	if orm.CodeOf(err) != "REL-002" {
		t.Fatalf("expected REL-002, got %v", err)
	}
}

// --- REL-003: a shape-broken declaration ------------------------------------

// shapeOwner declares a one-to-many FK ("OwnerID") that the loader could only
// check exists as a Go field on the target at declaration time (MAP-021); it
// is deliberately tagged `ignore` there, so it is not actually a mapped
// column — the shape loading alone can catch (spec/loading.md "Shape errors").
type shapeOwner struct {
	ID    int64          `orm:"column,key,generated"`
	Items []*shapeTarget `orm:"one_to_many=OwnerID"`
}

func (shapeOwner) Entity() orm.EntityDef { return orm.EntityDef{Source: orm.Table("rel003_owners")} }

type shapeTarget struct {
	ID      int64 `orm:"column,key,generated"`
	OwnerID int64 `orm:"ignore"`
}

func (shapeTarget) Entity() orm.EntityDef { return orm.EntityDef{Source: orm.Table("rel003_targets")} }

func TestLoadEach_ShapeBrokenForeignKey_IsREL003(t *testing.T) {
	ctx := context.Background()
	db, err := orm.Open(ctx, testsupport.TempDatabase(t), orm.Options{Dialect: sqlite.New()})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	// The declaration itself loads fine (MAP-021 only checks the Go field
	// exists); no table is needed — resolveProperties refuses before any SQL
	// runs, purely from the target's assembled EntityMap.
	owner := &shapeOwner{ID: 1}
	err = orm.LoadEach(ctx, db, []*shapeOwner{owner}, "Items")
	if orm.CodeOf(err) != "REL-003" {
		t.Fatalf("expected REL-003, got %v", err)
	}
}

// keylessTarget/ownerOfKeylessTarget mirror eager_join_test.go's
// joinKeylessTarget/joinOwnerOfKeylessTarget: a many-to-one navigation whose
// target declares no key (only reachable through a view — a table-backed
// entity must declare a key, MAP-019). spec/loading.md "Shape errors": "an
// arity mismatch against a key the target never declared" refuses REL-003 at
// load time in every mode — checkArity's want == 0 case, not just join
// mode's own explicit guard (db_eager_join.go's buildJoins).
type keylessTarget struct {
	ID   int64  `orm:"column"` // no key tag: deliberately keyless
	Name string `orm:"column"`
}

func (keylessTarget) Entity() orm.EntityDef {
	return orm.EntityDef{Source: orm.View("rel003_keyless_targets", "select 1 as id, 'x' as name")}
}

type ownerOfKeylessTarget struct {
	ID       int64          `orm:"column,key,generated"`
	TargetID int64          `orm:"column"`
	Target   *keylessTarget `orm:"many_to_one=TargetID"`
}

func (ownerOfKeylessTarget) Entity() orm.EntityDef {
	return orm.EntityDef{Source: orm.Table("rel003_owners_of_keyless")}
}

func TestLoadEach_KeylessTarget_IsREL003(t *testing.T) {
	ctx := context.Background()
	db, err := orm.Open(ctx, testsupport.TempDatabase(t), orm.Options{Dialect: sqlite.New()})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	// The refusal fires before any SQL runs (checkArity, ahead of
	// correlate/queryMembership) — no table needs to exist, exactly like
	// join mode's own TestJoin_KeylessTarget_IsREL003.
	owner := &ownerOfKeylessTarget{ID: 1, TargetID: 7}
	err = orm.LoadEach(ctx, db, []*ownerOfKeylessTarget{owner}, "Target")
	if orm.CodeOf(err) != "REL-003" {
		t.Fatalf("expected REL-003, got %v", err)
	}
}

// keylessOwnerOneToMany/keylessOwnerOneToOne/keylessOwnerManyToMany*: the
// mirror image of keylessTarget above — the *owner* declares no key (only
// reachable through a view, MAP-019). Declaration-time validation only checks
// this arity when the owner's key is already known (map_assembler.go's
// ownerKeyCount > 0 guard), deferring a keyless owner to runtime — exactly
// what join mode's fkOnTargetPairs/linkPairsToOwner already guard
// (db_eager_join.go). Before this fix, loadOneToOne/loadOneToMany/
// loadManyToMany had no equivalent guard: membershipCriteria indexed the
// owner's (zero-length) key tuple by the target/link's declared FK count and
// panicked instead of naming the shape problem (spec/loading.md "Shape
// errors": "an arity mismatch against a key the target never declared").
type keylessOwnerOneToMany struct {
	ID    int64                          `orm:"column"` // no key: deliberately keyless owner
	Items []*keylessOwnerOneToManyTarget `orm:"one_to_many=OwnerID"`
}

func (keylessOwnerOneToMany) Entity() orm.EntityDef {
	return orm.EntityDef{Source: orm.View("rel003_keyless_owners_one_to_many", "select 1 as id")}
}

type keylessOwnerOneToManyTarget struct {
	ID      int64 `orm:"column,key,generated"`
	OwnerID int64 `orm:"column"`
}

func (keylessOwnerOneToManyTarget) Entity() orm.EntityDef {
	return orm.EntityDef{Source: orm.Table("rel003_keyless_owner_one_to_many_targets")}
}

func TestLoadEach_OneToMany_KeylessOwner_IsREL003(t *testing.T) {
	ctx := context.Background()
	db, err := orm.Open(ctx, testsupport.TempDatabase(t), orm.Options{Dialect: sqlite.New()})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	owner := &keylessOwnerOneToMany{ID: 1}
	err = orm.LoadEach(ctx, db, []*keylessOwnerOneToMany{owner}, "Items")
	if orm.CodeOf(err) != "REL-003" {
		t.Fatalf("expected REL-003, got %v", err)
	}
}

type keylessOwnerOneToOne struct {
	ID     int64                       `orm:"column"` // no key: deliberately keyless owner
	Target *keylessOwnerOneToOneTarget `orm:"one_to_one=OwnerID"`
}

func (keylessOwnerOneToOne) Entity() orm.EntityDef {
	return orm.EntityDef{Source: orm.View("rel003_keyless_owners_one_to_one", "select 1 as id")}
}

type keylessOwnerOneToOneTarget struct {
	ID      int64 `orm:"column,key,generated"`
	OwnerID int64 `orm:"column"`
}

func (keylessOwnerOneToOneTarget) Entity() orm.EntityDef {
	return orm.EntityDef{Source: orm.Table("rel003_keyless_owner_one_to_one_targets")}
}

func TestLoadEach_OneToOne_KeylessOwner_IsREL003(t *testing.T) {
	ctx := context.Background()
	db, err := orm.Open(ctx, testsupport.TempDatabase(t), orm.Options{Dialect: sqlite.New()})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	owner := &keylessOwnerOneToOne{ID: 1}
	err = orm.LoadEach(ctx, db, []*keylessOwnerOneToOne{owner}, "Target")
	if orm.CodeOf(err) != "REL-003" {
		t.Fatalf("expected REL-003, got %v", err)
	}
}

type keylessOwnerManyToMany struct {
	ID    int64                           `orm:"column"` // no key: deliberately keyless owner
	Items []*keylessOwnerManyToManyTarget `orm:"many_to_many"`
}

func (keylessOwnerManyToMany) Entity() orm.EntityDef {
	return orm.EntityDef{
		Source:     orm.View("rel003_keyless_owners_many_to_many", "select 1 as id"),
		ManyToMany: []orm.ManyToManyDef{orm.ManyToMany[keylessOwnerManyToManyLink]("Items")},
	}
}

type keylessOwnerManyToManyTarget struct {
	ID int64 `orm:"column,key,generated"`
}

func (keylessOwnerManyToManyTarget) Entity() orm.EntityDef {
	return orm.EntityDef{Source: orm.Table("rel003_keyless_owner_mtm_targets")}
}

type keylessOwnerManyToManyLink struct {
	OwnerID  int64 `orm:"column,key"`
	TargetID int64 `orm:"column,key"`
}

func (keylessOwnerManyToManyLink) Entity() orm.EntityDef {
	return orm.EntityDef{
		Source: orm.Table("rel003_keyless_owner_mtm_links"),
		ForeignKeys: []orm.ForeignKeyDef{
			orm.ForeignKey[keylessOwnerManyToMany]("OwnerID"),
			orm.ForeignKey[keylessOwnerManyToManyTarget]("TargetID"),
		},
	}
}

func TestLoadEach_ManyToMany_KeylessOwner_IsREL003(t *testing.T) {
	ctx := context.Background()
	db, err := orm.Open(ctx, testsupport.TempDatabase(t), orm.Options{Dialect: sqlite.New()})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	// The owner-side arity check fires before the link/target queries run —
	// no table needs to exist.
	owner := &keylessOwnerManyToMany{ID: 1}
	err = orm.LoadEach(ctx, db, []*keylessOwnerManyToMany{owner}, "Items")
	if orm.CodeOf(err) != "REL-003" {
		t.Fatalf("expected REL-003, got %v", err)
	}
}

// --- nil-vs-empty slice, shared instances, dead links, reload --------------

func newSeededUser(t *testing.T, ctx context.Context, db *orm.Db, name, email string) *sample.User {
	t.Helper()
	return testsupport.InsertUser(t, db, name, email)
}

func newSeededTransaction(t *testing.T, ctx context.Context, db *orm.Db, userID int64) *sample.Transaction {
	t.Helper()
	tx := &sample.Transaction{UserID: userID, Status: sample.Pending, Amount: orm.MustDecimal("1.00")}
	tx.CreatedAtUtc = testsupport.SeedTime
	if err := orm.Insert(ctx, db, tx); err != nil {
		t.Fatalf("insert transaction: %v", err)
	}
	return tx
}

func TestLoadEach_OneToMany_NeverLoadedIsNilLoadedEmptyIsNonNil(t *testing.T) {
	ctx := context.Background()
	db := testsupport.OpenSample(t)
	user := newSeededUser(t, ctx, db, "Ada", "ada@example.com")

	if user.Transactions != nil {
		t.Fatalf("a never-loaded collection navigation must start nil, got %#v", user.Transactions)
	}

	if err := orm.LoadEach(ctx, db, []*sample.User{user}, "Transactions"); err != nil {
		t.Fatalf("LoadEach: %v", err)
	}
	if user.Transactions == nil {
		t.Fatal("a loaded-but-empty collection must be an empty non-nil slice, got nil")
	}
	if len(user.Transactions) != 0 {
		t.Fatalf("expected zero transactions, got %d", len(user.Transactions))
	}
}

func TestLoadEach_ManyToOne_OwnersSharingATargetShareTheInstance(t *testing.T) {
	ctx := context.Background()
	db := testsupport.OpenSample(t)
	user := newSeededUser(t, ctx, db, "Ada", "ada@example.com")
	tx1 := newSeededTransaction(t, ctx, db, user.ID)
	tx2 := newSeededTransaction(t, ctx, db, user.ID)

	if err := orm.LoadEach(ctx, db, []*sample.Transaction{tx1, tx2}, "User"); err != nil {
		t.Fatalf("LoadEach: %v", err)
	}
	if tx1.User == nil || tx2.User == nil {
		t.Fatal("expected both transactions' User to load")
	}
	if tx1.User != tx2.User {
		t.Fatal("owners sharing a many-to-one target must share the same instance, got distinct pointers")
	}
}

func TestLoad_ManyToOne_DeadLinkLoadsAsNilNeverAnError(t *testing.T) {
	ctx := context.Background()
	db := testsupport.OpenSample(t)
	user := newSeededUser(t, ctx, db, "Ada", "ada@example.com")
	tx := newSeededTransaction(t, ctx, db, user.ID)

	// Point the FK at a row that does not exist — "there is no real model to
	// go there" (spec/loading.md); a raw update keeps the entity's own User
	// navigation untouched (nil) so Insert's FK/navigation consistency check
	// never sees it.
	if _, err := orm.Execute(ctx, db, updateTransactionUserID, updateTransactionUserIDArgs{ID: tx.ID, UserID: 999999}); err != nil {
		t.Fatalf("point FK at a dead link: %v", err)
	}
	tx.UserID = 999999

	if err := orm.Load(ctx, db, tx, "User"); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if tx.User != nil {
		t.Fatalf("a dead link must load as nil, got %+v", tx.User)
	}
}

type updateTransactionUserIDArgs struct {
	ID     int64
	UserID int64
}

var updateTransactionUserID = orm.InlineCommand[updateTransactionUserIDArgs](
	"update transactions set user_id = @UserID where id = @ID")

func TestLoadEach_ReloadOverwritesStaleState(t *testing.T) {
	ctx := context.Background()
	db := testsupport.OpenSample(t)
	user := newSeededUser(t, ctx, db, "Ada", "ada@example.com")
	real := newSeededTransaction(t, ctx, db, user.ID)

	// A stale placeholder that must not survive the reload — a reload is a
	// reload, never a merge (spec/loading.md).
	user.Transactions = []*sample.Transaction{{ID: -1}}

	if err := orm.LoadEach(ctx, db, []*sample.User{user}, "Transactions"); err != nil {
		t.Fatalf("LoadEach: %v", err)
	}
	if len(user.Transactions) != 1 || user.Transactions[0].ID != real.ID {
		t.Fatalf("expected the reload to overwrite the stale slice with [%d], got %#v", real.ID, user.Transactions)
	}

	// A stale placeholder on a singular navigation must be overwritten too.
	stale := &sample.User{ID: -1}
	real.User = stale
	if err := orm.Load(ctx, db, real, "User"); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if real.User == stale || real.User.ID != user.ID {
		t.Fatalf("expected the reload to overwrite the stale User with %d, got %#v", user.ID, real.User)
	}
}

// --- null FK part excluded --------------------------------------------------

// node is a minimal self-referencing many-to-one fixture with a nullable FK,
// which no sample entity has: it lets a case distinguish "null FK part" from
// "dead link" cleanly (spec/loading.md: "An owner whose key contains a null
// part is excluded from querying" — the many-to-one FK is its symmetric case).
type node struct {
	ID       int64  `orm:"column,key,generated"`
	Name     string `orm:"column"`
	ParentID *int64 `orm:"column"`
	Parent   *node  `orm:"many_to_one=ParentID"`
}

func (node) Entity() orm.EntityDef { return orm.EntityDef{Source: orm.Table("rel_nodes")} }

func TestLoad_ManyToOne_NullForeignKeyPartExcludedFromQuerying(t *testing.T) {
	ctx := context.Background()
	db, err := orm.Open(ctx, testsupport.TempDatabase(t), orm.Options{Dialect: sqlite.New()})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := orm.CreateTable[node](ctx, db); err != nil {
		t.Fatalf("create table: %v", err)
	}

	root := &node{Name: "root"}
	if err := orm.Insert(ctx, db, root); err != nil {
		t.Fatalf("insert root: %v", err)
	}

	if err := orm.Load(ctx, db, root, "Parent"); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if root.Parent != nil {
		t.Fatalf("a null FK part must keep the navigation nil, got %+v", root.Parent)
	}
}

// --- chunking past the parameter budget -------------------------------------

// countingDialect wraps a Dialect and counts SelectSQL calls — the only
// externally observable trace of how many queries loading actually issued.
type countingDialect struct {
	orm.Dialect
	calls *int
}

func (d countingDialect) SelectSQL(ast *orm.SelectAst, bind orm.BindCriteriaParameter) (string, error) {
	*d.calls++
	return d.Dialect.SelectSQL(ast, bind)
}

func TestLoadEach_ChunksPastLoadChunkSize(t *testing.T) {
	ctx := context.Background()
	var calls int
	db, err := orm.Open(ctx, testsupport.TempDatabase(t), orm.Options{Dialect: countingDialect{Dialect: sqlite.New(), calls: &calls}})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if err := orm.CreateTable[sample.User](ctx, db); err != nil {
		t.Fatalf("create users: %v", err)
	}
	if err := orm.CreateTable[sample.Transaction](ctx, db); err != nil {
		t.Fatalf("create transactions: %v", err)
	}

	const ownerCount = core.LoadChunkSize + 1 // proves the boundary actually splits queries, not just "some chunking exists"
	users := make([]*sample.User, ownerCount)
	for i := 0; i < ownerCount; i++ {
		user := &sample.User{Name: fmt.Sprintf("user-%d", i), Email: fmt.Sprintf("user-%d@example.com", i)}
		user.CreatedAtUtc = testsupport.SeedTime
		if err := orm.Insert(ctx, db, user); err != nil {
			t.Fatalf("insert user %d: %v", i, err)
		}
		tx := &sample.Transaction{UserID: user.ID, Status: sample.Pending, Amount: orm.MustDecimal("1.00")}
		tx.CreatedAtUtc = testsupport.SeedTime
		if err := orm.Insert(ctx, db, tx); err != nil {
			t.Fatalf("insert transaction %d: %v", i, err)
		}
		users[i] = user
	}

	calls = 0
	if err := orm.LoadEach(ctx, db, users, "Transactions"); err != nil {
		t.Fatalf("LoadEach: %v", err)
	}
	if calls != 2 {
		t.Fatalf("expected 2 queries for %d distinct owners chunked at %d, got %d", ownerCount, core.LoadChunkSize, calls)
	}
	for _, u := range users {
		if len(u.Transactions) != 1 {
			t.Fatalf("user %d: expected exactly 1 transaction, got %d", u.ID, len(u.Transactions))
		}
	}
}
