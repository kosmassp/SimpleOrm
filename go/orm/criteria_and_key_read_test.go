package orm_test

import (
	"context"
	"testing"

	"github.com/kosmassp/SimpleOrm/go/orm"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/testsupport"
	"github.com/kosmassp/SimpleOrm/go/orm/sample"
)

// ADR-0020 null semantics, against a real database.
func TestFrom_NullCriteriaRenderIsNullAndActuallyMatch(t *testing.T) {
	ctx := context.Background()
	db := testsupport.OpenSample(t)
	ada := testsupport.InsertUser(t, db, "AdaNull", "ada-null@example.com")
	grace := testsupport.InsertUser(t, db, "GraceNull", "grace-null@example.com")
	displayName := "Countess"
	grace.DisplayName = &displayName
	if err := orm.Update(ctx, db, grace); err != nil {
		t.Fatal(err)
	}

	// Eq(nil) renders IS NULL — it finds the row a '= NULL' comparison would silently miss.
	unset, err := orm.From[sample.User](db).
		Where(orm.Eq("DisplayName", nil), orm.Like("Name", "%Null")).List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(unset) != 1 || unset[0].ID != ada.ID {
		t.Fatalf("got %+v", unset)
	}

	set, err := orm.From[sample.User](db).
		Where(orm.Ne("DisplayName", nil), orm.Like("Name", "%Null")).List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(set) != 1 || set[0].ID != grace.ID {
		t.Fatalf("got %+v", set)
	}

	// Ordered comparison with null and null-in-IN are refused, not rendered.
	_, err = orm.From[sample.User](db).Where(orm.Gt("ID", nil)).List(ctx)
	if orm.CodeOf(err) != "QRY-007" {
		t.Fatalf("expected QRY-007, got %v", err)
	}
	_, err = orm.From[sample.User](db).Where(orm.In[any]("DisplayName", "x", nil)).List(ctx)
	if orm.CodeOf(err) != "QRY-007" {
		t.Fatalf("expected QRY-007, got %v", err)
	}
}

func TestGet_ReadsAndMissesWithCodes(t *testing.T) {
	ctx := context.Background()
	db := testsupport.OpenSample(t)
	ada := testsupport.InsertUser(t, db, "Ada", "ada@example.com")

	got, err := orm.Get[sample.User](ctx, db, ada.ID)
	if err != nil || got.Name != "Ada" {
		t.Fatalf("got %+v, %v", got, err)
	}
	widened, err := orm.Get[sample.User](ctx, db, int32(ada.ID)) // int32 widens to int64
	if err != nil || widened.Name != "Ada" {
		t.Fatalf("got %+v, %v", widened, err)
	}

	_, err = orm.Get[sample.User](ctx, db, int64(999_999))
	if orm.CodeOf(err) != "CRUD-001" {
		t.Fatalf("expected CRUD-001, got %v", err)
	}
	if missing, err := orm.GetOrDefault[sample.User](ctx, db, int64(999_999)); err != nil || missing != nil {
		t.Fatalf("expected nil, nil, got %v, %v", missing, err)
	}
}

func TestGet_CompositeKeysPassListsAndValidateShape(t *testing.T) {
	ctx := context.Background()
	db := testsupport.OpenSample(t)
	ada := testsupport.InsertUser(t, db, "Ada", "ada@example.com")
	role := &sample.Role{Name: "admin"}
	role.CreatedAtUtc = testsupport.SeedTime
	if err := orm.Insert(ctx, db, role); err != nil {
		t.Fatal(err)
	}
	link := &sample.UserRole{UserID: ada.ID, RoleID: role.ID}
	link.CreatedAtUtc = testsupport.SeedTime
	if err := orm.Insert(ctx, db, link); err != nil {
		t.Fatal(err)
	}

	got, err := orm.Get[sample.UserRole](ctx, db, []any{ada.ID, role.ID})
	if err != nil {
		t.Fatal(err)
	}
	if got.RoleID != role.ID {
		t.Errorf("expected role id %d, got %d", role.ID, got.RoleID)
	}

	_, err = orm.Get[sample.UserRole](ctx, db, ada.ID)
	if orm.CodeOf(err) != "CRUD-002" {
		t.Fatalf("expected CRUD-002 (arity), got %v", err)
	}

	_, err = orm.Get[sample.User](ctx, db, "not a key")
	if orm.CodeOf(err) != "CRUD-002" {
		t.Fatalf("expected CRUD-002 (type), got %v", err)
	}

	_, err = orm.Get[sample.DailySales](ctx, db, int64(1))
	if orm.CodeOf(err) != "QRY-005" {
		t.Fatalf("expected QRY-005, got %v", err)
	}
}

