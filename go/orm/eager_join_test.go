package orm_test

import (
	"context"
	"testing"

	"github.com/kosmassp/SimpleOrm/go/orm"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/testsupport"
	"github.com/kosmassp/SimpleOrm/go/orm/sample"
	"github.com/kosmassp/SimpleOrm/go/orm/sqlite"
)

// This file covers join-mode eager loading (spec/loading.md "Join",
// ADR-0022 add.1): Include(...).Fetch(orm.FetchJoin) against the sample
// fixture, plus the refusals and shape rules the conformance/load-cases
// format cannot express for this mode (REL-002/003/005/006 need drifted data
// or shape-broken metadata, exactly like explicit/batch loading's own tests
// in loading_test.go).

// --- test-local fixture helpers (CODING-STANDARD §7: fixtures stay in the
// test file, never in sample) ------------------------------------------------

func insertRole(t *testing.T, ctx context.Context, db *orm.Db, name string) *sample.Role {
	t.Helper()
	role := &sample.Role{Name: name}
	role.CreatedAtUtc = testsupport.SeedTime
	if err := orm.Insert(ctx, db, role); err != nil {
		t.Fatalf("insert role: %v", err)
	}
	return role
}

func assignRole(t *testing.T, ctx context.Context, db *orm.Db, userID, roleID int64) {
	t.Helper()
	link := &sample.UserRole{UserID: userID, RoleID: roleID}
	link.CreatedAtUtc = testsupport.SeedTime
	if err := orm.Insert(ctx, db, link); err != nil {
		t.Fatalf("insert user_role: %v", err)
	}
}

func insertJoinTransaction(t *testing.T, ctx context.Context, db *orm.Db, userID int64, amount string) *sample.Transaction {
	t.Helper()
	tx := &sample.Transaction{UserID: userID, Status: sample.Pending, Amount: orm.MustDecimal(amount)}
	tx.CreatedAtUtc = testsupport.SeedTime
	if err := orm.Insert(ctx, db, tx); err != nil {
		t.Fatalf("insert transaction: %v", err)
	}
	return tx
}

func insertProfile(t *testing.T, ctx context.Context, db *orm.Db, userID int64, bio string) *sample.UserProfile {
	t.Helper()
	profile := &sample.UserProfile{UserID: userID, Bio: &bio}
	profile.CreatedAtUtc = testsupport.SeedTime
	if err := orm.Insert(ctx, db, profile); err != nil {
		t.Fatalf("insert profile: %v", err)
	}
	return profile
}

// capturingDialect wraps a Dialect and records the exact SQL text SelectSQL
// last rendered — the same text listWithJoins runs, so it is the one
// externally observable trace of the join rendering (mirrors loading_test.go's
// countingDialect).
type capturingDialect struct {
	orm.Dialect
	last *string
}

func (d capturingDialect) SelectSQL(ast *orm.SelectAst, bind orm.BindCriteriaParameter) (string, error) {
	text, err := d.Dialect.SelectSQL(ast, bind)
	if err == nil {
		*d.last = text
	}
	return text, err
}

func openCapturing(t *testing.T) (*orm.Db, *string) {
	t.Helper()
	var last string
	ctx := context.Background()
	db, err := orm.Open(ctx, testsupport.TempDatabase(t), orm.Options{Dialect: capturingDialect{Dialect: sqlite.New(), last: &last}})
	if err != nil {
		t.Fatalf("open: %v", err)
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
	return db, &last
}

// --- one navigation of each kind, through Join mode -------------------------

func TestJoin_ManyToOne_TransactionUser(t *testing.T) {
	ctx := context.Background()
	db := testsupport.OpenSample(t)
	ada := testsupport.InsertUser(t, db, "Ada", "ada@example.com")
	tx := insertJoinTransaction(t, ctx, db, ada.ID, "10.00")

	loaded, err := orm.From[sample.Transaction](db).
		Where(orm.Eq("ID", tx.ID)).
		Include("User").Fetch(orm.FetchJoin).List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected 1 transaction, got %d", len(loaded))
	}
	if loaded[0].User == nil || loaded[0].User.ID != ada.ID || loaded[0].User.Name != "Ada" {
		t.Fatalf("expected User to load as Ada, got %+v", loaded[0].User)
	}
}

