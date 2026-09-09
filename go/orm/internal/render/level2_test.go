package render_test

import (
	"testing"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/render"
)

// noRowValueDialect wraps the real SQLite dialect but reports no row-value IN
// support, exercising the correlated-EXISTS rewrite (spec/query-ast.md
// "Subquery membership") that this Go port has no live dialect for yet (only
// SQLite exists here; SupportsRowValueIn is always true) — conformance/ast
// pins only the sqlite row-value expectation, so this is the one place the Go
// port checks the EXISTS branch at all.
type noRowValueDialect struct{ core.Dialect }

func (noRowValueDialect) SupportsRowValueIn() bool { return false }

func fakeDialect() core.Dialect { return noRowValueDialect{dialect()} }

// TestSelectSQL_ProjectionAndJoinsTogether is not one of the seven
// conformance/ast/level2 cases (none combine the two): a Projection narrows
// and orders the root's own columns, which still re-alias t_<column> once
// joins are present, and the join's columns follow in declaration order.
func TestSelectSQL_ProjectionAndJoinsTogether(t *testing.T) {
	root := userMap()
	queryName := root.EntityName() + " criteria"
	idProp, err := render.Resolve(root, "ID", queryName)
	if err != nil {
		t.Fatal(err)
	}
	nameProp, err := render.Resolve(root, "Name", queryName)
	if err != nil {
		t.Fatal(err)
	}

	ast := &core.SelectAst{
		Map:        root,
		Projection: []*core.PropertyMap{idProp, nameProp},
		Joins: []*core.SelectJoin{
			{
				Target:  transactionMap(),
				Alias:   "j0",
				On:      []core.JoinPair{{ParentProperty: "ID", TargetProperty: "UserID"}},
				Project: true,
			},
		},
	}

	bind, bound := recordingBind()
	sql, err := render.SelectSQL(dialect(), ast, bind)
	if err != nil {
		t.Fatal(err)
	}
	want := "select t.id as t_id, t.name as t_name, " +
		"j0.id as j0_id, j0.user_id as j0_user_id, j0.status as j0_status, j0.amount as j0_amount, " +
		"j0.version as j0_version, j0.note as j0_note, j0.created_at as j0_created_at, j0.updated_at as j0_updated_at " +
		"from users t left join transactions j0 on j0.user_id = t.id"
	if sql != want {
		t.Errorf("got  %q\nwant %q", sql, want)
	}
	if len(*bound) != 0 {
		t.Errorf("no parameters expected, got %v", *bound)
	}
}

// TestSelectSQL_JoinUnprojected_ContributesNoColumns is the many-to-many link
// shape in isolation: a join with Project: false renders its "left join …
// on …" but no columns, regardless of the root's own aliasing.
func TestSelectSQL_JoinUnprojected_ContributesNoColumns(t *testing.T) {
	ast := &core.SelectAst{
		Map: userMap(),
		Joins: []*core.SelectJoin{
			{Target: transactionMap(), Alias: "l0", On: []core.JoinPair{{ParentProperty: "ID", TargetProperty: "UserID"}}, Project: false},
		},
	}
	bind, _ := recordingBind()
	sql, err := render.SelectSQL(dialect(), ast, bind)
	if err != nil {
		t.Fatal(err)
	}
	want := "select t.id as t_id, t.name as t_name, t.email as t_email, t.display_name as t_display_name, " +
		"t.created_at as t_created_at, t.updated_at as t_updated_at " +
		"from users t left join transactions l0 on l0.user_id = t.id"
	if sql != want {
		t.Errorf("got  %q\nwant %q", sql, want)
	}
}

// TestSelectSQL_JoinOnUnknownParentAlias_IsQRY006 is the defensive branch
// spec/query-ast.md does not name a case for: a join declaring "parent" as an
// alias no earlier join declared.
func TestSelectSQL_JoinOnUnknownParentAlias_IsQRY006(t *testing.T) {
	ast := &core.SelectAst{
		Map: userMap(),
		Joins: []*core.SelectJoin{
			{Target: transactionMap(), Alias: "j0", ParentAlias: "nope", On: []core.JoinPair{{ParentProperty: "ID", TargetProperty: "UserID"}}, Project: true},
		},
	}
	bind, _ := recordingBind()
	if _, err := render.SelectSQL(dialect(), ast, bind); core.CodeOf(err) != "QRY-006" {
		t.Errorf("expected QRY-006, got %v", err)
	}
}

