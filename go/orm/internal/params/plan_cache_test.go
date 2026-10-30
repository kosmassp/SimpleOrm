package params

import (
	"database/sql"
	"reflect"
	"testing"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/mapping"
	"github.com/kosmassp/SimpleOrm/go/orm/sqlite"
)

type twoListsArgs struct {
	Status string
	Ids    []int64
	Names  []string
}

// The binding plan is computed once per (SQL text, query name, args type) and reused (ADR-0048 item 2); what each
// call binds still comes from that call's values — two IN lists of different lengths per call expand correctly, the
// second rescanning the text the first moved.
func TestBind_ThePlanIsBuiltOncePerTextAndTypeAndEveryCallExpandsItsOwnValues(t *testing.T) {
	const text = "select id from users where status = @Status and id in (@Ids) and name in (@Names) or id in (@Ids)"
	argsType := reflect.TypeFor[twoListsArgs]()
	first := planFor(text, "plan-cache", argsType)
	if planFor(text, "plan-cache", argsType) != first {
		t.Fatal("the plan was built again for the same text and type")
	}
	if planFor(text, "plan-cache other", argsType) == first {
		t.Fatal("a different query name shares a plan whose error contexts name another query")
	}

	converter := mapping.NewTypeConverter(core.NewTypeHandlerRegistry(), false)
	for _, scenario := range []struct {
		args twoListsArgs
		sql  string
	}{
		{twoListsArgs{Status: "a", Ids: []int64{1, 2}, Names: []string{"x"}},
			"select id from users where status = @Status and id in (@Ids_0, @Ids_1) and name in (@Names_0) or id in (@Ids_0, @Ids_1)"},
		{twoListsArgs{Status: "b", Ids: []int64{3}, Names: []string{"y", "z", "w"}},
			"select id from users where status = @Status and id in (@Ids_0) and name in (@Names_0, @Names_1, @Names_2) or id in (@Ids_0)"},
		{twoListsArgs{Status: "c", Ids: nil, Names: []string{"v"}},
			"select id from users where status = @Status and id in (NULL) and name in (@Names_0) or id in (NULL)"},
	} {
		bound, err := Bind(text, scenario.args, converter, sqlite.New(), "plan-cache")
		if err != nil {
			t.Fatal(err)
		}
		if bound.SQL != scenario.sql {
			t.Errorf("got  %q\nwant %q", bound.SQL, scenario.sql)
		}
		if bound.Args[0].(sql.NamedArg).Value != scenario.args.Status {
			t.Errorf("the scalar bound %v, want %v", bound.Args[0], scenario.args.Status)
		}
		if want := 1 + len(scenario.args.Ids) + len(scenario.args.Names); len(bound.Args) != want {
			t.Errorf("bound %d args, want %d", len(bound.Args), want)
		}
	}

	// The cached verdicts still name the call's query.
	if _, err := Bind("select 1 where 1 = @Missing", twoListsArgs{}, converter, sqlite.New(), "q1"); core.CodeOf(err) != "PRM-001" {
		t.Errorf("expected PRM-001, got %v", err)
	}
	if _, err := Bind("select 1 where 1 = @Missing", twoListsArgs{}, converter, sqlite.New(), "q1"); core.CodeOf(err) != "PRM-001" {
		t.Errorf("expected PRM-001 from the cached plan, got %v", err)
	}
	if _, err := Bind("select 1 where 1 = @Status", twoListsArgs{}, converter, sqlite.New(), "q2"); core.CodeOf(err) != "PRM-002" {
		t.Errorf("expected PRM-002 from the cached plan, got %v", err)
	}
}