func TestJoin_OneToMany_UserTransactions_ExactSQL(t *testing.T) {
	ctx := context.Background()
	db, lastSQL := openCapturing(t)
	ada := testsupport.InsertUser(t, db, "Ada", "ada@example.com")
	tx1 := insertJoinTransaction(t, ctx, db, ada.ID, "10.00")
	tx2 := insertJoinTransaction(t, ctx, db, ada.ID, "5.00")

	loaded, err := orm.From[sample.User](db).
		Where(orm.Eq("ID", ada.ID)).
		Include("Transactions").Fetch(orm.FetchJoin).List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	// The where clause qualifies the root column with "t." — pinned in full so
	// the alias convention (root "t", projected join "j0") is verified
	// byte-for-byte, not just by the loaded values (spec/query-ast.md).
	const expected = "select t.id as t_id, t.name as t_name, t.email as t_email, t.display_name as t_display_name, " +
		"t.created_at as t_created_at, t.updated_at as t_updated_at, " +
		"j0.id as j0_id, j0.user_id as j0_user_id, j0.status as j0_status, j0.amount as j0_amount, " +
		"j0.version as j0_version, j0.note as j0_note, j0.created_at as j0_created_at, j0.updated_at as j0_updated_at " +
		"from users t left join transactions j0 on j0.user_id = t.id where t.id = @c0"
	if *lastSQL != expected {
		t.Fatalf("rendered SQL mismatch:\n got: %s\nwant: %s", *lastSQL, expected)
	}

	if len(loaded) != 1 {
		t.Fatalf("expected 1 user, got %d", len(loaded))
	}
	if loaded[0].Transactions == nil {
		t.Fatal("expected a loaded (non-nil) Transactions slice")
	}
	if len(loaded[0].Transactions) != 2 {
		t.Fatalf("expected 2 transactions, got %d", len(loaded[0].Transactions))
	}
	// Ordered by target key value-wise.
	if loaded[0].Transactions[0].ID != tx1.ID || loaded[0].Transactions[1].ID != tx2.ID {
		t.Fatalf("expected transactions ordered [%d,%d], got [%d,%d]",
			tx1.ID, tx2.ID, loaded[0].Transactions[0].ID, loaded[0].Transactions[1].ID)
	}
}

func TestJoin_OneToMany_NoMatchingRows_IsEmptyNonNilSlice(t *testing.T) {
	ctx := context.Background()
	db := testsupport.OpenSample(t)
	ada := testsupport.InsertUser(t, db, "Ada", "ada@example.com")

	loaded, err := orm.From[sample.User](db).
		Where(orm.Eq("ID", ada.ID)).
		Include("Transactions").Fetch(orm.FetchJoin).List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected 1 user, got %d", len(loaded))
	}
	if loaded[0].Transactions == nil {
		t.Fatal("an included collection with no matching rows must be an empty non-nil slice, got nil")
	}
	if len(loaded[0].Transactions) != 0 {
		t.Fatalf("expected zero transactions, got %d", len(loaded[0].Transactions))
	}
}

func TestJoin_OneToOne_UserProfile(t *testing.T) {
	ctx := context.Background()
	db := testsupport.OpenSample(t)
	ada := testsupport.InsertUser(t, db, "Ada", "ada@example.com")
	profile := insertProfile(t, ctx, db, ada.ID, "Mathematician")
	grace := testsupport.InsertUser(t, db, "Grace", "grace@example.com") // no profile: dead singular match

	loaded, err := orm.From[sample.User](db).
		Where(orm.In("ID", ada.ID, grace.ID)).
		Include("Profile").Fetch(orm.FetchJoin).List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(loaded) != 2 {
		t.Fatalf("expected 2 users, got %d", len(loaded))
	}
	for _, u := range loaded {
		switch u.ID {
		case ada.ID:
			if u.Profile == nil || u.Profile.ID != profile.ID {
				t.Fatalf("expected Ada's Profile to load, got %+v", u.Profile)
			}
		case grace.ID:
			if u.Profile != nil {
				t.Fatalf("expected Grace's Profile to stay nil (no matching row), got %+v", u.Profile)
			}
		}
	}
}

