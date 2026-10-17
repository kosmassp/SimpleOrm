package sqlserver_test

import (
	"reflect"
	"testing"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/metadata"
	"github.com/kosmassp/SimpleOrm/go/orm/sample"
	"github.com/kosmassp/SimpleOrm/go/orm/sqlserver"
)

// Rendering tests for every string member of sqlserver.Dialect, against
// hand-built maps mirroring the fixture entities (no database needed) — the
// expected strings are the ones spec/dialects.md states. Test-local fixture
// types only (CODING-STANDARD §7: never in sample), except the version-column
// case, which loads sample.Transaction.

type user struct {
	ID          int64
	Name        string
	DisplayName *string
}

type userRole struct {
	UserID    int64
	RoleID    int64
	GrantedBy *string
}

type widget struct {
	ID   core.GUID
	Note string
}

type tag struct {
	Name  string
	Label string
	Blob  []byte
}

func propSpec(t reflect.Type, name, column string, columnType core.ColumnType, nullable, key, generated, version bool) *core.PropertyMap {
	field, ok := t.FieldByName(name)
	if !ok {
		panic("sqlserver_test: no field " + name + " on " + t.Name())
	}
	return &core.PropertyMap{
		Field: field, Index: field.Index, DeclaringType: t,
		PropertyName: name, ColumnName: column, Type: field.Type, ColumnType: columnType,
		IsNullable: nullable, IsKey: key, IsGenerated: generated, IsVersion: version,
	}
}

func usersMap() *core.EntityMap {
	t := reflect.TypeFor[user]()
	return core.NewEntityMap(t, core.RelationTable, "users", "", "", nil, []*core.PropertyMap{
		propSpec(t, "ID", "id", core.TypeInt64, false, true, true, false),
		propSpec(t, "Name", "name", core.TypeString, false, false, false, false),
		propSpec(t, "DisplayName", "display_name", core.TypeString, true, false, false, false),
	}, core.KeyDatabaseGenerated, nil, nil)
}

func userRolesMap() *core.EntityMap {
	t := reflect.TypeFor[userRole]()
	return core.NewEntityMap(t, core.RelationTable, "user_roles", "", "", nil, []*core.PropertyMap{
		propSpec(t, "UserID", "user_id", core.TypeInt64, false, true, false, false),
		propSpec(t, "RoleID", "role_id", core.TypeInt64, false, true, false, false),
		propSpec(t, "GrantedBy", "granted_by", core.TypeString, true, false, false, false),
	}, core.KeyNatural, nil, nil)
}

func widgetsMap() *core.EntityMap {
	t := reflect.TypeFor[widget]()
	return core.NewEntityMap(t, core.RelationTable, "widgets", "", "", nil, []*core.PropertyMap{
		propSpec(t, "ID", "id", core.TypeGUID, false, true, false, false),
		propSpec(t, "Note", "note", core.TypeString, false, false, false, false),
	}, core.KeyClientGuid, nil, nil)
}

// tagsMap is a natural string key (the indexable 450) beside a plain string and a blob.
func tagsMap() *core.EntityMap {
	t := reflect.TypeFor[tag]()
	return core.NewEntityMap(t, core.RelationTable, "tags", "", "", nil, []*core.PropertyMap{
		propSpec(t, "Name", "name", core.TypeString, false, true, false, false),
		propSpec(t, "Label", "label", core.TypeString, false, false, false, false),
		propSpec(t, "Blob", "blob", core.TypeBytes, true, false, false, false),
	}, core.KeyNatural, nil, nil)
}

