package render_test

import (
	"reflect"
	"testing"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/render"
	"github.com/kosmassp/SimpleOrm/go/orm/sample"
	"github.com/kosmassp/SimpleOrm/go/orm/sqlite"
)

// propSpec builds one hand-authored PropertyMap over a real sample field (its
// reflect.StructField and Index — embedded-field promotion included — come
// straight from the struct, exactly as a loader would resolve them).
type propSpec struct {
	name       string
	column     string
	columnType core.ColumnType
	nullable   bool
	key        bool
	generated  bool
	version    bool
}

func buildMap(entityType reflect.Type, relation string, keyStrategy core.KeyStrategy, specs []propSpec) *core.EntityMap {
	properties := make([]*core.PropertyMap, len(specs))
	for i, s := range specs {
		field, ok := entityType.FieldByName(s.name)
		if !ok {
			panic("render_test: no field " + s.name + " on " + entityType.Name())
		}
		properties[i] = &core.PropertyMap{
			Field:         field,
			Index:         field.Index,
			DeclaringType: entityType,
			PropertyName:  s.name,
			ColumnName:    s.column,
			Type:          field.Type,
			ColumnType:    s.columnType,
			IsNullable:    s.nullable,
			IsKey:         s.key,
			IsGenerated:   s.generated,
			IsVersion:     s.version,
		}
	}
	return core.NewEntityMap(entityType, core.RelationTable, relation, "", "", nil, properties, keyStrategy, nil, nil)
}

// userMap mirrors conformance/entities/user.json's column order and the shape
// the task calls out: id, name, email, display_name, created_at, updated_at.
func userMap() *core.EntityMap {
	t := reflect.TypeFor[sample.User]()
	return buildMap(t, "users", core.KeyDatabaseGenerated, []propSpec{
		{name: "ID", column: "id", columnType: core.TypeInt64, key: true, generated: true},
		{name: "Name", column: "name", columnType: core.TypeString},
		{name: "Email", column: "email", columnType: core.TypeString},
		{name: "DisplayName", column: "display_name", columnType: core.TypeString, nullable: true},
		{name: "CreatedAtUtc", column: "created_at", columnType: core.TypeDateTime},
		{name: "UpdatedAtUtc", column: "updated_at", columnType: core.TypeDateTime, nullable: true},
	})
}

// transactionMap mirrors conformance/entities/transaction.json: id, user_id,
// status, amount, version, note, created_at, updated_at.
func transactionMap() *core.EntityMap {
	t := reflect.TypeFor[sample.Transaction]()
	return buildMap(t, "transactions", core.KeyDatabaseGenerated, []propSpec{
		{name: "ID", column: "id", columnType: core.TypeInt64, key: true, generated: true},
		{name: "UserID", column: "user_id", columnType: core.TypeInt64},
		{name: "Status", column: "status", columnType: core.TypeEnumText},
		{name: "Amount", column: "amount", columnType: core.TypeDecimal},
		{name: "Version", column: "version", columnType: core.TypeInt64, version: true},
		{name: "Note", column: "note", columnType: core.TypeString, nullable: true},
		{name: "CreatedAtUtc", column: "created_at", columnType: core.TypeDateTime},
		{name: "UpdatedAtUtc", column: "updated_at", columnType: core.TypeDateTime, nullable: true},
	})
}

// recordingBind returns a bind function that appends every value in render
// order and hands back "@c<index>" — exactly the conformance/ast contract.
func recordingBind() (core.BindCriteriaParameter, *[]any) {
	var bound []any
	return func(value any, _ *core.PropertyMap) (string, error) {
		bound = append(bound, value)
		return "@c" + itoa(len(bound)-1), nil
	}, &bound
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	digits := ""
	neg := i < 0
	if neg {
		i = -i
	}
	for i > 0 {
		digits = string(rune('0'+i%10)) + digits
		i /= 10
	}
	if neg {
		return "-" + digits
	}
	return digits
}

func dialect() core.Dialect { return sqlite.New() }