func TestJoin_ManyToMany_UserRoles(t *testing.T) {
	ctx := context.Background()
	db := testsupport.OpenSample(t)
	ada := testsupport.InsertUser(t, db, "Ada", "ada@example.com")
	admin := insertRole(t, ctx, db, "Admin")
	editor := insertRole(t, ctx, db, "Editor")
	assignRole(t, ctx, db, ada.ID, editor.ID)
	assignRole(t, ctx, db, ada.ID, admin.ID)

	loaded, err := orm.From[sample.User](db).
		Where(orm.Eq("ID", ada.ID)).
		Include("Roles").Fetch(orm.FetchJoin).List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected 1 user, got %d", len(loaded))
	}
	if len(loaded[0].Roles) != 2 {
		t.Fatalf("expected 2 roles, got %d", len(loaded[0].Roles))
	}
	// Ordered by target key value-wise (Admin's ID < Editor's ID: inserted first).
	if loaded[0].Roles[0].ID != admin.ID || loaded[0].Roles[1].ID != editor.ID {
		t.Fatalf("expected roles ordered [%d,%d], got [%d,%d]",
			admin.ID, editor.ID, loaded[0].Roles[0].ID, loaded[0].Roles[1].ID)
	}
}

// --- shared instances (§7.4) -------------------------------------------------

func TestJoin_ManyToOne_OwnersSharingATargetShareTheInstance(t *testing.T) {
	ctx := context.Background()
	db := testsupport.OpenSample(t)
	ada := testsupport.InsertUser(t, db, "Ada", "ada@example.com")
	tx1 := insertJoinTransaction(t, ctx, db, ada.ID, "1.00")
	tx2 := insertJoinTransaction(t, ctx, db, ada.ID, "2.00")

	loaded, err := orm.From[sample.Transaction](db).
		Where(orm.In("ID", tx1.ID, tx2.ID)).
		Include("User").Fetch(orm.FetchJoin).List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(loaded) != 2 {
		t.Fatalf("expected 2 transactions, got %d", len(loaded))
	}
	if loaded[0].User == nil || loaded[1].User == nil {
		t.Fatal("expected both transactions' User to load")
	}
	if loaded[0].User != loaded[1].User {
		t.Fatal("owners sharing a many-to-one target must share the same instance, got distinct pointers")
	}
}

func TestJoin_ManyToMany_TargetsSharedAcrossOwners(t *testing.T) {
	ctx := context.Background()
	db := testsupport.OpenSample(t)
	ada := testsupport.InsertUser(t, db, "Ada", "ada@example.com")
	grace := testsupport.InsertUser(t, db, "Grace", "grace@example.com")
	admin := insertRole(t, ctx, db, "Admin")
	assignRole(t, ctx, db, ada.ID, admin.ID)
	assignRole(t, ctx, db, grace.ID, admin.ID)

	loaded, err := orm.From[sample.User](db).
		Where(orm.In("ID", ada.ID, grace.ID)).
		OrderBy("ID").
		Include("Roles").Fetch(orm.FetchJoin).List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(loaded) != 2 || len(loaded[0].Roles) != 1 || len(loaded[1].Roles) != 1 {
		t.Fatalf("expected both users to have exactly one role each, got %+v / %+v", loaded[0].Roles, loaded[1].Roles)
	}
	if loaded[0].Roles[0] != loaded[1].Roles[0] {
		t.Fatal("a role shared by two owners must share the same instance, got distinct pointers")
	}
}

// --- dead links --------------------------------------------------------------

