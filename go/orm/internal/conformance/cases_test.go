package conformance

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/kosmassp/SimpleOrm/go/orm"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/testsupport"
	"github.com/kosmassp/SimpleOrm/go/orm/sample"
	"github.com/kosmassp/SimpleOrm/go/orm/sqlite"
)

// caseSpec is the conformance/cases/*.json shape (spec/mapping-rules.md).
type caseSpec struct {
	Name   string `json:"name"`
	Result string `json:"result"`
	Query  string `json:"query"`
	Expect struct {
		Error string          `json:"error"`
		Rows  json.RawMessage `json:"rows"`
	} `json:"expect"`
}

// TestCases_BehaveAsSpecified is the conformance/cases/ runner (§9, mirrors
// ConformanceCaseTests.cs + ConformanceDatabase.cs): per case, a fresh temp
// database built from entity metadata and seeded from
// conformance/fixtures/seed.json; "raw" runs the query through database/sql,
// an entity name runs it through orm.Query and encodes the mapped
// properties per spec/mapping-rules.md; expect.error compares core.CodeOf.
func TestCases_BehaveAsSpecified(t *testing.T) {
	for _, name := range testsupport.ConformanceCases(t, "cases") {
		t.Run(name, func(t *testing.T) {
			var spec caseSpec
			if err := json.Unmarshal(testsupport.ReadConformance(t, "cases", name), &spec); err != nil {
				t.Fatalf("parse %s: %v", name, err)
			}

			ctx := context.Background()
			path := testsupport.TempDatabase(t)
			buildCaseFixture(t, ctx, path)

			if spec.Result == "raw" {
				rows, err := runRawQuery(ctx, path, spec.Query)
				if err != nil {
					t.Fatalf("%s: raw query: %v", name, err)
				}
				assertRowsMatch(t, name, rows, spec.Expect.Rows)
				return
			}

			db, err := orm.Open(ctx, path, orm.Options{Dialect: sqlite.New()})
			if err != nil {
				t.Fatalf("%s: open: %v", name, err)
			}
			defer db.Close()

			rows, queryErr := runEntityQuery(ctx, db, spec.Result, spec.Query)
			if spec.Expect.Error != "" {
				if core.CodeOf(queryErr) != spec.Expect.Error {
					t.Fatalf("%s: expected error %s, got %v", name, spec.Expect.Error, queryErr)
				}
				return
			}
			if queryErr != nil {
				t.Fatalf("%s: unexpected error: %v", name, queryErr)
			}
			assertRowsMatch(t, name, rows, spec.Expect.Rows)
		})
	}
}

// buildCaseFixture creates every fixture table and the UserTransactionTotal
// view from metadata (never hand-written DDL, never the migrations tree — a
// separate, in-flux area of this port), then seeds it from fixtures/seed.json.
func buildCaseFixture(t *testing.T, ctx context.Context, path string) {
	t.Helper()
	db, err := orm.Open(ctx, path, orm.Options{Dialect: sqlite.New()})
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}

	for _, create := range []func(context.Context, *orm.Db) error{
		orm.CreateTable[sample.User],
		orm.CreateTable[sample.Role],
		orm.CreateTable[sample.UserRole],
		orm.CreateTable[sample.UserProfile],
		orm.CreateTable[sample.Transaction],
		orm.CreateTable[sample.TransactionDetail],
	} {
		if err := create(ctx, db); err != nil {
			_ = db.Close()
			t.Fatalf("create fixture table: %v", err)
		}
	}
	if err := orm.CreateView[sample.UserTransactionTotal](ctx, db); err != nil {
		_ = db.Close()
		t.Fatalf("create fixture view: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close fixture: %v", err)
	}

	seedCaseFixture(t, ctx, path)
}

// seedCaseFixture inserts conformance/fixtures/seed.json with plain
// database/sql (JSON number -> int64 when integral else float64, bool -> 0/1,
// string, null) — column names come from the fixture's own keys (trusted test
// data), values always bind as parameters.
func seedCaseFixture(t *testing.T, ctx context.Context, path string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(testsupport.ConformanceFolder(t, "fixtures"), "seed.json"))
	if err != nil {
		t.Fatalf("read seed.json: %v", err)
	}
	var seed map[string][]map[string]any
	if err := json.Unmarshal(data, &seed); err != nil {
		t.Fatalf("parse seed.json: %v", err)
	}

	raw, err := sql.Open(sqlite.DriverName, sqlite.DataSourceName(path))
	if err != nil {
		t.Fatalf("open raw connection: %v", err)
	}
	defer raw.Close()

	tables := make([]string, 0, len(seed))
	for table := range seed {
		tables = append(tables, table)
	}
	sort.Strings(tables) // deterministic order; the generated schema carries no FK constraints to satisfy

	for _, table := range tables {
		for _, row := range seed[table] {
			columns := make([]string, 0, len(row))
			args := make([]any, 0, len(row))
			for column, value := range row {
				columns = append(columns, column)
				args = append(args, sql.Named(column, normalizeSeedValue(value)))
			}
			placeholders := make([]string, len(columns))
			for i, c := range columns {
				placeholders[i] = "@" + c
			}
			insertSQL := "insert into " + table + " (" + strings.Join(columns, ", ") + ") values (" +
				strings.Join(placeholders, ", ") + ")"
			if _, err := raw.ExecContext(ctx, insertSQL, args...); err != nil {
				t.Fatalf("seed insert into %s: %v", table, err)
			}
		}
	}
}

