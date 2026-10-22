package sqlserver_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
	"github.com/kosmassp/SimpleOrm/go/orm/migrations"
	"github.com/kosmassp/SimpleOrm/go/orm/sqlserver"
)

// The migration-tooling members of the SQL Server dialect (ADR-0043, spec/dialects.md): the stamp name, the
// storage-type function with its facets and the indexable length, the snapshot-shape renderings, the view
// members, and the shadow host's connection-string plumbing. Pure — no server.

type facetEntity struct{}

func facetProperty(name string, columnType core.ColumnType, key bool) *core.PropertyMap {
	return &core.PropertyMap{PropertyName: name, ColumnName: strings.ToLower(name), ColumnType: columnType, IsKey: key}
}

func facetMap(indexes []*core.EntityIndex, properties ...*core.PropertyMap) *core.EntityMap {
	return core.NewEntityMap(reflect.TypeFor[facetEntity](), core.RelationTable, "facets", "", "", nil, properties,
		core.KeyDatabaseGenerated, indexes, nil)
}

func TestName_IsSqlserver(t *testing.T) {
	if got := sqlserver.New().Name(); got != "sqlserver" {
		t.Errorf("Name = %q, want sqlserver", got)
	}
}

func TestStorageType_FacetsIndexableLengthAndTheDecimalDefault(t *testing.T) {
	d := sqlserver.New()
	indexedName := facetProperty("Name", core.TypeString, false)
	key := facetProperty("Code", core.TypeString, true)
	plain := facetProperty("Remark", core.TypeString, false)
	m := facetMap([]*core.EntityIndex{
		{Name: "ix_facets_name", Columns: []core.IndexColumn{{PropertyName: "Name", ColumnName: "name"}}},
	}, indexedName, key, plain)

	for _, c := range []struct {
		what     string
		property *core.PropertyMap
		want     string
	}{
		{"a column of a declared index takes the indexable length", indexedName, "nvarchar(450)"},
		{"a key column takes the indexable length", key, "nvarchar(450)"},
		{"any other string is max", plain, "nvarchar(max)"},
	} {
		if got := d.StorageType(m, c.property); got != c.want {
			t.Errorf("%s: got %q, want %q", c.what, got, c.want)
		}
	}

	declared := &core.PropertyMap{PropertyName: "Name", ColumnName: "name", ColumnType: core.TypeString, IsKey: true, Length: 50}
	if got := d.StorageType(m, declared); got != "nvarchar(50)" {
		t.Errorf("a Length facet wins over the indexable default: got %q", got)
	}
	blob := &core.PropertyMap{PropertyName: "Blob", ColumnName: "blob", ColumnType: core.TypeBytes, Length: 16}
	if got := d.StorageType(m, blob); got != "varbinary(16)" {
		t.Errorf("a bytes Length facet: got %q", got)
	}
	indexedBlob := &core.PropertyMap{PropertyName: "Blob", ColumnName: "blob", ColumnType: core.TypeBytes, IsKey: true}
	if got := d.StorageType(m, indexedBlob); got != "varbinary(450)" {
		t.Errorf("an indexable bytes column: got %q", got)
	}

	if got := d.StorageType(m, &core.PropertyMap{ColumnType: core.TypeDecimal}); got != "decimal(38,9)" {
		t.Errorf("the decimal default is spelled without a space: got %q", got)
	}
	if got := d.StorageType(m, &core.PropertyMap{ColumnType: core.TypeDecimal, Precision: 18, Scale: 2}); got != "decimal(18,2)" {
		t.Errorf("a Precision/Scale facet: got %q", got)
	}
	if got := d.StorageType(m, &core.PropertyMap{ColumnType: core.TypeDecimal, Precision: 9}); got != "decimal(9,0)" {
		t.Errorf("a Precision with the scale left out is scale 0: got %q", got)
	}
}

func TestCreateTableSQL_RendersIndexedStringsAsNvarchar450ThroughTheOneFunction(t *testing.T) {
	m := facetMap([]*core.EntityIndex{
		{Name: "ix_facets_email", Columns: []core.IndexColumn{{PropertyName: "Email", ColumnName: "email"}}, Unique: true},
	}, facetProperty("Id", core.TypeInt64, true), facetProperty("Email", core.TypeString, false), facetProperty("Remark", core.TypeString, false))
	m.Properties[0].IsGenerated = true

	got := sqlserver.New().CreateTableSQL(m)
	for _, want := range []string{"[email] nvarchar(450) not null", "[remark] nvarchar(max) not null"} {
		if !strings.Contains(got, want) {
			t.Errorf("CreateTableSQL lacks %q:\n%s", want, got)
		}
	}
}

