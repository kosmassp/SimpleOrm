package orm_test

import (
	"context"
	"testing"

	"github.com/kosmassp/SimpleOrm/go/orm"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/testsupport"
	"github.com/kosmassp/SimpleOrm/go/orm/sample"
	"github.com/kosmassp/SimpleOrm/go/orm/sqlite"
)

// §7.10: nested results are produced by the database with
// json_group_array(json_object(…)) and deserialized by the JSON handler.

type detailLine struct {
	Description string
	Quantity    int32
	UnitPrice   orm.Decimal
}

type transactionWithDetails struct {
	ID      int64
	Amount  orm.Decimal
	Details []detailLine
}

type transactionsByUserArgs struct{ UserID int64 }

var withDetails = orm.Inline[transactionsByUserArgs, transactionWithDetails](`
select t.id,
       t.amount,
       (select json_group_array(json_object(
                'description', d.description,
                'quantity', d.quantity,
                'unit_price', d.unit_price))
        from transaction_details d
        where d.transaction_id = t.id) as details
from transactions t
where t.user_id = @UserID
order by t.id
`)

func openJSONSample(t *testing.T) *orm.Db {
	t.Helper()
	ctx := context.Background()
	handlers := orm.NewTypeHandlerRegistry()
	orm.RegisterJSON[[]detailLine](handlers)

	db, err := orm.Open(ctx, testsupport.TempDatabase(t), orm.Options{Dialect: sqlite.New(), TypeHandlers: handlers})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	for _, create := range []func(context.Context, *orm.Db) error{
		orm.CreateTable[sample.User],
		orm.CreateTable[sample.Transaction],
		orm.CreateTable[sample.TransactionDetail],
	} {
		if err := create(ctx, db); err != nil {
			t.Fatalf("create table: %v", err)
		}
	}
	return db
}

func TestJSON_ChildrenNestThroughTheHandler(t *testing.T) {
	ctx := context.Background()
	db := openJSONSample(t)

	ada := testsupport.InsertUser(t, db, "Ada", "ada@example.com")
	tx := &sample.Transaction{UserID: ada.ID, Status: sample.Completed, Amount: orm.MustDecimal("15.25")}
	tx.CreatedAtUtc = testsupport.SeedTime
	if err := orm.Insert(ctx, db, tx); err != nil {
		t.Fatal(err)
	}
	cake := &sample.TransactionDetail{TransactionID: tx.ID, Description: "cake", Quantity: 2, UnitPrice: orm.MustDecimal("5.00")}
	cake.CreatedAtUtc = testsupport.SeedTime
	if err := orm.Insert(ctx, db, cake); err != nil {
		t.Fatal(err)
	}
	candle := &sample.TransactionDetail{TransactionID: tx.ID, Description: "candle", Quantity: 1, UnitPrice: orm.MustDecimal("5.25")}
	candle.CreatedAtUtc = testsupport.SeedTime
	if err := orm.Insert(ctx, db, candle); err != nil {
		t.Fatal(err)
	}

	rows, err := orm.Query(ctx, db, withDetails, transactionsByUserArgs{UserID: ada.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	row := rows[0]
	if !row.Amount.Equal(orm.MustDecimal("15.25")) {
		t.Errorf("expected 15.25, got %s", row.Amount)
	}
	if len(row.Details) != 2 {
		t.Fatalf("expected 2 details, got %d", len(row.Details))
	}
	if row.Details[0] != (detailLine{"cake", 2, orm.MustDecimal("5.00")}) {
		t.Errorf("got %+v", row.Details[0])
	}
	if row.Details[1] != (detailLine{"candle", 1, orm.MustDecimal("5.25")}) {
		t.Errorf("got %+v", row.Details[1])
	}

	// A parent with no children gets an empty list, not null.
	grace := testsupport.InsertUser(t, db, "Grace", "grace@example.com")
	lonely := &sample.Transaction{UserID: grace.ID, Status: sample.Pending, Amount: orm.MustDecimal("1")}
	lonely.CreatedAtUtc = testsupport.SeedTime
	if err := orm.Insert(ctx, db, lonely); err != nil {
		t.Fatal(err)
	}
	lonelyRows, err := orm.Query(ctx, db, withDetails, transactionsByUserArgs{UserID: grace.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(lonelyRows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(lonelyRows))
	}
	if lonelyRows[0].Details == nil {
		t.Errorf("expected an empty (non-nil) slice, got nil")
	}
	if len(lonelyRows[0].Details) != 0 {
		t.Errorf("expected 0 details, got %d", len(lonelyRows[0].Details))
	}
}