// TestSelectSQL_JoinUnknownParentProperty_IsQRY006 mirrors
// conformance/ast/level2/join_unknown_property_refused.json but on the
// PARENT side of the pair (the pinned case only exercises the target side).
func TestSelectSQL_JoinUnknownParentProperty_IsQRY006(t *testing.T) {
	ast := &core.SelectAst{
		Map: userMap(),
		Joins: []*core.SelectJoin{
			{Target: transactionMap(), Alias: "j0", On: []core.JoinPair{{ParentProperty: "NoSuchProperty", TargetProperty: "UserID"}}, Project: true},
		},
	}
	bind, _ := recordingBind()
	if _, err := render.SelectSQL(dialect(), ast, bind); core.CodeOf(err) != "QRY-006" {
		t.Errorf("expected QRY-006, got %v", err)
	}
}

// TestSelectSQL_CompositeMembership_ExistsRewrite_NestedInOrAndNot exercises
// the branch conformance/ast/level2/subselect_composite_membership.json pins
// only for the SQL Server *expectation* (this port has no such live dialect):
// a composite in_select on a dialect without row-value IN rewrites as a
// correlated EXISTS, the root gains alias "t" WITHOUT re-aliasing its own
// columns (only joins do that), and every root column reference in the same
// select — including an unrelated sibling predicate — picks up the "t."
// qualifier once the alias exists. The membership node sits two levels deep
// (inside Not, inside Or) to exercise requiresExistsRewrite's recursion
// through Composite and Negation.
func TestSelectSQL_CompositeMembership_ExistsRewrite_NestedInOrAndNot(t *testing.T) {
	subUserMap := userMap()
	idProp, err := render.Resolve(subUserMap, "ID", "User criteria")
	if err != nil {
		t.Fatal(err)
	}
	nameProp, err := render.Resolve(subUserMap, "Name", "User criteria")
	if err != nil {
		t.Fatal(err)
	}
	subquery := &core.SelectAst{Map: subUserMap, Projection: []*core.PropertyMap{idProp, nameProp}}

	membership := &core.SubqueryMembership{Properties: []string{"ID", "UserID"}, Subquery: subquery}
	ast := &core.SelectAst{
		Map: transactionMap(),
		Where: []core.Criteria{
			core.Or(core.Not(membership), core.Eq("Status", "Pending")),
		},
	}

	bind, bound := recordingBind()
	sql, err := render.SelectSQL(fakeDialect(), ast, bind)
	if err != nil {
		t.Fatal(err)
	}
	want := "select t.id, t.user_id, t.status, t.amount, t.version, t.note, t.created_at, t.updated_at from transactions t " +
		"where (not exists (select 1 from (select id, name from users) s where s.id = t.id and s.name = t.user_id) or t.status = @c0)"
	if sql != want {
		t.Errorf("got  %q\nwant %q", sql, want)
	}
	if !equalSlices(*bound, []any{"Pending"}) {
		t.Errorf("params: got %v, want [Pending]", *bound)
	}
}

// TestSelectSQL_SingleColumnMembership_NeverAliasesTheRoot confirms that a
// single-property in_select never triggers the EXISTS rewrite (row-value IN
// only matters for a composite), even on a dialect without row-value support —
// matching conformance/ast/level2/subselect_membership.json's identical
// sqlite/sqlserver/postgres rendering.
func TestSelectSQL_SingleColumnMembership_NeverAliasesTheRoot(t *testing.T) {
	subquery := &core.SelectAst{Map: userMap(), Where: []core.Criteria{core.Eq("Name", "Ada")}}
	ast := &core.SelectAst{
		Map:   transactionMap(),
		Where: []core.Criteria{core.InSelect([]string{"UserID"}, subquery)},
	}
	bind, bound := recordingBind()
	sql, err := render.SelectSQL(fakeDialect(), ast, bind)
	if err != nil {
		t.Fatal(err)
	}
	want := "select id, user_id, status, amount, version, note, created_at, updated_at from transactions " +
		"where user_id in (select id, name, email, display_name, created_at, updated_at from users where name = @c0)"
	if sql != want {
		t.Errorf("got  %q\nwant %q", sql, want)
	}
	if !equalSlices(*bound, []any{"Ada"}) {
		t.Errorf("params: got %v, want [Ada]", *bound)
	}
}
