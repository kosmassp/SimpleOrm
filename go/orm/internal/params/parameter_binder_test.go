package params_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/mapping"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/params"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/testsupport"
	"github.com/kosmassp/SimpleOrm/go/orm/sqlite"
)

func newConverter() *mapping.TypeConverter {
	return mapping.NewTypeConverter(core.NewTypeHandlerRegistry(), false)
}

type probeArgs struct{ ID int64 }

type idsArgs struct{ Ids []int64 }

func TestBind_PlaceholderWithoutAMatchingFieldIsPRM001(t *testing.T) {
	_, err := params.Bind("select * from users where id = @Nope", probeArgs{ID: 1}, newConverter(), sqlite.New(), "q")
	if core.CodeOf(err) != "PRM-001" {
		t.Errorf("expected PRM-001, got %v", err)
	}
}

func TestBind_FieldNeverUsedByTheSQLIsPRM002(t *testing.T) {
	_, err := params.Bind("select * from users", probeArgs{ID: 1}, newConverter(), sqlite.New(), "q")
	if core.CodeOf(err) != "PRM-002" {
		t.Errorf("expected PRM-002, got %v", err)
	}
}

func TestBind_MatchingPlaceholderBindsCaseInsensitively(t *testing.T) {
	bound, err := params.Bind("select * from users where id = @id", probeArgs{ID: 7}, newConverter(), sqlite.New(), "q")
	if err != nil {
		t.Fatal(err)
	}
	if bound.SQL != "select * from users where id = @id" {
		t.Errorf("SQL should be untouched for a scalar bind: %q", bound.SQL)
	}
	if len(bound.Args) != 1 {
		t.Fatalf("expected one bound arg, got %d", len(bound.Args))
	}
	arg := bound.Args[0].(sql.NamedArg)
	if arg.Name != "id" || arg.Value != int64(7) {
		t.Errorf("got %+v", arg)
	}
}

func TestBind_CollectionExpandsToGeneratedPlaceholders(t *testing.T) {
	bound, err := params.Bind("select * from users where id in (@Ids)", idsArgs{Ids: []int64{1, 2, 3}}, newConverter(), sqlite.New(), "q")
	if err != nil {
		t.Fatal(err)
	}
	if bound.SQL != "select * from users where id in (@Ids_0, @Ids_1, @Ids_2)" {
		t.Errorf("got %q", bound.SQL)
	}
	if len(bound.Args) != 3 {
		t.Fatalf("expected 3 bound args, got %d", len(bound.Args))
	}
	for i, want := range []int64{1, 2, 3} {
		arg := bound.Args[i].(sql.NamedArg)
		if arg.Name != "Ids_"+itoa(i) || arg.Value != want {
			t.Errorf("arg %d: %+v", i, arg)
		}
	}
}

func TestBind_EmptyCollectionRewritesToNULLAndBindsNothing(t *testing.T) {
	bound, err := params.Bind("select * from users where id in (@Ids)", idsArgs{Ids: nil}, newConverter(), sqlite.New(), "q")
	if err != nil {
		t.Fatal(err)
	}
	if bound.SQL != "select * from users where id in (NULL)" {
		t.Errorf("got %q", bound.SQL)
	}
	if len(bound.Args) != 0 {
		t.Errorf("expected no bound args, got %v", bound.Args)
	}
}

func TestBind_ExpansionLeavesALookalikeInsideALiteralOrCommentAlone(t *testing.T) {
	sqlText := "select * from users where email like '%@ids' -- narrows by @ids\n and id in (@ids)"
	bound, err := params.Bind(sqlText, idsArgs{Ids: []int64{9}}, newConverter(), sqlite.New(), "q")
	if err != nil {
		t.Fatal(err)
	}
	want := "select * from users where email like '%@ids' -- narrows by @ids\n and id in (@ids_0)"
	if bound.SQL != want {
		t.Errorf("got %q, want %q", bound.SQL, want)
	}
	if len(bound.Args) != 1 {
		t.Fatalf("expected one bound arg, got %d", len(bound.Args))
	}
}

type stringArgs struct{ Name string }

func TestBind_AStringValueIsNeverTreatedAsACollection(t *testing.T) {
	bound, err := params.Bind(
		"select * from users where name = @name and email like '%domain%'",
		stringArgs{Name: "Ada"}, newConverter(), sqlite.New(), "q")
	if err != nil {
		t.Fatal(err)
	}
	if len(bound.Args) != 1 {
		t.Fatalf("expected one bound arg, got %d", len(bound.Args))
	}
	arg := bound.Args[0].(sql.NamedArg)
	if arg.Name != "name" || arg.Value != "Ada" {
		t.Errorf("got %+v", arg)
	}
}

type bytesArgs struct{ Blob []byte }

func TestBind_AByteSliceIsNeverTreatedAsACollection(t *testing.T) {
	bound, err := params.Bind("select @Blob", bytesArgs{Blob: []byte{1, 2, 3}}, newConverter(), sqlite.New(), "q")
	if err != nil {
		t.Fatal(err)
	}
	if len(bound.Args) != 1 {
		t.Fatalf("expected one bound arg (the whole blob), got %d", len(bound.Args))
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	digits := ""
	for i > 0 {
		digits = string(rune('0'+i%10)) + digits
		i /= 10
	}
	return digits
}

// TestBind_RoundTripsAgainstARealSQLiteDatabase mirrors DbParameterTests.cs:
// a real temp-file database, inserted rows through bound parameters, an IN
// list narrowing a select, and a value that would break a concatenated
// statement.
func TestBind_RoundTripsAgainstARealSQLiteDatabase(t *testing.T) {
	ctx := context.Background()
	dialect := sqlite.New()
	pool, err := dialect.CreateConnection(testsupport.TempDatabase(t))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	if _, err := pool.ExecContext(ctx, "create table users (id INTEGER PRIMARY KEY, name TEXT NOT NULL) STRICT"); err != nil {
		t.Fatal(err)
	}

	converter := newConverter()
	type insertArgs struct{ Name string }
	names := []string{"Ada", "Grace", "O'Brien; drop table users; --"}
	var ids []int64
	for _, name := range names {
		bound, err := params.Bind("insert into users (name) values (@Name) returning id", insertArgs{Name: name}, converter, dialect, "insert")
		if err != nil {
			t.Fatal(err)
		}
		var id int64
		if err := pool.QueryRowContext(ctx, bound.SQL, bound.Args...).Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}

	countBound, err := params.Bind("select count(id) from users where id in (@Ids)", idsArgs{Ids: []int64{ids[0], ids[2]}}, converter, dialect, "count")
	if err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := pool.QueryRowContext(ctx, countBound.SQL, countBound.Args...).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Errorf("expected 2 matching rows, got %d", count)
	}

	var storedName string
	if err := pool.QueryRowContext(ctx, "select name from users where id = ?", ids[2]).Scan(&storedName); err != nil {
		t.Fatal(err)
	}
	if storedName != names[2] {
		t.Errorf("the malicious-looking string binds as data, not SQL: got %q", storedName)
	}
}
