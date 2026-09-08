package sqlite_test

import (
	"reflect"
	"testing"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
	"github.com/kosmassp/SimpleOrm/go/orm/sqlite"
)

// Rendering tests for every string member of sqlite.Dialect, against
// hand-built maps mirroring the fixture entities — expected strings come from
// dotnet/src/SimpleOrm.Sqlite/SqliteDialect.cs byte-for-byte (CODING-STANDARD §5).
// Test-local fixture types only (CODING-STANDARD §7: never in sample).

type user struct {
	ID          int64
	Name        string
	DisplayName *string
}

type transaction struct {
	ID      int64
	UserID  int64
	Version int64
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

func propSpec(t reflect.Type, name, column string, columnType core.ColumnType, nullable, key, generated, version bool) *core.PropertyMap {
	field, ok := t.FieldByName(name)
	if !ok {
		panic("sqlite_test: no field " + name + " on " + t.Name())
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

func transactionsMap() *core.EntityMap {
	t := reflect.TypeFor[transaction]()
	return core.NewEntityMap(t, core.RelationTable, "transactions", "", "", nil, []*core.PropertyMap{
		propSpec(t, "ID", "id", core.TypeInt64, false, true, true, false),
		propSpec(t, "UserID", "user_id", core.TypeInt64, false, false, false, false),
		propSpec(t, "Version", "version", core.TypeInt64, false, false, false, true),
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

func TestCreateTableSQL_DatabaseGeneratedKeyIsIntegerPrimaryKey(t *testing.T) {
	got := sqlite.New().CreateTableSQL(usersMap())
	want := "create table if not exists users (\n" +
		"    id INTEGER PRIMARY KEY,\n" +
		"    name TEXT NOT NULL,\n" +
		"    display_name TEXT\n" +
		") STRICT"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestCreateTableSQL_NaturalCompositeKeyAppendsTrailingPrimaryKey(t *testing.T) {
	got := sqlite.New().CreateTableSQL(userRolesMap())
	want := "create table if not exists user_roles (\n" +
		"    user_id INTEGER NOT NULL,\n" +
		"    role_id INTEGER NOT NULL,\n" +
		"    granted_by TEXT,\n" +
		"    primary key (user_id, role_id)\n" +
		") STRICT"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestCreateTableSQL_ClientGuidKeyMarksThatColumnPrimaryKey(t *testing.T) {
	got := sqlite.New().CreateTableSQL(widgetsMap())
	want := "create table if not exists widgets (\n" +
		"    id TEXT NOT NULL PRIMARY KEY,\n" +
		"    note TEXT NOT NULL\n" +
		") STRICT"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestCreateTableSQL_VersionColumnIsAnOrdinaryIntegerColumn(t *testing.T) {
	got := sqlite.New().CreateTableSQL(transactionsMap())
	want := "create table if not exists transactions (\n" +
		"    id INTEGER PRIMARY KEY,\n" +
		"    user_id INTEGER NOT NULL,\n" +
		"    version INTEGER NOT NULL\n" +
		") STRICT"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestCreateIndexSQL_UniqueAndDescendingColumns(t *testing.T) {
	m := usersMap()
	m.Indexes = []*core.EntityIndex{
		{Name: "ix_users_name", Columns: []core.IndexColumn{{ColumnName: "name"}}, Unique: true},
		{Name: "ix_users_id_name", Columns: []core.IndexColumn{{ColumnName: "id", Descending: true}, {ColumnName: "name"}}},
	}
	got := sqlite.New().CreateIndexSQL(m)
	want := []string{
		"create unique index if not exists ix_users_name on users (name)",
		"create index if not exists ix_users_id_name on users (id desc, name)",
	}
	if len(got) != len(want) {
		t.Fatalf("got %d statements, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d] got %q, want %q", i, got[i], want[i])
		}
	}
}

func TestCreateViewSQL(t *testing.T) {
	t2 := reflect.TypeFor[struct {
		SalesMonth string
	}]()
	m := core.NewEntityMap(t2, core.RelationView, "monthly_totals", "", "select 1 as sales_month", nil, nil, core.KeyNone, nil, nil)
	got := sqlite.New().CreateViewSQL(m)
	want := "create view if not exists monthly_totals as\nselect 1 as sales_month"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestInsertSQL_DatabaseGeneratedKeyIsExcludedButReturned(t *testing.T) {
	got := sqlite.New().InsertSQL(usersMap())
	want := "insert into users (name, display_name) values (@name, @display_name) returning id"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestInsertSQL_NaturalKeyHasNoReturningClause(t *testing.T) {
	got := sqlite.New().InsertSQL(userRolesMap())
	want := "insert into user_roles (user_id, role_id, granted_by) values (@user_id, @role_id, @granted_by)"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestUpdateSQL_VersionIncrementsAndGuardsTheWhereClause(t *testing.T) {
	got := sqlite.New().UpdateSQL(transactionsMap())
	want := "update transactions set user_id = @user_id, version = version + 1 where id = @id and version = @version"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestUpdateOnlySQL_RendersExactlyTheListedColumnsWithTheVersionRules(t *testing.T) {
	d := sqlite.New()
	transactions := transactionsMap()
	got := d.UpdateOnlySQL(transactions, []*core.PropertyMap{transactions.Property("UserID")})
	want := "update transactions set user_id = @user_id, version = version + 1 where id = @id and version = @version"
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
	got := sqlite.New().UpdateSQL(usersMap())
	want := "update users set name = @name, display_name = @display_name where id = @id"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestDeleteSQL_PlainAndVersionCheckedForms(t *testing.T) {
	d := sqlite.New()
	plain := d.DeleteSQL(transactionsMap(), false)
	if want := "delete from transactions where id = @id"; plain != want {
		t.Errorf("plain: got %q, want %q", plain, want)
	}
	checked := d.DeleteSQL(transactionsMap(), true)
	if want := "delete from transactions where id = @id and version = @version"; checked != want {
		t.Errorf("version-checked: got %q, want %q", checked, want)
	}
	// checkVersion on an unversioned entity is a no-op: no version to add.
	unversioned := d.DeleteSQL(usersMap(), true)
	if want := "delete from users where id = @id"; unversioned != want {
		t.Errorf("no version column: got %q, want %q", unversioned, want)
	}
}

func TestVersionTableSQL(t *testing.T) {
	got := sqlite.New().VersionTableSQL()
	want := "create table if not exists schema_version (\n" +
		"    version      INTEGER NOT NULL,\n" +
		"    object       TEXT NOT NULL,\n" +
		"    description  TEXT NOT NULL,\n" +
		"    checksum     TEXT NOT NULL,\n" +
		"    applied_at   TEXT NOT NULL,\n" +
		"    execution_ms INTEGER NOT NULL,\n" +
		"    primary key (version, object)\n" +
		") STRICT"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestMigrationActionDDL(t *testing.T) {
	d := sqlite.New()
	if got, want := d.RenameTableSQL("old_name", "new_name"), "alter table old_name rename to new_name"; got != want {
		t.Errorf("RenameTableSQL: got %q, want %q", got, want)
	}
	if got, want := d.RenameColumnSQL("users", "old_col", "new_col"), "alter table users rename column old_col to new_col"; got != want {
		t.Errorf("RenameColumnSQL: got %q, want %q", got, want)
	}
	if got, want := d.AddColumnSQL("users", "note", "TEXT", true, ""), "alter table users add column note TEXT"; got != want {
		t.Errorf("AddColumnSQL nullable no default: got %q, want %q", got, want)
	}
	if got, want := d.AddColumnSQL("users", "note", "TEXT", false, ""), "alter table users add column note TEXT not null"; got != want {
		t.Errorf("AddColumnSQL not null no default: got %q, want %q", got, want)
	}
	if got, want := d.AddColumnSQL("users", "note", "TEXT", false, "'x'"), "alter table users add column note TEXT not null default 'x'"; got != want {
		t.Errorf("AddColumnSQL not null with default: got %q, want %q", got, want)
	}
	if got, want := d.DropColumnSQL("users", "note"), "alter table users drop column note"; got != want {
		t.Errorf("DropColumnSQL: got %q, want %q", got, want)
	}
	if got, want := d.DropTableSQL("users"), "drop table users"; got != want {
		t.Errorf("DropTableSQL: got %q, want %q", got, want)
	}
	if got, want := d.DropIndexSQL("users", "ix_users_name"), "drop index ix_users_name"; got != want {
		t.Errorf("DropIndexSQL: got %q, want %q", got, want)
	}
}

func TestLimitOffsetClause_ThreeForms(t *testing.T) {
	d := sqlite.New()
	if got, want := d.LimitOffsetClause("@c0", "@c1"), "limit @c0 offset @c1"; got != want {
		t.Errorf("both: got %q, want %q", got, want)
	}
	if got, want := d.LimitOffsetClause("@c0", ""), "limit @c0"; got != want {
		t.Errorf("limit only: got %q, want %q", got, want)
	}
	if got, want := d.LimitOffsetClause("", "@c0"), "limit -1 offset @c0"; got != want {
		t.Errorf("offset only: got %q, want %q", got, want)
	}
	if got, want := d.LimitOffsetClause("", ""), ""; got != want {
		t.Errorf("neither: got %q, want %q", got, want)
	}
}

func TestIsDeclaredTypeCompatible(t *testing.T) {
	d := sqlite.New()
	cases := []struct {
		declared   string
		columnType core.ColumnType
		want       bool
	}{
		{"", core.TypeInt64, true},
		{"ANY", core.TypeString, true},
		{"any", core.TypeDecimal, true},
		{"INTEGER", core.TypeInt16, true},
		{"INTEGER", core.TypeInt32, true},
		{"INTEGER", core.TypeInt64, true},
		{"INT", core.TypeBool, true},
		{"INTEGER", core.TypeString, false},
		{"REAL", core.TypeDouble, true},
		{"REAL", core.TypeFloat, true},
		{"REAL", core.TypeInt64, false},
		{"BLOB", core.TypeBytes, true},
		{"BLOB", core.TypeGUID, true},
		{"BLOB", core.TypeString, false},
		{"TEXT", core.TypeString, true},
		{"TEXT", core.TypeDecimal, true},
		{"TEXT", core.TypeGUID, true},
		{"TEXT", core.TypeDateTime, true},
		{"TEXT", core.TypeDateTimeOffset, true},
		{"TEXT", core.TypeDate, true},
		{"TEXT", core.TypeTime, true},
		{"TEXT", core.TypeInt64, false},
		{"INTEGER", core.TypeEnumInt, true},
		{"INT", core.TypeEnumInt, true},
		{"TEXT", core.TypeEnumInt, false},
		{"TEXT", core.TypeEnumText, true},
		{"INTEGER", core.TypeEnumText, false},
	}
	for _, c := range cases {
		if got := d.IsDeclaredTypeCompatible(c.declared, c.columnType); got != c.want {
			t.Errorf("IsDeclaredTypeCompatible(%q, %s) = %v, want %v", c.declared, c.columnType, got, c.want)
		}
	}
}

func TestStorageType(t *testing.T) {
	d := sqlite.New()
	cases := []struct {
		columnType core.ColumnType
		want       string
	}{
		{core.TypeInt16, "INTEGER"},
		{core.TypeInt32, "INTEGER"},
		{core.TypeInt64, "INTEGER"},
		{core.TypeBool, "INTEGER"},
		{core.TypeEnumInt, "INTEGER"},
		{core.TypeDouble, "REAL"},
		{core.TypeFloat, "REAL"},
		{core.TypeBytes, "BLOB"},
		{core.TypeString, "TEXT"},
		{core.TypeDecimal, "TEXT"},
		{core.TypeGUID, "TEXT"},
		{core.TypeDateTime, "TEXT"},
		{core.TypeDateTimeOffset, "TEXT"},
		{core.TypeDate, "TEXT"},
		{core.TypeTime, "TEXT"},
		{core.TypeEnumText, "TEXT"},
	}
	for _, c := range cases {
		property := &core.PropertyMap{ColumnType: c.columnType}
		if got := d.StorageType(property); got != c.want {
			t.Errorf("StorageType(%s) = %q, want %q", c.columnType, got, c.want)
		}
	}
}

func TestCapabilityFlags(t *testing.T) {
	d := sqlite.New()
	if d.SupportsArrayParameters() {
		t.Error("SupportsArrayParameters should be false")
	}
	if d.BindsTemporalsNatively() {
		t.Error("BindsTemporalsNatively should be false")
	}
	if d.PagingRequiresOrderBy() {
		t.Error("PagingRequiresOrderBy should be false")
	}
	if !d.SupportsRowValueIn() {
		t.Error("SupportsRowValueIn should be true")
	}
	if d.SupportsMaterializedViews() {
		t.Error("SupportsMaterializedViews should be false")
	}
	if d.SupportsProcedures() {
		t.Error("SupportsProcedures should be false")
	}
	if !d.SupportsTransactionalDDL() {
		t.Error("SupportsTransactionalDDL should be true")
	}
}

func TestIntrospectionSQL(t *testing.T) {
	d := sqlite.New()
	if got, want := d.ColumnsInfoSQL(), `select name, type, "notnull", pk from pragma_table_info(@relation)`; got != want {
		t.Errorf("ColumnsInfoSQL: got %q, want %q", got, want)
	}
	if got, want := d.ViewDefinitionSQL(), "select sql from sqlite_master where type = 'view' and name = @relation"; got != want {
		t.Errorf("ViewDefinitionSQL: got %q, want %q", got, want)
	}
	if got, want := d.IndexesInfoSQL(), `select il.name, il."unique", ii.seqno, ii.name, ii."desc" from pragma_index_list(@relation) il, pragma_index_xinfo(il.name) ii where il.origin = 'c' and ii.key = 1 order by il.name, ii.seqno`; got != want {
		t.Errorf("IndexesInfoSQL: got %q, want %q", got, want)
	}
}