func TestJoin_ManyToOne_DeadLinkLoadsAsNil(t *testing.T) {
	ctx := context.Background()
	db := testsupport.OpenSample(t)
	ada := testsupport.InsertUser(t, db, "Ada", "ada@example.com")
	tx := insertJoinTransaction(t, ctx, db, ada.ID, "1.00")

	if _, err := orm.Execute(ctx, db,
		orm.InlineCommand[struct {
			ID     int64
			UserID int64
		}]("update transactions set user_id = @UserID where id = @ID"),
		struct {
			ID     int64
			UserID int64
		}{ID: tx.ID, UserID: 999999},
	); err != nil {
		t.Fatalf("point FK at a dead link: %v", err)
	}

	loaded, err := orm.From[sample.Transaction](db).
		Where(orm.Eq("ID", tx.ID)).
		Include("User").Fetch(orm.FetchJoin).List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected 1 transaction, got %d", len(loaded))
	}
	if loaded[0].User != nil {
		t.Fatalf("a dead link must load as nil, got %+v", loaded[0].User)
	}
}

// --- raw-vs-dedup row count: several includes cancel the fan-out -----------

func TestJoin_SeveralIncludes_IdentityDedupCancelsFanOut(t *testing.T) {
	ctx := context.Background()
	db := testsupport.OpenSample(t)
	ada := testsupport.InsertUser(t, db, "Ada", "ada@example.com")
	insertProfile(t, ctx, db, ada.ID, "Mathematician")
	tx1 := insertJoinTransaction(t, ctx, db, ada.ID, "1.00")
	tx2 := insertJoinTransaction(t, ctx, db, ada.ID, "2.00")
	tx3 := insertJoinTransaction(t, ctx, db, ada.ID, "3.00")

	// Three raw joined rows (one per transaction, each repeating the same
	// Profile segment) must dedup to exactly one root with all three
	// transactions and the one Profile still attached.
	loaded, err := orm.From[sample.User](db).
		Where(orm.Eq("ID", ada.ID)).
		Include("Profile", "Transactions").Fetch(orm.FetchJoin).List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected identity-dedup to collapse the fan-out to 1 root, got %d", len(loaded))
	}
	if loaded[0].Profile == nil {
		t.Fatal("expected Profile to still load despite the Transactions fan-out")
	}
	if len(loaded[0].Transactions) != 3 {
		t.Fatalf("expected 3 transactions, got %d", len(loaded[0].Transactions))
	}
	ids := map[int64]bool{tx1.ID: true, tx2.ID: true, tx3.ID: true}
	for _, tx := range loaded[0].Transactions {
		if !ids[tx.ID] {
			t.Fatalf("unexpected transaction %d in the loaded collection", tx.ID)
		}
	}
}

// A single included navigation's raw row count is exactly the target row
// count (no other navigation to fan out against, so dedup changes nothing).
func TestJoin_SingleInclude_RawRowCountMatchesTargetCount(t *testing.T) {
	ctx := context.Background()
	db := testsupport.OpenSample(t)
	ada := testsupport.InsertUser(t, db, "Ada", "ada@example.com")
	for i := 0; i < 4; i++ {
		insertJoinTransaction(t, ctx, db, ada.ID, "1.00")
	}

	loaded, err := orm.From[sample.User](db).
		Where(orm.Eq("ID", ada.ID)).
		Include("Transactions").Fetch(orm.FetchJoin).List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(loaded) != 1 || len(loaded[0].Transactions) != 4 {
		t.Fatalf("expected 1 user with 4 transactions, got %d user(s), %d transaction(s)",
			len(loaded), len(loaded[0].Transactions))
	}
}

// --- to-one-only include with paging is allowed ------------------------------