func TestFrom_OrAndInWithImplicitAnd(t *testing.T) {
	ctx := context.Background()
	db := testsupport.OpenSample(t)
	ada := testsupport.InsertUser(t, db, "Ada", "ada@example.com")
	testsupport.InsertUser(t, db, "Grace", "grace@example.com")
	testsupport.InsertUser(t, db, "Edsger", "edsger@example.com")

	// (Id = ada OR Name IN ('Grace','Nope')) AND CreatedAtUtc >= seed-1day
	users, err := orm.From[sample.User](db).
		Where(
			orm.Or(orm.Eq("ID", ada.ID), orm.In("Name", "Grace", "Nope")),
			orm.Ge("CreatedAtUtc", testsupport.SeedTime.AddDate(0, 0, -1)),
		).
		OrderBy("Name").
		List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 2 || users[0].Name != "Ada" || users[1].Name != "Grace" {
		t.Fatalf("got %+v", users)
	}
}

func TestFrom_OrderingPagingNullChecksAndLike(t *testing.T) {
	ctx := context.Background()
	db := testsupport.OpenSample(t)
	testsupport.InsertUser(t, db, "Ada", "ada@example.com")
	testsupport.InsertUser(t, db, "Grace", "grace@example.com")
	testsupport.InsertUser(t, db, "Edsger", "edsger@example.com")

	page, err := orm.From[sample.User](db).OrderBy("Name", orm.Desc).Limit(2).Offset(1).List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 2 || page[0].Name != "Edsger" || page[1].Name != "Ada" {
		t.Fatalf("got %+v", page)
	}

	fresh, err := orm.From[sample.User](db).Where(orm.IsNull("UpdatedAtUtc")).List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(fresh) != 3 {
		t.Fatalf("expected 3, got %d", len(fresh))
	}

	gr, err := orm.From[sample.User](db).Where(orm.Like("Name", "Gr%")).List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(gr) != 1 || gr[0].Name != "Grace" {
		t.Fatalf("got %+v", gr)
	}

	none, err := orm.From[sample.User](db).Where(orm.In[int64]("ID")).List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(none) != 0 {
		t.Fatalf("expected 0, got %d", len(none))
	}
}

func TestFrom_UnknownPropertyIsQRY006AndStatementsAreQRY005(t *testing.T) {
	ctx := context.Background()
	db := testsupport.OpenSample(t)

	_, err := orm.From[sample.User](db).Where(orm.Eq("Nope", 1)).List(ctx)
	if orm.CodeOf(err) != "QRY-006" {
		t.Fatalf("expected QRY-006, got %v", err)
	}

	_, err = orm.From[sample.DailySales](db).List(ctx)
	if orm.CodeOf(err) != "QRY-005" {
		t.Fatalf("expected QRY-005, got %v", err)
	}
}

func TestFrom_CriteriaWorkOnViewsToo(t *testing.T) {
	ctx := context.Background()
	db := testsupport.OpenSample(t)
	ada := testsupport.InsertUser(t, db, "Ada", "ada@example.com")
	testsupport.InsertUser(t, db, "Grace", "grace@example.com")
	tx := &sample.Transaction{UserID: ada.ID, Status: sample.Completed, Amount: orm.MustDecimal("10")}
	tx.CreatedAtUtc = testsupport.SeedTime
	if err := orm.Insert(ctx, db, tx); err != nil {
		t.Fatal(err)
	}

	busy, err := orm.From[sample.UserTransactionTotal](db).Where(orm.Gt("TransactionCount", int64(0))).List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(busy) != 1 || busy[0].UserName != "Ada" {
		t.Fatalf("got %+v", busy)
	}

	viaKey, err := orm.Get[sample.UserTransactionTotal](ctx, db, ada.ID) // keyed view read
	if err != nil {
		t.Fatal(err)
	}
	if viaKey.TransactionCount != 1 {
		t.Errorf("expected 1, got %d", viaKey.TransactionCount)
	}
}
