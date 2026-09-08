package mapping_test

import (
	"context"
	"database/sql"
	"reflect"
	"testing"
	"time"

	"github.com/kosmassp/SimpleOrm/go/orm"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/mapping"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/metadata"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/testsupport"
	"github.com/kosmassp/SimpleOrm/go/orm/sample"
	"github.com/kosmassp/SimpleOrm/go/orm/sqlite"
)

func newMapper() *mapping.ResultMapper {
	return mapping.NewResultMapper(metadata.NewLoader(nil), mapping.NewTypeConverter(core.NewTypeHandlerRegistry(), false))
}

// setupUsersTable creates a real "users" table with one seeded row and
// returns its file path — a second, raw database/sql connection reads it for
// the *sql.Rows a Plan needs (CODING-STANDARD §7: real temp-file databases only).
func setupUsersTable(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	path := testsupport.TempDatabase(t)

	db, err := orm.Open(ctx, path, orm.Options{Dialect: sqlite.New()})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := orm.CreateTable[sample.User](ctx, db); err != nil {
		t.Fatalf("create table: %v", err)
	}
	testsupport.InsertUser(t, db, "Ada", "ada@example.com")
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	return path
}

func openRawRows(t *testing.T, path, query string) *sql.Rows {
	t.Helper()
	raw, err := sql.Open(sqlite.DriverName, sqlite.DataSourceName(path))
	if err != nil {
		t.Fatalf("open raw connection: %v", err)
	}
	t.Cleanup(func() { _ = raw.Close() })

	rows, err := raw.QueryContext(context.Background(), query)
	if err != nil {
		t.Fatalf("query %q: %v", query, err)
	}
	t.Cleanup(func() { _ = rows.Close() })
	return rows
}

func planFor(t *testing.T, mapper *mapping.ResultMapper, resultType reflect.Type, rows *sql.Rows) *mapping.Plan {
	t.Helper()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatalf("columns: %v", err)
	}
	plan, err := mapper.Plan(resultType, columns, "test")
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	return plan
}

func readScalar[T any](t *testing.T, mapper *mapping.ResultMapper, path, query string) T {
	t.Helper()
	rows := openRawRows(t, path, query)
	plan := planFor(t, mapper, reflect.TypeFor[T](), rows)
	if !rows.Next() {
		t.Fatalf("expected a row for %q", query)
	}
	value, err := plan.Read(rows)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	typed, ok := value.(T)
	if !ok {
		t.Fatalf("expected %T, got %T (%v)", *new(T), value, value)
	}
	return typed
}

// --- entity strictness, both ways (§7.7) ------------------------------------

func TestPlan_EntityExtraColumnIsMAP001(t *testing.T) {
	path := setupUsersTable(t)
	rows := openRawRows(t, path, "select id, name, email, display_name, created_at, updated_at, 42 as extra from users")
	columns, _ := rows.Columns()
	_, err := newMapper().Plan(reflect.TypeFor[sample.User](), columns, "test")
	if core.CodeOf(err) != "MAP-001" {
		t.Fatalf("expected MAP-001, got %v", err)
	}
}

func TestPlan_EntityMissingColumnIsMAP002(t *testing.T) {
	path := setupUsersTable(t)
	rows := openRawRows(t, path, "select id, name from users")
	columns, _ := rows.Columns()
	_, err := newMapper().Plan(reflect.TypeFor[sample.User](), columns, "test")
	if core.CodeOf(err) != "MAP-002" {
		t.Fatalf("expected MAP-002, got %v", err)
	}
}

func TestPlan_EntityStrictnessFiresBeforeTheFirstRow(t *testing.T) {
	path := setupUsersTable(t)
	rows := openRawRows(t, path, "select id, name from users where 1 = 0") // zero rows, still strict
	columns, _ := rows.Columns()
	_, err := newMapper().Plan(reflect.TypeFor[sample.User](), columns, "test")
	if core.CodeOf(err) != "MAP-002" {
		t.Fatalf("expected MAP-002 even for an empty result, got %v", err)
	}
}

// --- DTO matching: case- and underscore-insensitive, the Go "required" rule ---

type userRow struct {
	ID        int64
	CreatedAt time.Time
	Nickname  *string // optional: no column, stays nil
}

func TestPlan_DTOMatchesCaseAndUnderscoreInsensitively(t *testing.T) {
	path := setupUsersTable(t)
	rows := openRawRows(t, path, "select id, created_at from users")
	plan := planFor(t, newMapper(), reflect.TypeFor[userRow](), rows)

	if !rows.Next() {
		t.Fatal("expected a row")
	}
	value, err := plan.Read(rows)
	if err != nil {
		t.Fatal(err)
	}
	row := value.(userRow)
	if row.ID == 0 {
		t.Errorf("expected a nonzero id")
	}
	if row.CreatedAt.IsZero() {
		t.Errorf("expected created_at to bind to CreatedAt")
	}
	if row.Nickname != nil {
		t.Errorf("expected the unbound pointer field to stay nil")
	}
}

type requiredNameDTO struct {
	ID   int64
	Name string // required: no column, non-pointer -> MAP-002
}