func TestJoin_ToOneOnlyInclude_PagingIsAllowed(t *testing.T) {
	ctx := context.Background()
	db := testsupport.OpenSample(t)
	ada := testsupport.InsertUser(t, db, "Ada", "ada@example.com")
	insertJoinTransaction(t, ctx, db, ada.ID, "1.00")
	insertJoinTransaction(t, ctx, db, ada.ID, "2.00")

	loaded, err := orm.From[sample.Transaction](db).
		OrderBy("ID").Limit(1).
		Include("User").Fetch(orm.FetchJoin).List(ctx)
	if err != nil {
		t.Fatalf("expected a to-one-only include to page fine, got: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected exactly 1 row (LIMIT 1), got %d", len(loaded))
	}
	if loaded[0].User == nil || loaded[0].User.ID != ada.ID {
		t.Fatalf("expected User to still load, got %+v", loaded[0].User)
	}
}

// --- refusals, before any SQL ------------------------------------------------

func TestJoin_UnknownNavigation_IsREL001(t *testing.T) {
	ctx := context.Background()
	db := testsupport.OpenSample(t)

	_, err := orm.From[sample.User](db).Include("Bogus").Fetch(orm.FetchJoin).List(ctx)
	if orm.CodeOf(err) != "REL-001" {
		t.Fatalf("expected REL-001, got %v", err)
	}
}

func TestJoin_CollectionWithLimit_IsREL005(t *testing.T) {
	ctx := context.Background()
	db := testsupport.OpenSample(t)

	_, err := orm.From[sample.User](db).Limit(5).
		Include("Transactions").Fetch(orm.FetchJoin).List(ctx)
	if orm.CodeOf(err) != "REL-005" {
		t.Fatalf("expected REL-005, got %v", err)
	}

	_, err = orm.From[sample.User](db).Offset(5).
		Include("Transactions").Fetch(orm.FetchJoin).List(ctx)
	if orm.CodeOf(err) != "REL-005" {
		t.Fatalf("expected REL-005 for Offset too, got %v", err)
	}
}

func TestJoin_TwoCollectionIncludes_IsREL006(t *testing.T) {
	ctx := context.Background()
	db := testsupport.OpenSample(t)

	_, err := orm.From[sample.User](db).
		Include("Transactions", "Roles").Fetch(orm.FetchJoin).List(ctx)
	if orm.CodeOf(err) != "REL-006" {
		t.Fatalf("expected REL-006, got %v", err)
	}
}

// joinOneOneOwner/joinOneOneTarget are a test-local one-to-one pair
// deliberately without the unique index a real 1:1 needs (spec/loading.md:
// "the unique index on the target FK is what makes a 1:1"), so two target
// rows can share an owner's FK and drift into REL-002 under Join mode too.
type joinOneOneOwner struct {
	ID     int64             `orm:"column,key,generated"`
	Name   string            `orm:"column"`
	Target *joinOneOneTarget `orm:"one_to_one=OwnerID"`
}

func (joinOneOneOwner) Entity() orm.EntityDef {
	return orm.EntityDef{Source: orm.Table("join_rel002_owners")}
}

type joinOneOneTarget struct {
	ID      int64  `orm:"column,key,generated"`
	OwnerID int64  `orm:"column"`
	Note    string `orm:"column"`
}

func (joinOneOneTarget) Entity() orm.EntityDef {
	return orm.EntityDef{Source: orm.Table("join_rel002_targets")}
}

func TestJoin_OneToOne_DriftedDuplicateRow_IsREL002(t *testing.T) {
	ctx := context.Background()
	db, err := orm.Open(ctx, testsupport.TempDatabase(t), orm.Options{Dialect: sqlite.New()})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := orm.CreateTable[joinOneOneOwner](ctx, db); err != nil {
		t.Fatalf("create owners: %v", err)
	}
	if err := orm.CreateTable[joinOneOneTarget](ctx, db); err != nil {
		t.Fatalf("create targets: %v", err)
	}

	owner := &joinOneOneOwner{Name: "Owner"}
	if err := orm.Insert(ctx, db, owner); err != nil {
		t.Fatalf("insert owner: %v", err)
	}
	for _, note := range []string{"first", "second"} {
		target := &joinOneOneTarget{OwnerID: owner.ID, Note: note}
		if err := orm.Insert(ctx, db, target); err != nil {
			t.Fatalf("insert target: %v", err)
		}
	}

	_, err = orm.From[joinOneOneOwner](db).
		Include("Target").Fetch(orm.FetchJoin).List(ctx)
	if orm.CodeOf(err) != "REL-002" {
		t.Fatalf("expected REL-002, got %v", err)
	}
}