func normalizeSeedValue(value any) any {
	switch v := value.(type) {
	case float64:
		return normalizeJSONNumber(v)
	case bool:
		if v {
			return int64(1)
		}
		return int64(0)
	}
	return value
}

func runRawQuery(ctx context.Context, path, query string) ([]map[string]any, error) {
	raw, err := sql.Open(sqlite.DriverName, sqlite.DataSourceName(path))
	if err != nil {
		return nil, err
	}
	defer raw.Close()

	rows, err := raw.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}

	var results []map[string]any
	for rows.Next() {
		values := make([]any, len(columns))
		scanArgs := make([]any, len(columns))
		for i := range values {
			scanArgs[i] = &values[i]
		}
		if err := rows.Scan(scanArgs...); err != nil {
			return nil, err
		}
		row := make(map[string]any, len(columns))
		for i, column := range columns {
			row[column] = encodeRawCell(values[i])
		}
		results = append(results, row)
	}
	return results, rows.Err()
}

// encodeRawCell is the "raw" result encoding (spec/mapping-rules.md): int64
// and float64 pass through as numbers, []byte as base64, string and nil as-is.
func encodeRawCell(value any) any {
	if b, ok := value.([]byte); ok {
		return base64.StdEncoding.EncodeToString(b)
	}
	return value
}

// runEntityQuery dispatches on the entity name to a generic helper (Go has no
// runtime generic instantiation by name): the seven entity types the case
// fixture maps (mirrors ConformanceDatabase.EntityTypes).
func runEntityQuery(ctx context.Context, db *orm.Db, entityName, query string) ([]map[string]any, error) {
	switch entityName {
	case "User":
		return queryEncoded[sample.User](ctx, db, query)
	case "Role":
		return queryEncoded[sample.Role](ctx, db, query)
	case "UserRole":
		return queryEncoded[sample.UserRole](ctx, db, query)
	case "UserProfile":
		return queryEncoded[sample.UserProfile](ctx, db, query)
	case "Transaction":
		return queryEncoded[sample.Transaction](ctx, db, query)
	case "TransactionDetail":
		return queryEncoded[sample.TransactionDetail](ctx, db, query)
	case "UserTransactionTotal":
		return queryEncoded[sample.UserTransactionTotal](ctx, db, query)
	default:
		return nil, fmt.Errorf("cases_test: unknown entity %q", entityName)
	}
}

func queryEncoded[T any](ctx context.Context, db *orm.Db, query string) ([]map[string]any, error) {
	rows, err := orm.Query(ctx, db, orm.Inline[orm.EmptyArgs, T](query), orm.EmptyArgs{})
	if err != nil {
		return nil, err
	}
	m, err := db.Maps().Load(reflect.TypeFor[T]())
	if err != nil {
		return nil, err
	}
	encoded := make([]map[string]any, len(rows))
	for i := range rows {
		encoded[i] = encodeEntityRow(m, &rows[i])
	}
	return encoded, nil
}

// encodeEntityRow is the documented conformance value encoding
// (spec/mapping-rules.md): every mapped property, keyed by column name.
func encodeEntityRow(m *core.EntityMap, entity any) map[string]any {
	row := make(map[string]any, len(m.Properties))
	for _, p := range m.Properties {
		row[p.ColumnName] = encodeValue(p.Get(entity), p.ColumnType)
	}
	return row
}

func encodeValue(value any, columnType core.ColumnType) any {
	if value == nil {
		return nil
	}
	switch columnType {
	case core.TypeDecimal:
		if d, ok := value.(core.Decimal); ok {
			return d.String()
		}
	case core.TypeGUID:
		if g, ok := value.(core.GUID); ok {
			return g.String()
		}
	case core.TypeDateTime:
		if tm, ok := value.(time.Time); ok {
			return core.FormatUTC(tm)
		}
	case core.TypeDateTimeOffset:
		if tm, ok := value.(time.Time); ok {
			return core.FormatOffset(tm)
		}
	case core.TypeDate:
		if tm, ok := value.(time.Time); ok {
			return core.FormatDate(tm)
		}
	case core.TypeTime:
		if tm, ok := value.(time.Time); ok {
			return core.FormatTime(tm)
		}
	case core.TypeEnumText, core.TypeEnumInt:
		if name, ok := core.EnumName(value); ok {
			return name
		}
	case core.TypeBytes:
		if b, ok := value.([]byte); ok {
			return base64.StdEncoding.EncodeToString(b)
		}
	}
	// Integers, floats, bool, and string pass through: both sides go through
	// an encoding/json round trip before comparison, which normalizes numeric
	// representations identically.
	return value
}

// assertRowsMatch compares actual against expected by deep JSON equality —
// both sides round-trip through encoding/json so numeric encodings converge.
func assertRowsMatch(t *testing.T, name string, actual any, expected json.RawMessage) {
	t.Helper()
	actualBytes, err := json.Marshal(actual)
	if err != nil {
		t.Fatalf("%s: marshal actual rows: %v", name, err)
	}
	var actualAny, expectedAny any
	if err := json.Unmarshal(actualBytes, &actualAny); err != nil {
		t.Fatalf("%s: decode actual rows: %v", name, err)
	}
	if err := json.Unmarshal(expected, &expectedAny); err != nil {
		t.Fatalf("%s: decode expected rows: %v", name, err)
	}
	if !reflect.DeepEqual(actualAny, expectedAny) {
		t.Errorf("%s: rows mismatch\n  actual:   %s\n  expected: %s", name, actualBytes, expected)
	}
}