func TestSelectSQL_SelectAll_NoWhereNoStar(t *testing.T) {
	bind, bound := recordingBind()
	sql, err := render.SelectSQL(dialect(), &core.SelectAst{Map: transactionMap()}, bind)
	if err != nil {
		t.Fatal(err)
	}
	want := "select id, user_id, status, amount, version, note, created_at, updated_at from transactions"
	if sql != want {
		t.Errorf("got %q, want %q", sql, want)
	}
	if len(*bound) != 0 {
		t.Errorf("no parameters expected, got %v", *bound)
	}
}

func TestSelectSQL_ComparisonOperators_ExactTokensAndCaseInsensitiveProperty(t *testing.T) {
	bind, bound := recordingBind()
	ast := &core.SelectAst{
		Map: userMap(),
		Where: []core.Criteria{
			core.And(
				core.Ne("email", "x@example.com"), // lower-case: case-insensitive resolution
				core.Gt("ID", int64(1)),
				core.Lt("ID", int64(9)),
				core.Le("ID", int64(8)),
			),
		},
	}
	sql, err := render.SelectSQL(dialect(), ast, bind)
	if err != nil {
		t.Fatal(err)
	}
	want := "select id, name, email, display_name, created_at, updated_at from users " +
		"where (email <> @c0 and id > @c1 and id < @c2 and id <= @c3)"
	if sql != want {
		t.Errorf("got %q, want %q", sql, want)
	}
	wantParams := []any{"x@example.com", int64(1), int64(9), int64(8)}
	if !equalSlices(*bound, wantParams) {
		t.Errorf("params: got %v, want %v", *bound, wantParams)
	}
}

func TestSelectSQL_EmptyComposites_RenderIdentityTruthValues(t *testing.T) {
	bind, bound := recordingBind()
	ast := &core.SelectAst{
		Map: transactionMap(),
		Where: []core.Criteria{
			core.And(),
			core.Or(),
			core.Not(core.In[int64]("ID")),
		},
	}
	sql, err := render.SelectSQL(dialect(), ast, bind)
	if err != nil {
		t.Fatal(err)
	}
	want := "select id, user_id, status, amount, version, note, created_at, updated_at from transactions " +
		"where (1 = 1 and 1 = 0 and not 1 = 0)"
	if sql != want {
		t.Errorf("got %q, want %q", sql, want)
	}
	if len(*bound) != 0 {
		t.Errorf("no parameters expected, got %v", *bound)
	}
}

func TestSelectSQL_EmptyInList_MatchesNothingButStillResolvesTheProperty(t *testing.T) {
	bind, _ := recordingBind()
	ast := &core.SelectAst{Map: transactionMap(), Where: []core.Criteria{core.In[int64]("ID")}}
	sql, err := render.SelectSQL(dialect(), ast, bind)
	if err != nil {
		t.Fatal(err)
	}
	if want := "select id, user_id, status, amount, version, note, created_at, updated_at from transactions where 1 = 0"; sql != want {
		t.Errorf("got %q, want %q", sql, want)
	}

	// An unknown property is QRY-006 even though the list is empty.
	bind2, _ := recordingBind()
	bad := &core.SelectAst{Map: transactionMap(), Where: []core.Criteria{core.In[int64]("NoSuchProperty")}}
	if _, err := render.SelectSQL(dialect(), bad, bind2); core.CodeOf(err) != "QRY-006" {
		t.Errorf("expected QRY-006, got %v", err)
	}
}

func TestSelectSQL_InListWithNull_IsQRY007(t *testing.T) {
	bind, _ := recordingBind()
	ast := &core.SelectAst{Map: userMap(), Where: []core.Criteria{core.In[any]("DisplayName", "Ada", nil)}}
	if _, err := render.SelectSQL(dialect(), ast, bind); core.CodeOf(err) != "QRY-007" {
		t.Errorf("expected QRY-007, got %v", err)
	}
}

func TestSelectSQL_LikeWithNull_IsQRY007(t *testing.T) {
	// Like's factory takes a string pattern, but the AST node allows a nil
	// value (a caller building it directly, or a dynamic front-end) — build the
	// node directly, as the conformance JSON's "like" op with a null value does.
	bind, _ := recordingBind()
	ast := &core.SelectAst{Map: userMap(), Where: []core.Criteria{&core.Comparison{Property: "Name", Operator: "like", Value: nil}}}
	if _, err := render.SelectSQL(dialect(), ast, bind); core.CodeOf(err) != "QRY-007" {
		t.Errorf("expected QRY-007, got %v", err)
	}
}