// joinKeylessTarget/joinOwnerOfKeylessTarget: the FK ("TargetID") is a mapped
// column the loader can check at declaration, and the target's own arity is 0
// (keyArity short-circuits, spec/loading.md "Shape errors": "an arity mismatch
// against a key the target never declared") — join-mode's own runtime check is
// what catches the keyless target.
type joinKeylessTarget struct {
	ID   int64  `orm:"column"` // no key tag: deliberately keyless
	Name string `orm:"column"`
}

func (joinKeylessTarget) Entity() orm.EntityDef {
	// View-backed, never Table-backed: a table-backed entity must declare a
	// key (MAP-019) in this port, so "keyless" is only reachable through a
	// view — the refusal fires before any SQL runs, so the view need not be
	// a real one.
	return orm.EntityDef{Source: orm.View("join_rel003_keyless_targets", "select 1 as id, 'x' as name")}
}

type joinOwnerOfKeylessTarget struct {
	ID       int64              `orm:"column,key,generated"`
	TargetID int64              `orm:"column"`
	Target   *joinKeylessTarget `orm:"many_to_one=TargetID"`
}

func (joinOwnerOfKeylessTarget) Entity() orm.EntityDef {
	return orm.EntityDef{Source: orm.Table("join_rel003_owners_of_keyless")}
}

func TestJoin_KeylessTarget_IsREL003(t *testing.T) {
	ctx := context.Background()
	db, err := orm.Open(ctx, testsupport.TempDatabase(t), orm.Options{Dialect: sqlite.New()})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	// The refusal fires before any SQL runs — no table needs to exist.
	_, err = orm.From[joinOwnerOfKeylessTarget](db).
		Include("Target").Fetch(orm.FetchJoin).List(ctx)
	if orm.CodeOf(err) != "REL-003" {
		t.Fatalf("expected REL-003, got %v", err)
	}
}

// joinKeylessRoot/joinKeylessRootTarget: the root itself declares no key —
// identity cannot dedup its rows, so join-mode refuses regardless of the
// navigation's own shape (spec/loading.md: "a keyless root … refuse — load
// them via MultiQuery").
type joinKeylessRootTarget struct {
	ID int64 `orm:"column,key,generated"`
}

func (joinKeylessRootTarget) Entity() orm.EntityDef {
	return orm.EntityDef{Source: orm.Table("join_rel003_keyless_root_targets")}
}

type joinKeylessRoot struct {
	ID       int64                  `orm:"column"` // no key tag: deliberately keyless
	TargetID int64                  `orm:"column"`
	Target   *joinKeylessRootTarget `orm:"many_to_one=TargetID"`
}

func (joinKeylessRoot) Entity() orm.EntityDef {
	// View-backed for the same reason as joinKeylessTarget above.
	return orm.EntityDef{Source: orm.View("join_rel003_keyless_roots", "select 1 as id, 1 as target_id")}
}

func TestJoin_KeylessRoot_IsREL003(t *testing.T) {
	ctx := context.Background()
	db, err := orm.Open(ctx, testsupport.TempDatabase(t), orm.Options{Dialect: sqlite.New()})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	_, err = orm.From[joinKeylessRoot](db).
		Include("Target").Fetch(orm.FetchJoin).List(ctx)
	if orm.CodeOf(err) != "REL-003" {
		t.Fatalf("expected REL-003, got %v", err)
	}
}

// --- Join mode loads the identical graph MultiQuery does (spec/loading.md:
// "the modes must load identical graphs") ------------------------------------