func TestPlan_DTOUnboundNonPointerFieldIsMAP002(t *testing.T) {
	path := setupUsersTable(t)
	rows := openRawRows(t, path, "select id from users")
	columns, _ := rows.Columns()
	_, err := newMapper().Plan(reflect.TypeFor[requiredNameDTO](), columns, "test")
	if core.CodeOf(err) != "MAP-002" {
		t.Fatalf("expected MAP-002, got %v", err)
	}
}

type idOnlyDTO struct{ ID int64 }

func TestPlan_DTOColumnWithNoFieldIsMAP001(t *testing.T) {
	path := setupUsersTable(t)
	rows := openRawRows(t, path, "select id, name from users")
	columns, _ := rows.Columns()
	_, err := newMapper().Plan(reflect.TypeFor[idOnlyDTO](), columns, "test")
	if core.CodeOf(err) != "MAP-001" {
		t.Fatalf("expected MAP-001, got %v", err)
	}
}

// --- scalars: the fixed table's types, an orm.Enum, a handler type, and pointers ---

func TestPlan_ScalarTypesReadColumnZero(t *testing.T) {
	path := setupUsersTable(t)
	mapper := newMapper()

	if got := readScalar[int64](t, mapper, path, "select 42"); got != 42 {
		t.Errorf("int64: got %d", got)
	}
	if got := readScalar[string](t, mapper, path, "select 'hi'"); got != "hi" {
		t.Errorf("string: got %q", got)
	}
	if got := readScalar[bool](t, mapper, path, "select 1"); !got {
		t.Errorf("bool: got %v", got)
	}
	if got := readScalar[float64](t, mapper, path, "select 1.5"); got != 1.5 {
		t.Errorf("float64: got %v", got)
	}
	if got := readScalar[[]byte](t, mapper, path, "select x'0102'"); string(got) != string([]byte{1, 2}) {
		t.Errorf("bytes: got %v", got)
	}
	if got := readScalar[core.Decimal](t, mapper, path, "select '19.99'"); !got.Equal(core.MustDecimal("19.99")) {
		t.Errorf("decimal: got %s", got)
	}
	if got := readScalar[core.GUID](t, mapper, path, "select '6f9619ff-8b86-d011-b42d-00cf4fc964ff'"); got.String() != "6f9619ff-8b86-d011-b42d-00cf4fc964ff" {
		t.Errorf("guid: got %s", got)
	}
	if got := readScalar[time.Time](t, mapper, path, "select '2026-01-01T00:00:00Z'"); got.Year() != 2026 {
		t.Errorf("time: got %v", got)
	}
	if got := readScalar[sample.TransactionStatus](t, mapper, path, "select 'Completed'"); got != sample.Completed {
		t.Errorf("enum: got %v", got)
	}

	// A nullable scalar result: NULL reads as a typed nil pointer, a value as a new pointer.
	if got := readScalar[*string](t, mapper, path, "select null"); got != nil {
		t.Errorf("expected nil, got %v", *got)
	}
	if got := readScalar[*string](t, mapper, path, "select 'x'"); got == nil || *got != "x" {
		t.Errorf("expected a pointer to 'x', got %v", got)
	}
}

type priceHandlerValue struct{ Amount core.Decimal }

type priceHandler struct{}

func (priceHandler) Parse(databaseValue any) (priceHandlerValue, error) {
	text, _ := databaseValue.(string)
	d, err := core.ParseDecimal(text)
	return priceHandlerValue{Amount: d}, err
}

func (priceHandler) Format(value priceHandlerValue) (any, error) { return value.Amount.String(), nil }

func TestPlan_HandlerTypeScalarUsesTheRegisteredHandler(t *testing.T) {
	path := setupUsersTable(t)
	handlers := core.NewTypeHandlerRegistry()
	core.RegisterHandler[priceHandlerValue](handlers, priceHandler{})
	mapper := mapping.NewResultMapper(metadata.NewLoader(nil), mapping.NewTypeConverter(handlers, false))

	got := readScalar[priceHandlerValue](t, mapper, path, "select '19.99'")
	if !got.Amount.Equal(core.MustDecimal("19.99")) {
		t.Errorf("got %v", got)
	}
}

// --- caching: the same (type, column set) plan is reused ---------------------

func TestPlan_CachesPerTypeAndColumnSet(t *testing.T) {
	path := setupUsersTable(t)
	mapper := newMapper()

	rows1 := openRawRows(t, path, "select id, name from users")
	columns1, _ := rows1.Columns()
	plan1, err := mapper.Plan(reflect.TypeFor[idOnlyDTOWithName](), columns1, "test")
	if err != nil {
		t.Fatal(err)
	}

	rows2 := openRawRows(t, path, "select id, name from users")
	columns2, _ := rows2.Columns()
	plan2, err := mapper.Plan(reflect.TypeFor[idOnlyDTOWithName](), columns2, "test")
	if err != nil {
		t.Fatal(err)
	}

	if plan1 != plan2 {
		t.Errorf("expected the same cached plan for the same (type, columns) pair")
	}
}

type idOnlyDTOWithName struct {
	ID   int64
	Name string
}