func TestCreateTableFromSchemaSQL_GeneratedKeyInlineOtherKeysTrailing(t *testing.T) {
	d := sqlserver.New()
	generated := &core.TableSchema{Name: "users", Columns: []core.TableColumn{
		{Name: "id", StorageType: "bigint", Key: true, Generated: true},
		{Name: "name", StorageType: "nvarchar(max)"},
		{Name: "note", StorageType: "nvarchar(max)", Nullable: true},
	}}
	wantGenerated := "if object_id(N'users', N'U') is null\n" +
		"create table [users] (\n" +
		"    [id] bigint identity(1,1) primary key,\n" +
		"    [name] nvarchar(max) not null,\n" +
		"    [note] nvarchar(max)\n" +
		")"
	if got := d.CreateTableFromSchemaSQL(generated); got != wantGenerated {
		t.Errorf("generated key:\n got: %s\nwant: %s", got, wantGenerated)
	}

	composite := &core.TableSchema{Name: "user_roles", Columns: []core.TableColumn{
		{Name: "user_id", StorageType: "bigint", Key: true},
		{Name: "role_id", StorageType: "bigint", Key: true},
		{Name: "granted_by", StorageType: "nvarchar(max)", Nullable: true},
	}}
	wantComposite := "if object_id(N'user_roles', N'U') is null\n" +
		"create table [user_roles] (\n" +
		"    [user_id] bigint not null,\n" +
		"    [role_id] bigint not null,\n" +
		"    [granted_by] nvarchar(max),\n" +
		"    primary key ([user_id], [role_id])\n" +
		")"
	if got := d.CreateTableFromSchemaSQL(composite); got != wantComposite {
		t.Errorf("composite key:\n got: %s\nwant: %s", got, wantComposite)
	}
}

func TestCreateIndexFromSchemaSQL_GuardedByASysIndexesLookup(t *testing.T) {
	index := core.TableIndex{
		Name: "ix_t_a_b", Unique: true,
		Columns: []core.IndexPart{{ColumnName: "a"}, {ColumnName: "b", Descending: true}},
	}
	want := "if not exists (select 1 from sys.indexes where name = N'ix_t_a_b' and object_id = object_id(N't'))\n" +
		"create unique index [ix_t_a_b] on [t] ([a], [b] desc)"
	if got := sqlserver.New().CreateIndexFromSchemaSQL("t", index); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestViewMembers_RenderThroughTheSeam(t *testing.T) {
	d := sqlserver.New()
	if got, want := d.CreateViewFromBodySQL("totals", "select 1 as answer", false), "create or alter view [totals] as\nselect 1 as answer"; got != want {
		t.Errorf("CreateViewFromBodySQL: got %q, want %q", got, want)
	}
	if got, want := d.DropViewSQL("totals", false), "drop view if exists [totals]"; got != want {
		t.Errorf("DropViewSQL: got %q, want %q", got, want)
	}
	if !d.ViewDefinitionIsVerbatim() {
		t.Error("sys.sql_modules keeps the view text as submitted: ViewDefinitionIsVerbatim should be true")
	}
}

func TestDropColumnSQL_QuotesTheTableAndColumnInsideTheBatch(t *testing.T) {
	got := sqlserver.New().DropColumnSQL("o'brien", "x]y")
	for _, want := range []string{
		"object_id(N'o''brien')", "c.name = N'x]y'",
		"exec(N'alter table [o''brien] drop constraint ' + quotename(@df))",
		"alter table [o'brien] drop column [x]]y]",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("DropColumnSQL lacks %q:\n%s", want, got)
		}
	}
}

func TestShadowHost_ImplementsTheContractWithoutATriggerReadBack(t *testing.T) {
	var host migrations.ShadowHost = sqlserver.ShadowHost{}
	if _, hasReadBack := host.(migrations.TriggerReadBack); hasReadBack {
		t.Error("SQL Server has no trigger read-back: `inserted` has lost a token's column list")
	}
}