func TestSelectSQL_OrderedComparisonWithNull_IsQRY007(t *testing.T) {
	bind, _ := recordingBind()
	ast := &core.SelectAst{Map: userMap(), Where: []core.Criteria{core.Gt("ID", nil)}}
	if _, err := render.SelectSQL(dialect(), ast, bind); core.CodeOf(err) != "QRY-007" {
		t.Errorf("expected QRY-007, got %v", err)
	}
}

func TestSelectSQL_NotLike_PrefixesNegationAndBindsThePattern(t *testing.T) {
	bind, bound := recordingBind()
	ast := &core.SelectAst{Map: userMap(), Where: []core.Criteria{core.Not(core.Like("Email", "%@example.com"))}}
	sql, err := render.SelectSQL(dialect(), ast, bind)
	if err != nil {
		t.Fatal(err)
	}
	want := "select id, name, email, display_name, created_at, updated_at from users where not email like @c0"
	if sql != want {
		t.Errorf("got %q, want %q", sql, want)
	}
	if !equalSlices(*bound, []any{"%@example.com"}) {
		t.Errorf("params: %v", *bound)
	}
}

func TestSelectSQL_NullSemantics_EqNeIsNullIsNotNull(t *testing.T) {
	bind, bound := recordingBind()
	ast := &core.SelectAst{
		Map: userMap(),
		Where: []core.Criteria{
			core.Eq("DisplayName", nil),
			core.Ne("Email", nil),
			core.IsNull("UpdatedAtUtc"),
			core.IsNotNull("Name"),
		},
	}
	sql, err := render.SelectSQL(dialect(), ast, bind)
	if err != nil {
		t.Fatal(err)
	}
	want := "select id, name, email, display_name, created_at, updated_at from users " +
		"where (display_name is null and email is not null and updated_at is null and name is not null)"
	if sql != want {
		t.Errorf("got %q, want %q", sql, want)
	}
	if len(*bound) != 0 {
		t.Errorf("null comparisons never bind a parameter, got %v", *bound)
	}
}

func TestSelectSQL_OrderByUnknownProperty_IsQRY006(t *testing.T) {
	bind, _ := recordingBind()
	ast := &core.SelectAst{Map: userMap(), Orderings: []core.Ordering{{Property: "NoSuchProperty"}}}
	if _, err := render.SelectSQL(dialect(), ast, bind); core.CodeOf(err) != "QRY-006" {
		t.Errorf("expected QRY-006, got %v", err)
	}
}

func TestSelectSQL_UnknownPropertyInPredicate_IsQRY006(t *testing.T) {
	bind, _ := recordingBind()
	ast := &core.SelectAst{Map: userMap(), Where: []core.Criteria{core.Eq("NoSuchProperty", int64(1))}}
	if _, err := render.SelectSQL(dialect(), ast, bind); core.CodeOf(err) != "QRY-006" {
		t.Errorf("expected QRY-006, got %v", err)
	}
}

func TestSelectSQL_AmbiguousCaseInsensitiveProperty_IsQRY006(t *testing.T) {
	// A synthetic map with two properties differing only by case: neither is an
	// exact match for the mixed-case lookup, so both compete case-insensitively.
	entityType := reflect.TypeFor[sample.User]()
	nameField, _ := entityType.FieldByName("Name")
	emailField, _ := entityType.FieldByName("Email")
	m := core.NewEntityMap(entityType, core.RelationTable, "users", "", "", nil, []*core.PropertyMap{
		{Field: nameField, Index: nameField.Index, DeclaringType: entityType, PropertyName: "myprop", ColumnName: "name", Type: nameField.Type, ColumnType: core.TypeString},
		{Field: emailField, Index: emailField.Index, DeclaringType: entityType, PropertyName: "MYPROP", ColumnName: "email", Type: emailField.Type, ColumnType: core.TypeString},
	}, core.KeyNone, nil, nil)

	bind, _ := recordingBind()
	ast := &core.SelectAst{Map: m, Where: []core.Criteria{core.Eq("MyProp", "x")}}
	err := mustSelectError(t, ast, bind)
	if core.CodeOf(err) != "QRY-006" {
		t.Errorf("expected QRY-006, got %v", err)
	}
}

