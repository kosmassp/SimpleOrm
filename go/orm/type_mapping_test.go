package orm_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kosmassp/SimpleOrm/go/orm"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/testsupport"
	"github.com/kosmassp/SimpleOrm/go/orm/sample"
	"github.com/kosmassp/SimpleOrm/go/orm/sqlite"
)

// typeZoo carries every fixed-table type (§7.9), created and round-tripped
// via generated code — the Go analog of TypeMappingTests.cs's TypeZoo.
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

func TestTypeZoo_FixedTableRoundTripsEveryType(t *testing.T) {
	ctx := context.Background()
	db := testsupport.OpenSample(t)
	if err := orm.CreateTable[typeZoo](ctx, db); err != nil {
		t.Fatal(err)
	}

	maybeText := (*string)(nil)
	at := time.Date(2026, 8, 28, 10, 0, 0, 0, time.UTC)
	atOffset := time.Date(2026, 8, 28, 12, 0, 0, 0, time.FixedZone("+02:00", 2*60*60))
	day := time.Date(2026, 8, 28, 0, 0, 0, 0, time.UTC)
	clock := time.Date(1, 1, 1, 13, 30, 15, 0, time.UTC)
	token, err := orm.ParseGUID("6f9619ff-8b86-d011-b42d-00cf4fc964ff")
	if err != nil {
		t.Fatal(err)
	}

	zoo := &typeZoo{
		I32:       42,
		I16:       7,
		Price:     orm.MustDecimal("1234.56"),
		D:         2.25,
		F:         1.5,
		Flag:      true,
		Text:      "hello",
		MaybeText: maybeText,
		Token:     token,
		Blob:      []byte{1, 2, 3},
		At:        at,
		AtOffset:  atOffset,
		Day:       day,
		Clock:     clock,
		AsText:    sample.Completed,
		AsInt:     sample.Cancelled,
		MaybeEnum: nil,
	}
	if err := orm.Insert(ctx, db, zoo); err != nil {
		t.Fatal(err)
	}

	rows, err := orm.QueryAll[typeZoo](ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	loaded := rows[0]

	if loaded.I32 != zoo.I32 || loaded.I16 != zoo.I16 {
		t.Errorf("integers: got %+v", loaded)
	}
	if !loaded.Price.Equal(zoo.Price) {
		t.Errorf("expected price %s, got %s", zoo.Price, loaded.Price)
	}
	if loaded.D != zoo.D || loaded.F != zoo.F {
		t.Errorf("floats: got %+v", loaded)
	}
	if !loaded.Flag {
		t.Errorf("expected flag true")
	}
	if loaded.Text != "hello" {
		t.Errorf("expected hello, got %s", loaded.Text)
	}
	if loaded.MaybeText != nil {
		t.Errorf("expected nil MaybeText")
	}
	if loaded.Token != zoo.Token {
		t.Errorf("expected token %s, got %s", zoo.Token, loaded.Token)
	}
	if string(loaded.Blob) != string(zoo.Blob) {
		t.Errorf("expected blob %v, got %v", zoo.Blob, loaded.Blob)
	}
	if !loaded.At.Equal(zoo.At) || loaded.At.Location() != time.UTC {
		t.Errorf("expected UTC instant %v, got %v (%v)", zoo.At, loaded.At, loaded.At.Location())
	}
	if !loaded.AtOffset.Equal(zoo.AtOffset) { // same instant
		t.Errorf("expected same instant as %v, got %v", zoo.AtOffset, loaded.AtOffset)
	}
	if !loaded.Day.Equal(zoo.Day) {
		t.Errorf("expected day %v, got %v", zoo.Day, loaded.Day)
	}
	if loaded.Clock.Hour() != 13 || loaded.Clock.Minute() != 30 || loaded.Clock.Second() != 15 {
		t.Errorf("expected 13:30:15, got %v", loaded.Clock)
	}
	if loaded.AsText != sample.Completed {
		t.Errorf("expected Completed, got %v", loaded.AsText)
	}
	if loaded.AsInt != sample.Cancelled {
		t.Errorf("expected Cancelled, got %v", loaded.AsInt)
	}
	if loaded.MaybeEnum != nil {
		t.Errorf("expected nil MaybeEnum")
	}

	// Storage-representation checks: enum-as-text vs enum-as-int, datetime as ISO Z.
	asTextRaw := orm.Inline[orm.EmptyArgs, string]("select as_text from type_zoo")
	text, err := orm.QuerySingle(ctx, db, asTextRaw, orm.EmptyArgs{})
	if err != nil || text != "Completed" {
		t.Fatalf("expected Completed, got %q, %v", text, err)
	}
	asIntRaw := orm.Inline[orm.EmptyArgs, int64]("select as_int from type_zoo")
	number, err := orm.QuerySingle(ctx, db, asIntRaw, orm.EmptyArgs{})
	if err != nil || number != 2 { // Cancelled is the third declared member, index 2
		t.Fatalf("expected 2, got %d, %v", number, err)
	}
	atRaw := orm.Inline[orm.EmptyArgs, string]("select at from type_zoo")
	stored, err := orm.QuerySingle(ctx, db, atRaw, orm.EmptyArgs{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(stored, "Z") {
		t.Errorf("expected a trailing Z, got %q", stored)
	}
}

type requiredDTO struct {
	ID   int64
	Name string
}

func TestQuery_StrictnessCodesFirePerCase(t *testing.T) {
	ctx := context.Background()
	db := testsupport.OpenSample(t)
	testsupport.InsertUser(t, db, "Ada", "ada@example.com")

	// MAP-002: entity result missing mapped columns.
	partial := orm.Inline[orm.EmptyArgs, sample.User]("select id, name from users")
	if _, err := orm.Query(ctx, db, partial, orm.EmptyArgs{}); orm.CodeOf(err) != "MAP-002" {
		t.Fatalf("expected MAP-002, got %v", err)
	}

	// MAP-002: required (non-pointer) DTO field without a column.
	missingRequired := orm.Inline[orm.EmptyArgs, requiredDTO]("select id from users")
	if _, err := orm.Query(ctx, db, missingRequired, orm.EmptyArgs{}); orm.CodeOf(err) != "MAP-002" {
		t.Fatalf("expected MAP-002, got %v", err)
	}

	// MAP-031: the rule exists but the value is garbage.
	type decimalRow struct{ Amount orm.Decimal }
	badDecimal := orm.Inline[orm.EmptyArgs, decimalRow]("select 'abc' as amount")
	if _, err := orm.Query(ctx, db, badDecimal, orm.EmptyArgs{}); orm.CodeOf(err) != "MAP-031" {
		t.Fatalf("expected MAP-031, got %v", err)
	}

	// MAP-031: unknown enum name in the column.
	badEnum := orm.Inline[orm.EmptyArgs, sample.Transaction](
		"select 1 as id, 1 as user_id, 'Nope' as status, '1' as amount, 0 as version, null as note, " +
			"'2026-01-01T00:00:00Z' as created_at, null as updated_at")
	if _, err := orm.Query(ctx, db, badEnum, orm.EmptyArgs{}); orm.CodeOf(err) != "MAP-031" {
		t.Fatalf("expected MAP-031, got %v", err)
	}

	// VAL-020 read: a stored datetime without a UTC marker.
	type stampRow struct{ CreatedAt time.Time }
	unmarked := orm.Inline[orm.EmptyArgs, stampRow]("select '2026-01-01T00:00:00' as created_at")
	if _, err := orm.Query(ctx, db, unmarked, orm.EmptyArgs{}); orm.CodeOf(err) != "VAL-020" {
		t.Fatalf("expected VAL-020, got %v", err)
	}
}

// money is the custom-handler fixture (a value type the fixed table cannot express).
type money struct{ Amount orm.Decimal }

type moneyHandler struct{}

func (moneyHandler) Parse(databaseValue any) (money, error) {
	switch v := databaseValue.(type) {
	case string:
		d, err := orm.ParseDecimal(v)
		return money{Amount: d}, err
	}
	return money{}, nil
}

func (moneyHandler) Format(value money) (any, error) { return value.Amount.String(), nil }

func TestTypeHandler_RegisteredHandlerRoundTripsBothDirections(t *testing.T) {
	ctx := context.Background()
	handlers := orm.NewTypeHandlerRegistry()
	orm.RegisterHandler[money](handlers, moneyHandler{})

	path := testsupport.TempDatabase(t)
	db, err := orm.Open(ctx, path, orm.Options{Dialect: sqlite.New(), TypeHandlers: handlers})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	type priceArgs struct{ P money }
	type priceRow struct{ Price money }
	echo := orm.Inline[priceArgs, priceRow]("select @P as price")

	row, err := orm.QuerySingle(ctx, db, echo, priceArgs{P: money{Amount: orm.MustDecimal("19.99")}})
	if err != nil {
		t.Fatal(err)
	}
	if !row.Price.Amount.Equal(orm.MustDecimal("19.99")) {
		t.Errorf("expected 19.99, got %s", row.Price.Amount)
	}
}