func TestJoin_ManyToOne_MatchesMultiQueryGraph(t *testing.T) {
	ctx := context.Background()
	db := testsupport.OpenSample(t)
	ada := testsupport.InsertUser(t, db, "Ada", "ada@example.com")
	insertJoinTransaction(t, ctx, db, ada.ID, "1.00")
	insertJoinTransaction(t, ctx, db, ada.ID, "2.00")

	viaJoin, err := orm.From[sample.Transaction](db).
		OrderBy("ID").
		Include("User").Fetch(orm.FetchJoin).List(ctx)
	if err != nil {
		t.Fatalf("Join List: %v", err)
	}
	viaMultiQuery, err := orm.From[sample.Transaction](db).
		OrderBy("ID").
		Include("User").Fetch(orm.FetchMultiQuery).List(ctx)
	if err != nil {
		t.Fatalf("MultiQuery List: %v", err)
	}

	if len(viaJoin) != len(viaMultiQuery) {
		t.Fatalf("row count differs: join=%d multiQuery=%d", len(viaJoin), len(viaMultiQuery))
	}
	for i := range viaJoin {
		if viaJoin[i].ID != viaMultiQuery[i].ID {
			t.Fatalf("row %d: transaction ID differs: join=%d multiQuery=%d", i, viaJoin[i].ID, viaMultiQuery[i].ID)
		}
		joinUser, multiUser := viaJoin[i].User, viaMultiQuery[i].User
		if (joinUser == nil) != (multiUser == nil) {
			t.Fatalf("row %d: User nil-ness differs: join=%v multiQuery=%v", i, joinUser, multiUser)
		}
		if joinUser != nil && joinUser.ID != multiUser.ID {
			t.Fatalf("row %d: User ID differs: join=%d multiQuery=%d", i, joinUser.ID, multiUser.ID)
		}
	}
}

func TestJoin_OneToMany_MatchesMultiQueryGraph(t *testing.T) {
	ctx := context.Background()
	db := testsupport.OpenSample(t)
	ada := testsupport.InsertUser(t, db, "Ada", "ada@example.com")
	grace := testsupport.InsertUser(t, db, "Grace", "grace@example.com")
	insertJoinTransaction(t, ctx, db, ada.ID, "1.00")
	insertJoinTransaction(t, ctx, db, ada.ID, "2.00")
	insertJoinTransaction(t, ctx, db, grace.ID, "3.00")

	viaJoin, err := orm.From[sample.User](db).
		OrderBy("ID").
		Include("Transactions").Fetch(orm.FetchJoin).List(ctx)
	if err != nil {
		t.Fatalf("Join List: %v", err)
	}
	viaMultiQuery, err := orm.From[sample.User](db).
		OrderBy("ID").
		Include("Transactions").Fetch(orm.FetchMultiQuery).List(ctx)
	if err != nil {
		t.Fatalf("MultiQuery List: %v", err)
	}

	if len(viaJoin) != len(viaMultiQuery) {
		t.Fatalf("row count differs: join=%d multiQuery=%d", len(viaJoin), len(viaMultiQuery))
	}
	for i := range viaJoin {
		if viaJoin[i].ID != viaMultiQuery[i].ID {
			t.Fatalf("row %d: user ID differs: join=%d multiQuery=%d", i, viaJoin[i].ID, viaMultiQuery[i].ID)
		}
		if len(viaJoin[i].Transactions) != len(viaMultiQuery[i].Transactions) {
			t.Fatalf("row %d: transaction count differs: join=%d multiQuery=%d",
				i, len(viaJoin[i].Transactions), len(viaMultiQuery[i].Transactions))
		}
		for j := range viaJoin[i].Transactions {
			if viaJoin[i].Transactions[j].ID != viaMultiQuery[i].Transactions[j].ID {
				t.Fatalf("row %d transaction %d: ID differs: join=%d multiQuery=%d",
					i, j, viaJoin[i].Transactions[j].ID, viaMultiQuery[i].Transactions[j].ID)
			}
		}
	}
}