func mustSelectError(t *testing.T, ast *core.SelectAst, bind core.BindCriteriaParameter) error {
	t.Helper()
	_, err := render.SelectSQL(dialect(), ast, bind)
	if err == nil {
		t.Fatal("expected an error")
	}
	return err
}

func TestSelectSQL_NegativeLimitAndOffset_IsQRY008(t *testing.T) {
	bind, _ := recordingBind()
	limit := int64(-5)
	if _, err := render.SelectSQL(dialect(), &core.SelectAst{Map: transactionMap(), Limit: &limit}, bind); core.CodeOf(err) != "QRY-008" {
		t.Errorf("negative limit: expected QRY-008, got %v", err)
	}
	offset := int64(-1)
	if _, err := render.SelectSQL(dialect(), &core.SelectAst{Map: transactionMap(), Offset: &offset}, bind); core.CodeOf(err) != "QRY-008" {
		t.Errorf("negative offset: expected QRY-008, got %v", err)
	}
}

func TestSelectSQL_LimitOnly(t *testing.T) {
	bind, bound := recordingBind()
	limit := int64(3)
	sql, err := render.SelectSQL(dialect(), &core.SelectAst{Map: transactionMap(), Limit: &limit}, bind)
	if err != nil {
		t.Fatal(err)
	}
	want := "select id, user_id, status, amount, version, note, created_at, updated_at from transactions limit @c0"
	if sql != want {
		t.Errorf("got %q, want %q", sql, want)
	}
	if !equalSlices(*bound, []any{int64(3)}) {
		t.Errorf("params: %v", *bound)
	}
}

func TestSelectSQL_OffsetOnly_SQLiteNeedsLimitBeforeOffset(t *testing.T) {
	bind, bound := recordingBind()
	offset := int64(4)
	sql, err := render.SelectSQL(dialect(), &core.SelectAst{Map: transactionMap(), Offset: &offset}, bind)
	if err != nil {
		t.Fatal(err)
	}
	want := "select id, user_id, status, amount, version, note, created_at, updated_at from transactions limit -1 offset @c0"
	if sql != want {
		t.Errorf("got %q, want %q", sql, want)
	}
	if !equalSlices(*bound, []any{int64(4)}) {
		t.Errorf("params: %v", *bound)
	}
}

func TestSelectSQL_WhereOrderPaging_KitchenSink(t *testing.T) {
	bind, bound := recordingBind()
	limit := int64(20)
	offset := int64(5)
	ast := &core.SelectAst{
		Map: userMap(),
		Where: []core.Criteria{
			core.Or(core.Eq("Id", int64(1)), core.In("Name", "Ada", "Grace")),
			core.Ge("CreatedAtUtc", "2026-01-01T00:00:00Z"),
		},
		Orderings: []core.Ordering{{Property: "Name", Order: core.Desc}, {Property: "Id"}},
		Limit:     &limit,
		Offset:    &offset,
	}
	sql, err := render.SelectSQL(dialect(), ast, bind)
	if err != nil {
		t.Fatal(err)
	}
	want := "select id, name, email, display_name, created_at, updated_at from users " +
		"where ((id = @c0 or name in (@c1, @c2)) and created_at >= @c3) " +
		"order by name desc, id limit @c4 offset @c5"
	if sql != want {
		t.Errorf("got %q, want %q", sql, want)
	}
	wantParams := []any{int64(1), "Ada", "Grace", "2026-01-01T00:00:00Z", int64(20), int64(5)}
	if !equalSlices(*bound, wantParams) {
		t.Errorf("params: got %v, want %v", *bound, wantParams)
	}
}

func TestResolve_ExactMatchWinsOverCaseInsensitive(t *testing.T) {
	m := userMap()
	p, err := render.Resolve(m, "ID", "test")
	if err != nil || p.PropertyName != "ID" {
		t.Errorf("exact match: %v %v", p, err)
	}
	p2, err := render.Resolve(m, "id", "test")
	if err != nil || p2.PropertyName != "ID" {
		t.Errorf("case-insensitive fallback: %v %v", p2, err)
	}
	if _, err := render.Resolve(m, "Nope", "test"); core.CodeOf(err) != "QRY-006" {
		t.Errorf("unknown property: %v", err)
	}
}

func equalSlices(a, b []any) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