func transactionMap(t *testing.T) *core.EntityMap {
	t.Helper()
	m, err := metadata.NewLoader(nil).Load(reflect.TypeFor[sample.Transaction]())
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestCreateTableSQL_DatabaseGeneratedKeyIsAnIdentityPrimaryKey(t *testing.T) {
	got := sqlserver.New().CreateTableSQL(usersMap())
	want := "if object_id(N'users', N'U') is null\n" +
		"create table [users] (\n" +
		"    [id] bigint identity(1,1) primary key,\n" +
		"    [name] nvarchar(max) not null,\n" +
		"    [display_name] nvarchar(max)\n" +
		")"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestCreateTableSQL_ClientGuidKeyMarksThatColumnPrimaryKey(t *testing.T) {
	got := sqlserver.New().CreateTableSQL(widgetsMap())
	want := "if object_id(N'widgets', N'U') is null\n" +
		"create table [widgets] (\n" +
		"    [id] uniqueidentifier not null primary key,\n" +
		"    [note] nvarchar(max) not null\n" +
		")"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestCreateTableSQL_NaturalCompositeKeyAppendsTrailingPrimaryKey(t *testing.T) {
	got := sqlserver.New().CreateTableSQL(userRolesMap())
	want := "if object_id(N'user_roles', N'U') is null\n" +
		"create table [user_roles] (\n" +
		"    [user_id] bigint not null,\n" +
		"    [role_id] bigint not null,\n" +
		"    [granted_by] nvarchar(max),\n" +
		"    primary key ([user_id], [role_id])\n" +
		")"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestCreateTableSQL_KeyAndIndexedStringColumnsTakeTheIndexableLength(t *testing.T) {
	m := tagsMap()
	m.Indexes = []*core.EntityIndex{{Name: "ix_tags_label", Columns: []core.IndexColumn{{ColumnName: "label"}}}}
	got := sqlserver.New().CreateTableSQL(m)
	want := "if object_id(N'tags', N'U') is null\n" +
		"create table [tags] (\n" +
		"    [name] nvarchar(450) not null,\n" +
		"    [label] nvarchar(450) not null,\n" +
		"    [blob] varbinary(max),\n" +
		"    primary key ([name])\n" +
		")"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestCreateTableSQL_ASingleQuoteInTheNameIsDoubledInTheGuard(t *testing.T) {
	m := widgetsMap()
	m.RelationName = "it's"
	got := sqlserver.New().CreateTableSQL(m)
	want := "if object_id(N'it''s', N'U') is null\ncreate table [it's] ("
	if len(got) < len(want) || got[:len(want)] != want {
		t.Errorf("got:\n%s\nwant prefix:\n%s", got, want)
	}
}

func TestCreateIndexSQL_UniqueAndDescendingColumnsAreGuardedByTheCatalog(t *testing.T) {
	m := usersMap()
	m.Indexes = []*core.EntityIndex{
		{Name: "ix_users_name", Columns: []core.IndexColumn{{ColumnName: "name"}}, Unique: true},
		{Name: "ix_users_id_name", Columns: []core.IndexColumn{{ColumnName: "id", Descending: true}, {ColumnName: "name"}}},
	}
	got := sqlserver.New().CreateIndexSQL(m)
	want := []string{
		"if not exists (select 1 from sys.indexes where name = N'ix_users_name' and object_id = object_id(N'users'))\n" +
			"create unique index [ix_users_name] on [users] ([name])",
		"if not exists (select 1 from sys.indexes where name = N'ix_users_id_name' and object_id = object_id(N'users'))\n" +
			"create index [ix_users_id_name] on [users] ([id] desc, [name])",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got:\n%q\nwant:\n%q", got, want)
	}
}

func TestCreateViewSQL_IsCreateOrAlter(t *testing.T) {
	viewType := reflect.TypeFor[struct{ SalesMonth string }]()
	m := core.NewEntityMap(viewType, core.RelationView, "monthly_totals", "", "select 1 as sales_month", nil, nil, core.KeyNone, nil, nil)
	got := sqlserver.New().CreateViewSQL(m)
	want := "create or alter view [monthly_totals] as\nselect 1 as sales_month"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestInsertSQL_DatabaseGeneratedKeyIsReadWithScopeIdentity(t *testing.T) {
	got := sqlserver.New().InsertSQL(usersMap())
	want := "insert into [users] ([name], [display_name]) values (@name, @display_name); select cast(scope_identity() as bigint)"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestInsertSQL_NaturalKeyHasNoKeyReadBack(t *testing.T) {
	got := sqlserver.New().InsertSQL(userRolesMap())
	want := "insert into [user_roles] ([user_id], [role_id], [granted_by]) values (@user_id, @role_id, @granted_by)"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestUpdateSQL_VersionIncrementsAndGuardsTheWhereClause(t *testing.T) {
	got := sqlserver.New().UpdateSQL(transactionMap(t))
	want := "update [transactions] set [user_id] = @user_id, [status] = @status, [amount] = @amount, [note] = @note, " +
		"[created_at] = @created_at, [updated_at] = @updated_at, [version] = [version] + 1 " +
		"where [id] = @id and [version] = @version"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestUpdateOnlySQL_RendersExactlyTheListedColumnsWithTheVersionRules(t *testing.T) {
	d := sqlserver.New()
	transactions := transactionMap(t)
	got := d.UpdateOnlySQL(transactions, []*core.PropertyMap{transactions.Property("Status")})
	want := "update [transactions] set [status] = @status, [version] = [version] + 1 where [id] = @id and [version] = @version"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}

	users := usersMap()
	var all []*core.PropertyMap
	for _, p := range users.Properties {
		if !p.IsKey && !p.IsVersion && !p.IsGenerated {
			all = append(all, p)
		}
	}
	if full, listed := d.UpdateSQL(users), d.UpdateOnlySQL(users, all); full != listed { // one renderer
		t.Errorf("full %q, listed %q", full, listed)
	}
}

func TestUpdateSQL_ExcludesKeyVersionAndGeneratedColumns(t *testing.T) {
	got := sqlserver.New().UpdateSQL(usersMap())
	want := "update [users] set [name] = @name, [display_name] = @display_name where [id] = @id"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestDeleteSQL_PlainAndVersionCheckedForms(t *testing.T) {
	d := sqlserver.New()
	transactions := transactionMap(t)
	if got, want := d.DeleteSQL(transactions, false), "delete from [transactions] where [id] = @id"; got != want {
		t.Errorf("plain: got %q, want %q", got, want)
	}
	if got, want := d.DeleteSQL(transactions, true), "delete from [transactions] where [id] = @id and [version] = @version"; got != want {
		t.Errorf("version-checked: got %q, want %q", got, want)
	}
	// checkVersion on an unversioned entity is a no-op: no version to add.
	if got, want := d.DeleteSQL(usersMap(), true), "delete from [users] where [id] = @id"; got != want {
		t.Errorf("no version column: got %q, want %q", got, want)
	}
	if got, want := d.DeleteSQL(userRolesMap(), false), "delete from [user_roles] where [user_id] = @user_id and [role_id] = @role_id"; got != want {
		t.Errorf("composite key: got %q, want %q", got, want)
	}
}

func TestLimitOffsetClause_ThreeForms(t *testing.T) {
	d := sqlserver.New()
	if got, want := d.LimitOffsetClause("@c4", "@c5"), "offset @c5 rows fetch next @c4 rows only"; got != want {
		t.Errorf("both: got %q, want %q", got, want)
	}
	if got, want := d.LimitOffsetClause("@c4", ""), "offset 0 rows fetch next @c4 rows only"; got != want {
		t.Errorf("limit only: got %q, want %q", got, want)
	}
	if got, want := d.LimitOffsetClause("", "@c5"), "offset @c5 rows"; got != want {
		t.Errorf("offset only: got %q, want %q", got, want)
	}
	if got, want := d.LimitOffsetClause("", ""), ""; got != want {
		t.Errorf("neither: got %q, want %q", got, want)
	}
}

func TestMigrationActionDDL(t *testing.T) {
	d := sqlserver.New()
	if got, want := d.RenameTableSQL("old_name", "new_name"), "exec sp_rename N'old_name', N'new_name'"; got != want {
		t.Errorf("RenameTableSQL: got %q, want %q", got, want)
	}
	if got, want := d.RenameColumnSQL("users", "old_col", "new_col"), "exec sp_rename N'users.old_col', N'new_col', 'COLUMN'"; got != want {
		t.Errorf("RenameColumnSQL: got %q, want %q", got, want)
	}
	if got, want := d.RenameTableSQL("o'brien", "x"), "exec sp_rename N'o''brien', N'x'"; got != want {
		t.Errorf("RenameTableSQL quote doubling: got %q, want %q", got, want)
	}
	if got, want := d.AddColumnSQL("users", "note", "nvarchar(max)", true, ""), "alter table [users] add [note] nvarchar(max)"; got != want {
		t.Errorf("AddColumnSQL nullable no default: got %q, want %q", got, want)
	}
	if got, want := d.AddColumnSQL("users", "note", "nvarchar(max)", false, ""), "alter table [users] add [note] nvarchar(max) not null"; got != want {
		t.Errorf("AddColumnSQL not null no default: got %q, want %q", got, want)
	}
	if got, want := d.AddColumnSQL("users", "note", "nvarchar(max)", false, "N'x'"), "alter table [users] add [note] nvarchar(max) not null default N'x'"; got != want {
		t.Errorf("AddColumnSQL not null with default: got %q, want %q", got, want)
	}
	if got, want := d.DropColumnSQL("users", "note"), "alter table [users] drop column [note]"; got != want {
		t.Errorf("DropColumnSQL: got %q, want %q", got, want)
	}
	if got, want := d.DropTableSQL("users"), "drop table [users]"; got != want {
		t.Errorf("DropTableSQL: got %q, want %q", got, want)
	}
	if got, want := d.DropIndexSQL("users", "ix_users_name"), "drop index [ix_users_name] on [users]"; got != want {
		t.Errorf("DropIndexSQL: got %q, want %q", got, want)
	}
}

func TestVersionTableSQL(t *testing.T) {
	got := sqlserver.New().VersionTableSQL()
	want := "if object_id(N'schema_version', N'U') is null\n" +
		"create table schema_version (\n" +
		"    version      bigint        not null,\n" +
		"    object       nvarchar(200) not null,\n" +
		"    description  nvarchar(400) not null,\n" +
		"    checksum     nvarchar(64)  not null,\n" +
		"    applied_at   nvarchar(40)  not null,\n" +
		"    execution_ms bigint        not null,\n" +
		"    primary key (version, object)\n" +
		")"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestQuoteIdentifier_BracketsAndDoublesTheClosingBracket(t *testing.T) {
	d := sqlserver.New()
	for name, want := range map[string]string{
		"order":  "[order]",
		"desc":   "[desc]",
		"a]b":    "[a]]b]",
		"a b":    "[a b]",
		"plain1": "[plain1]",
	} {
		if got := d.QuoteIdentifier(name); got != want {
			t.Errorf("QuoteIdentifier(%q): got %q, want %q", name, got, want)
		}
	}
}

func TestStorageType_EveryToken(t *testing.T) {
	d := sqlserver.New()
	cases := []struct {
		token core.ColumnType
		key   bool
		want  string
	}{
		{core.TypeInt16, false, "smallint"},
		{core.TypeInt32, false, "int"},
		{core.TypeInt64, false, "bigint"},
		{core.TypeBool, false, "bit"},
		{core.TypeDecimal, false, "decimal(38, 9)"},
		{core.TypeDouble, false, "float"},
		{core.TypeFloat, false, "real"},
		{core.TypeString, false, "nvarchar(max)"},
		{core.TypeString, true, "nvarchar(450)"},
		{core.TypeCustom, false, "nvarchar(max)"},
		{core.TypeCustom, true, "nvarchar(450)"},
		{core.TypeGUID, false, "uniqueidentifier"},
		{core.TypeGUID, true, "uniqueidentifier"},
		{core.TypeBytes, false, "varbinary(max)"},
		{core.TypeBytes, true, "varbinary(450)"},
		{core.TypeDateTime, false, "datetimeoffset"},
		{core.TypeDateTimeOffset, false, "datetimeoffset"},
		{core.TypeDate, false, "date"},
		{core.TypeTime, false, "time"},
		{core.TypeEnumText, false, "nvarchar(100)"},
		{core.TypeEnumText, true, "nvarchar(100)"},
		{core.TypeEnumInt, false, "int"},
	}
	for _, c := range cases {
		got := d.StorageType(&core.PropertyMap{ColumnType: c.token, IsKey: c.key})
		if got != c.want {
			t.Errorf("StorageType(%s, key=%v): got %q, want %q", c.token, c.key, got, c.want)
		}
	}
}

func TestIsDeclaredTypeCompatible(t *testing.T) {
	d := sqlserver.New()
	cases := []struct {
		declared string
		token    core.ColumnType
		want     bool
	}{
		// An empty declared type contradicts everything on SQL Server.
		{"", core.TypeInt64, false},
		{"", core.TypeString, false},
		{"int", core.TypeInt16, true},
		{"int", core.TypeInt32, true},
		{"INT", core.TypeInt64, true},
		{"tinyint", core.TypeInt32, true},
		{"int", core.TypeEnumInt, true},
		{"int", core.TypeString, false},
		{"bigint", core.TypeInt64, true},
		{"bigint", core.TypeInt32, false},
		{"bigint", core.TypeEnumInt, true},
		{"smallint", core.TypeInt16, true},
		{"smallint", core.TypeInt64, true},
		{"bit", core.TypeBool, true},
		{"bit", core.TypeInt32, false},
		{"decimal(18,2)", core.TypeDecimal, true},
		{"numeric(10, 4)", core.TypeDecimal, true},
		{"money", core.TypeDecimal, true},
		{"smallmoney", core.TypeDecimal, true},
		{"decimal(18,2)", core.TypeDouble, false},
		{"float", core.TypeDouble, true},
		{"float", core.TypeFloat, false},
		{"real", core.TypeFloat, true},
		{"real", core.TypeDouble, false},
		{"NVARCHAR(100)", core.TypeString, true},
		{"nvarchar(max)", core.TypeString, true},
		{"nvarchar(100)", core.TypeEnumText, true},
		{"varchar(50)", core.TypeEnumText, true},
		{"nchar(3)", core.TypeEnumText, true},
		{"char(3)", core.TypeString, true},
		{"ntext", core.TypeString, true},
		{"text", core.TypeString, true},
		{"ntext", core.TypeEnumText, false},
		{"text", core.TypeEnumText, false},
		{"nvarchar(10)", core.TypeInt32, false},
		{"varbinary(max)", core.TypeBytes, true},
		{"binary(16)", core.TypeBytes, true},
		{"image", core.TypeBytes, true},
		{"varbinary(16)", core.TypeGUID, false},
		{"uniqueidentifier", core.TypeGUID, true},
		{"uniqueidentifier", core.TypeString, false},
		{"datetime", core.TypeDateTime, true},
		{"datetime2(7)", core.TypeDateTime, true},
		{"smalldatetime", core.TypeDateTime, true},
		{"datetime", core.TypeDateTimeOffset, false},
		{"datetimeoffset", core.TypeDateTime, true},
		{"datetimeoffset(7)", core.TypeDateTimeOffset, true},
		{"date", core.TypeDateTime, true},
		{"date", core.TypeDate, true},
		{"date", core.TypeTime, false},
		{"time(7)", core.TypeTime, true},
		{"time", core.TypeDateTime, false},
		// A custom token is compatible only through a handler, which the caller checks.
		{"nvarchar(max)", core.TypeCustom, false},
		{"unknowntype", core.TypeString, false},
	}
	for _, c := range cases {
		if got := d.IsDeclaredTypeCompatible(c.declared, c.token); got != c.want {
			t.Errorf("IsDeclaredTypeCompatible(%q, %s): got %v, want %v", c.declared, c.token, got, c.want)
		}
	}
}

func TestIsMarkerlessTemporal(t *testing.T) {
	d := sqlserver.New()
	for name, want := range map[string]bool{
		"DATETIME":       true,
		"DATETIME2":      true,
		"SMALLDATETIME":  true,
		"datetime2":      true,
		"DATETIMEOFFSET": false,
		"DATE":           false,
		"TIME":           false,
		"NVARCHAR":       false,
		"":               false,
	} {
		if got := d.IsMarkerlessTemporal(name); got != want {
			t.Errorf("IsMarkerlessTemporal(%q): got %v, want %v", name, got, want)
		}
	}
}

func TestCapabilityFlags(t *testing.T) {
	d := sqlserver.New()
	flags := map[string][2]bool{
		"PagingRequiresOrderBy":     {d.PagingRequiresOrderBy(), true},
		"SupportsRowValueIn":        {d.SupportsRowValueIn(), false},
		"SupportsArrayParameters":   {d.SupportsArrayParameters(), false},
		"BindsTemporalsNatively":    {d.BindsTemporalsNatively(), false},
		"SupportsMaterializedViews": {d.SupportsMaterializedViews(), false},
		"SupportsProcedures":        {d.SupportsProcedures(), true},
		"SupportsTransactionalDDL":  {d.SupportsTransactionalDDL(), true},
	}
	for name, pair := range flags {
		if pair[0] != pair[1] {
			t.Errorf("%s: got %v, want %v", name, pair[0], pair[1])
		}
	}
}

func TestIntrospectionQueriesTakeTheRelationParameter(t *testing.T) {
	d := sqlserver.New()
	for name, query := range map[string]string{
		"columns": d.ColumnsInfoSQL(), "indexes": d.IndexesInfoSQL(), "view definition": d.ViewDefinitionSQL(),
	} {
		if placeholders := core.FindPlaceholders(query); !reflect.DeepEqual(placeholders, []string{"relation"}) {
			t.Errorf("%s: placeholders %v, want [relation]", name, placeholders)
		}
	}
}

func TestSelectSQL_AnUnpagedOrderedSubqueryDropsItsOrderByButAPagedOneKeepsIt(t *testing.T) {
	m := usersMap()
	render := func(subquery *core.SelectAst) string {
		t.Helper()
		var bound []any
		bind := func(value any, _ *core.PropertyMap) (string, error) {
			bound = append(bound, value)
			return "@c" + string(rune('0'+len(bound)-1)), nil
		}
		root := &core.SelectAst{Map: m, Where: []core.Criteria{core.InSelect([]string{"ID"}, subquery)}}
		got, err := sqlserver.New().SelectSQL(root, bind)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	projection := []*core.PropertyMap{m.Property("ID")}
	ordered := []core.Ordering{{Property: "Name"}}

	// ORDER BY inside a subquery without TOP/OFFSET is error 1033 on SQL Server and means nothing to membership.
	unpaged := render(&core.SelectAst{Map: m, Projection: projection, Orderings: ordered})
	if want := "select [id], [name], [display_name] from [users] where [id] in (select [id] from [users])"; unpaged != want {
		t.Errorf("unpaged:\n got  %s\n want %s", unpaged, want)
	}

	limit := int64(5)
	paged := render(&core.SelectAst{Map: m, Projection: projection, Orderings: ordered, Limit: &limit})
	want := "select [id], [name], [display_name] from [users] where [id] in " +
		"(select [id] from [users] order by [name] offset 0 rows fetch next @c0 rows only)"
	if paged != want {
		t.Errorf("paged:\n got  %s\n want %s", paged, want)
	}
}

func TestSelectSQL_PagedWithoutOrderingsGainsTheConstantOrderBy(t *testing.T) {
	m := usersMap()
	limit, offset := int64(10), int64(20)
	var bound []any
	bind := func(value any, _ *core.PropertyMap) (string, error) {
		bound = append(bound, value)
		return "@c" + string(rune('0'+len(bound)-1)), nil
	}
	got, err := sqlserver.New().SelectSQL(&core.SelectAst{Map: m, Limit: &limit, Offset: &offset}, bind)
	if err != nil {
		t.Fatal(err)
	}
	want := "select [id], [name], [display_name] from [users] order by (select null) offset @c1 rows fetch next @c0 rows only"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	if !reflect.DeepEqual(bound, []any{int64(10), int64(20)}) {
		t.Errorf("bind order is limit then offset, got %v", bound)
	}
}
