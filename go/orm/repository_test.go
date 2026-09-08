package orm_test

import (
	"context"
	"testing"

	"github.com/kosmassp/SimpleOrm/go/orm"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/testsupport"
	"github.com/kosmassp/SimpleOrm/go/orm/sample"
)

// userRepository is a sample entity-specific repository built by embedding
// the generic base (ADR-0016, CODING-STANDARD §10) — the Go analog of the
// sample's UserRepository : Repository<User>.
type userRepository struct {
	*orm.Repository[sample.User]
}

func newUserRepository(db *orm.Db) *userRepository {
	return &userRepository{Repository: orm.NewRepository[sample.User](db)}
}

func (r *userRepository) GetByEmail(ctx context.Context, email string) (sample.User, error) {
	return r.Query().Where(orm.Eq("Email", email)).Single(ctx)
}

type transactionRepository struct {
	*orm.Repository[sample.Transaction]
}

func newTransactionRepository(db *orm.Db) *transactionRepository {
	return &transactionRepository{Repository: orm.NewRepository[sample.Transaction](db)}
}

func (r *transactionRepository) GetByStatus(ctx context.Context, status sample.TransactionStatus) ([]sample.Transaction, error) {
	return r.Query().Where(orm.Eq("Status", status)).List(ctx)
}

func (r *transactionRepository) GetByUser(ctx context.Context, userID int64) ([]sample.Transaction, error) {
	return r.Query().Where(orm.Eq("UserID", userID)).List(ctx)
}

func TestRepository_SharesOneSessionAndCoversTheFlow(t *testing.T) {
	ctx := context.Background()
	db := testsupport.OpenSample(t)
	users := newUserRepository(db)
	transactions := newTransactionRepository(db)

	ada := &sample.User{Name: "Ada", Email: "ada@example.com"}
	ada.CreatedAtUtc = testsupport.SeedTime
	if err := users.Insert(ctx, ada); err != nil { // generic, from the base
		t.Fatal(err)
	}

	found, err := users.GetByEmail(ctx, "ada@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if found.ID != ada.ID {
		t.Fatalf("expected %d, got %d", ada.ID, found.ID)
	}

	got, err := users.Get(ctx, ada.ID)
	if err != nil || got.Name != "Ada" {
		t.Fatalf("got %+v, %v", got, err)
	}
	if missing, err := users.GetOrDefault(ctx, int64(999_999)); err != nil || missing != nil {
		t.Fatalf("expected nil, nil, got %v, %v", missing, err)
	}

	tx := &sample.Transaction{UserID: ada.ID, Status: sample.Pending, Amount: orm.MustDecimal("42")}
	tx.CreatedAtUtc = testsupport.SeedTime
	if err := transactions.Insert(ctx, tx); err != nil {
		t.Fatal(err)
	}

	pending, err := transactions.GetByStatus(ctx, sample.Pending)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 {
		t.Fatalf("expected 1, got %d", len(pending))
	}

	pending[0].Status = sample.Completed
	if err := transactions.Update(ctx, &pending[0]); err != nil {
		t.Fatal(err)
	}
	byUser, err := transactions.GetByUser(ctx, ada.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(byUser) != 1 || byUser[0].Status != sample.Completed {
		t.Fatalf("got %+v", byUser)
	}

	displayName := "The Countess"
	ada.DisplayName = &displayName // generic update from the base
	if err := users.Update(ctx, ada); err != nil {
		t.Fatal(err)
	}
	updated, err := users.Get(ctx, ada.ID)
	if err != nil || updated.DisplayName == nil || *updated.DisplayName != "The Countess" {
		t.Fatalf("got %+v, %v", updated, err)
	}

	if err := users.Delete(ctx, ada.ID); err != nil {
		t.Fatal(err)
	}
	if missing, err := users.GetOrDefault(ctx, ada.ID); err != nil || missing != nil {
		t.Fatalf("expected nil, nil, got %v, %v", missing, err)
	}
}

func TestRepository_TransactionsWrapRepositoryWorkThroughTheSharedSession(t *testing.T) {
	ctx := context.Background()
	db := testsupport.OpenSample(t)
	users := newUserRepository(db)

	tx, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ghost := &sample.User{Name: "Ghost", Email: "ghost@example.com"}
	ghost.CreatedAtUtc = testsupport.SeedTime
	if err := users.Insert(ctx, ghost); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(ctx); err != nil { // no commit -> rollback
		t.Fatal(err)
	}

	all, err := users.GetAll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 0 {
		t.Fatalf("expected 0, got %d", len(all))
	}
}
