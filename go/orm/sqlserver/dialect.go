package sqlserver

import (
	"context"
	"database/sql"
	"strings"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/render"
)

// Dialect is the SQL Server implementation of core.Dialect (ADR-0024,
// spec/dialects.md), backed by go-mssqldb. Every rendered string is the one
// spec/dialects.md states: identifiers are bracketed, paging is OFFSET/FETCH,
// the generated key is read with `scope_identity()`, DDL carries its existence
// guards, and the run lock is `sp_getapplock`. It also implements
// core.StatementDescriber (SchemaGuard) and core.TemporalMarkers (the §7.9
// VAL-020 rule on a database with real date types).
type Dialect struct{}

// New is the dialect; it carries no state.
func New() *Dialect { return &Dialect{} }

var (
	_ core.Dialect            = (*Dialect)(nil)
	_ core.StatementDescriber = (*Dialect)(nil)
	_ core.TemporalMarkers    = (*Dialect)(nil)
)

// CreateConnection opens the driver pool for go-mssqldb's native connection
// string (spec/dialects.md): a `sqlserver://host:port?database=…` URL or an
// ADO-style `server=…;database=…` string. With no `user id` the driver uses
// Windows integrated authentication on Windows (SSPI), which is what the
// default admin connection of the live tests relies on.
func (*Dialect) CreateConnection(connectionString string) (*sql.DB, error) {
	return openPool(connectionString)
}

// QuoteIdentifier brackets the name, doubling a `]` (ADR-0024: a legacy schema
// is full of reserved words — `transaction`, `order`, `desc`).
func (*Dialect) QuoteIdentifier(identifier string) string {
	return "[" + strings.ReplaceAll(identifier, "]", "]]") + "]"
}

// LimitOffsetClause is OFFSET/FETCH, legal only after ORDER BY (the renderer
// adds `order by (select null)` when PagingRequiresOrderBy). The placeholders
// bind WHERE first, then limit, then offset, even though this clause names the
// offset first (spec/dialects.md "Paging clause").
func (*Dialect) LimitOffsetClause(limitParameter, offsetParameter string) string {
	switch {
	case limitParameter != "" && offsetParameter != "":
		return "offset " + offsetParameter + " rows fetch next " + limitParameter + " rows only"
	case limitParameter != "":
		return "offset 0 rows fetch next " + limitParameter + " rows only"
	case offsetParameter != "":
		return "offset " + offsetParameter + " rows"
	}
	return ""
}

func (d *Dialect) SelectSQL(ast *core.SelectAst, bind core.BindCriteriaParameter) (string, error) {
	return render.SelectSQL(d, ast, bind)
}

// PagingRequiresOrderBy is true: OFFSET/FETCH is only legal after ORDER BY.
func (*Dialect) PagingRequiresOrderBy() bool { return true }

// SupportsRowValueIn is false: `(a, b) in (select …)` does not parse on SQL
// Server, so the renderer rewrites composite membership as a correlated EXISTS.
func (*Dialect) SupportsRowValueIn() bool { return false }

// SupportsArrayParameters is false: table-valued parameters are not simple
// array parameters, so the §7.12 IN-expansion applies.
func (*Dialect) SupportsArrayParameters() bool { return false }

// BindsTemporalsNatively is false: SQL Server converts the ISO-8601 string
// parameter (an nvarchar) to datetimeoffset implicitly (spec/dialects.md).
func (*Dialect) BindsTemporalsNatively() bool { return false }

// SupportsMaterializedViews is false: indexed views are a different mechanism.
func (*Dialect) SupportsMaterializedViews() bool { return false }

// SupportsProcedures is true and dormant: no execution machinery before Level 4.
func (*Dialect) SupportsProcedures() bool { return true }

func (*Dialect) SupportsTransactionalDDL() bool { return true }

// --- introspection (spec/dialects.md "Introspection result shapes") ---------------
//
// The result columns carry the spec's names (`name, type, notnull, pk`;
// `index_name, unique, seqno, column, desc`; `definition`) on every non-SQLite
// dialect of every implementation — SQLite's pragmas name them so already — so
// a consumer may read by name or, as this port and the reference do, by ordinal.

func (*Dialect) ColumnsInfoSQL() string {
	return "select c.name as [name],\n" +
		"       case\n" +
		"           when t.name in (N'decimal', N'numeric')\n" +
		"               then t.name + N'(' + cast(c.precision as nvarchar(8)) + N',' + cast(c.scale as nvarchar(8)) + N')'\n" +
		"           when t.name in (N'nvarchar', N'nchar')\n" +
		"               then t.name + N'(' + case when c.max_length = -1 then N'max' else cast(c.max_length / 2 as nvarchar(8)) end + N')'\n" +
		"           when t.name in (N'varchar', N'char', N'varbinary', N'binary')\n" +
		"               then t.name + N'(' + case when c.max_length = -1 then N'max' else cast(c.max_length as nvarchar(8)) end + N')'\n" +
		"           else t.name\n" +
		"       end as [type],\n" +
		"       cast(case when c.is_nullable = 1 then 0 else 1 end as bigint) as [notnull],\n" +
		"       cast(isnull(pk.key_ordinal, 0) as bigint) as [pk]\n" +
		"from sys.columns c\n" +
		"join sys.types t on t.user_type_id = c.user_type_id\n" +
		"left join (\n" +
		"    select ic.object_id, ic.column_id, ic.key_ordinal\n" +
		"    from sys.index_columns ic\n" +
		"    join sys.indexes i on i.object_id = ic.object_id and i.index_id = ic.index_id\n" +
		"    where i.is_primary_key = 1\n" +
		") pk on pk.object_id = c.object_id and pk.column_id = c.column_id\n" +
		"where c.object_id = object_id(@relation)\n" +
		"order by c.column_id"
}

func (*Dialect) IndexesInfoSQL() string {
	return "select i.name as [index_name], cast(i.is_unique as bigint) as [unique], cast(ic.key_ordinal - 1 as bigint) as [seqno],\n" +
		"       c.name as [column], cast(ic.is_descending_key as bigint) as [desc]\n" +
		"from sys.indexes i\n" +
		"join sys.index_columns ic on ic.object_id = i.object_id and ic.index_id = i.index_id and ic.is_included_column = 0\n" +
		"join sys.columns c on c.object_id = ic.object_id and c.column_id = ic.column_id\n" +
		"where i.object_id = object_id(@relation) and i.type > 0 and i.is_primary_key = 0 and i.is_unique_constraint = 0\n" +
		"order by i.name, ic.key_ordinal"
}

func (*Dialect) ViewDefinitionSQL() string {
	return "select m.definition as [definition] from sys.sql_modules m join sys.views v on v.object_id = m.object_id\n" +
		"where v.object_id = object_id(@relation)"
}

// IsDeclaredTypeCompatible is the SQL Server half of the VAL-011 table
// (spec/dialects.md "Declared-type compatibility"). The declared type is
// reduced to its base name first; an empty base name is compatible with nothing
// (SQL Server always declares a type). A custom token is compatible only
// through a handler, which the caller checks.
func (*Dialect) IsDeclaredTypeCompatible(declaredType string, columnType core.ColumnType) bool {
	base := baseTypeName(declaredType)
	switch base {
	case "int", "tinyint":
		return columnType == core.TypeInt32 || columnType == core.TypeInt64 || columnType == core.TypeInt16 ||
			columnType == core.TypeEnumInt
	case "bigint":
		return columnType == core.TypeInt64 || columnType == core.TypeEnumInt
	case "smallint":
		return columnType == core.TypeInt16 || columnType == core.TypeInt32 || columnType == core.TypeInt64 ||
			columnType == core.TypeEnumInt
	case "bit":
		return columnType == core.TypeBool
	case "decimal", "numeric", "money", "smallmoney":
		return columnType == core.TypeDecimal
	case "float":
		return columnType == core.TypeDouble
	case "real":
		return columnType == core.TypeFloat
	case "nvarchar", "varchar", "nchar", "char":
		return columnType == core.TypeString || columnType == core.TypeEnumText
	case "ntext", "text":
		return columnType == core.TypeString
	case "varbinary", "binary", "image":
		return columnType == core.TypeBytes
	case "uniqueidentifier":
		return columnType == core.TypeGUID
	case "datetime", "datetime2", "smalldatetime":
		// Type-compatible; the marker rule (VAL-020) applies per value at read time.
		return columnType == core.TypeDateTime
	case "datetimeoffset":
		return columnType == core.TypeDateTime || columnType == core.TypeDateTimeOffset
	case "date":
		return columnType == core.TypeDateTime || columnType == core.TypeDate
	case "time":
		return columnType == core.TypeTime
	}
	return false
}

// baseTypeName reduces a declared type to its base name: trimmed, lowercased,
// any parenthesized length or precision removed (`decimal(18,2)` → `decimal`,
// `NVARCHAR(100)` → `nvarchar`).
func baseTypeName(declaredType string) string {
	name := strings.ToLower(strings.TrimSpace(declaredType))
	if open := strings.IndexByte(name, '('); open >= 0 {
		name = strings.TrimSpace(name[:open])
	}
	return name
}

// IsMarkerlessTemporal reports the column types that store an instant with no
// UTC/offset marker (spec/dialects.md "Temporals"): datetime, datetime2, and
// smalldatetime refuse VAL-020 when read into a temporal property.
// datetimeoffset carries its marker; date and time carry no kind by design.
func (*Dialect) IsMarkerlessTemporal(databaseTypeName string) bool {
	switch strings.ToUpper(strings.TrimSpace(databaseTypeName)) {
	case "DATETIME", "DATETIME2", "SMALLDATETIME":
		return true
	}
	return false
}

// StorageType is the token → SQL Server storage type (spec/dialects.md
// "Storage types"): strings and blobs are `(max)` except key columns, which
// take the indexable 450; dates are `datetimeoffset`, the only marker-carrying
// storage; decimals default to `decimal(38, 9)` (declare the real precision
// in migrations).
func (*Dialect) StorageType(property *core.PropertyMap) string {
	return storageType(property, property.IsKey)
}

// storageType is StorageType with the indexable flag explicit: a key column,
// and — in generated CREATE TABLE only — a column a declared index lists, takes
// the 450 length, because a `(max)` column cannot be an index key.
func storageType(property *core.PropertyMap, indexable bool) string {
	switch property.ColumnType {
	case core.TypeInt16:
		return "smallint"
	case core.TypeInt32, core.TypeEnumInt:
		return "int"
	case core.TypeInt64:
		return "bigint"
	case core.TypeBool:
		return "bit"
	case core.TypeDecimal:
		return "decimal(38, 9)"
	case core.TypeDouble:
		return "float"
	case core.TypeFloat:
		return "real"
	case core.TypeGUID:
		return "uniqueidentifier"
	case core.TypeBytes:
		if indexable {
			return "varbinary(450)"
		}
		return "varbinary(max)"
	case core.TypeDateTime, core.TypeDateTimeOffset:
		return "datetimeoffset"
	case core.TypeDate:
		return "date"
	case core.TypeTime:
		return "time"
	case core.TypeEnumText:
		return "nvarchar(100)"
	}
	// string, JSON, and handler-backed (custom) types.
	if indexable {
		return "nvarchar(450)"
	}
	return "nvarchar(max)"
}

// --- migrations (§7.23) -------------------------------------------------------------

// runLockSQL takes the migration lock inside the open transaction:
// transaction-owned, exclusive, 60 s timeout, a refusal raises (a negative
// return code) so the run never starts.
const runLockSQL = "declare @rc int; " +
	"exec @rc = sp_getapplock @Resource = N'simpleorm:migrations', @LockMode = 'Exclusive', " +
	"@LockOwner = 'Transaction', @LockTimeout = 60000; " +
	"if @rc < 0 raiserror(N'the SimpleOrm migration lock is held by another run', 16, 1)"

// BeginMigrationRunLock begins a transaction and takes the application lock
// inside it (ADR-0024): DDL is transactional on SQL Server, so a failed run
// applies nothing, and the lock dies with the transaction — it can never leak
// past a rollback. A refused lock rolls the transaction back and returns the
// database error; the run has not started.
func (*Dialect) BeginMigrationRunLock(ctx context.Context, conn *sql.Conn) (*sql.Tx, error) {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, runLockSQL); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	return tx, nil
}

func (*Dialect) VersionTableSQL() string {
	return "if object_id(N'schema_version', N'U') is null\n" +
		"create table schema_version (\n" +
		"    version      bigint        not null,\n" +
		"    object       nvarchar(200) not null,\n" +
		"    description  nvarchar(400) not null,\n" +
		"    checksum     nvarchar(64)  not null,\n" +
		"    applied_at   nvarchar(40)  not null,\n" +
		"    execution_ms bigint        not null,\n" +
		"    primary key (version, object)\n" +
		")"
}

// nLiteral is a Unicode string literal, doubling any single quote.
func nLiteral(text string) string { return "N'" + strings.ReplaceAll(text, "'", "''") + "'" }

func (*Dialect) RenameTableSQL(fromName, toName string) string {
	return "exec sp_rename " + nLiteral(fromName) + ", " + nLiteral(toName)
}

func (*Dialect) RenameColumnSQL(table, fromName, toName string) string {
	return "exec sp_rename " + nLiteral(table+"."+fromName) + ", " + nLiteral(toName) + ", 'COLUMN'"
}

// AddColumnSQL has no `column` keyword (T-SQL: `alter table T add C …`).
func (d *Dialect) AddColumnSQL(table, column, storageType string, nullable bool, defaultSQL string) string {
	sql := "alter table " + d.QuoteIdentifier(table) + " add " + d.QuoteIdentifier(column) + " " + storageType
	if !nullable {
		sql += " not null"
	}
	if defaultSQL != "" {
		sql += " default " + defaultSQL
	}
	return sql
}

func (d *Dialect) DropColumnSQL(table, column string) string {
	return "alter table " + d.QuoteIdentifier(table) + " drop column " + d.QuoteIdentifier(column)
}

func (d *Dialect) DropTableSQL(table string) string { return "drop table " + d.QuoteIdentifier(table) }

// DropIndexSQL names the table too: index names are table-scoped on SQL Server.
func (d *Dialect) DropIndexSQL(table, index string) string {
	return "drop index " + d.QuoteIdentifier(index) + " on " + d.QuoteIdentifier(table)
}

// --- generated DDL and CRUD from the EntityMap (ADR-0011, ADR-0008 add.3) ----------

func (d *Dialect) CreateViewSQL(m *core.EntityMap) string {
	return "create or alter view " + d.QuoteIdentifier(m.RelationName) + " as\n" + m.DefiningSQL
}

func (d *Dialect) CreateTableSQL(m *core.EntityMap) string {
	indexed := indexedColumns(m)
	var b strings.Builder
	b.WriteString("if object_id(" + nLiteral(m.RelationName) + ", N'U') is null\n")
	b.WriteString("create table ")
	b.WriteString(d.QuoteIdentifier(m.RelationName))
	b.WriteString(" (")
	first := true
	for _, property := range m.Properties {
		if first {
			b.WriteString("\n    ")
		} else {
			b.WriteString(",\n    ")
		}
		first = false
		b.WriteString(d.QuoteIdentifier(property.ColumnName))
		b.WriteByte(' ')
		stored := storageType(property, property.IsKey || indexed[property.ColumnName])
		if m.KeyStrategy == core.KeyDatabaseGenerated && property.IsKey {
			b.WriteString(stored + " identity(1,1) primary key")
			continue
		}
		b.WriteString(stored)
		if !property.IsNullable {
			b.WriteString(" not null")
		}
		if m.KeyStrategy == core.KeyClientGuid && property.IsKey {
			b.WriteString(" primary key")
		}
	}
	if m.KeyStrategy == core.KeyNatural && len(m.KeyProperties) > 0 {
		b.WriteString(",\n    primary key (")
		b.WriteString(strings.Join(d.quotedColumns(m.KeyProperties), ", "))
		b.WriteByte(')')
	}
	b.WriteString("\n)")
	return b.String()
}

// indexedColumns is the set of column names any declared index lists: those
// columns take the indexable storage length in generated CREATE TABLE.
func indexedColumns(m *core.EntityMap) map[string]bool {
	columns := map[string]bool{}
	for _, index := range m.Indexes {
		for _, column := range index.Columns {
			columns[column.ColumnName] = true
		}
	}
	return columns
}

func (d *Dialect) CreateIndexSQL(m *core.EntityMap) []string {
	statements := make([]string, 0, len(m.Indexes))
	for _, index := range m.Indexes {
		unique := ""
		if index.Unique {
			unique = "unique "
		}
		columns := make([]string, len(index.Columns))
		for i, column := range index.Columns {
			columns[i] = d.QuoteIdentifier(column.ColumnName)
			if column.Descending {
				columns[i] += " desc"
			}
		}
		statements = append(statements,
			"if not exists (select 1 from sys.indexes where name = "+nLiteral(index.Name)+
				" and object_id = object_id("+nLiteral(m.RelationName)+"))\n"+
				"create "+unique+"index "+d.QuoteIdentifier(index.Name)+" on "+d.QuoteIdentifier(m.RelationName)+
				" ("+strings.Join(columns, ", ")+")")
	}
	return statements
}

// InsertSQL reads a database-generated key in the same batch with
// `scope_identity()` — not an OUTPUT clause, which fails on tables with
// triggers (ADR-0024). The generated-key strategy is identity-only (ADR-0020),
// which scope_identity always covers.
func (d *Dialect) InsertSQL(m *core.EntityMap) string {
	var columns, placeholders []string
	for _, p := range m.Properties {
		if p.IsGenerated {
			continue
		}
		columns = append(columns, d.QuoteIdentifier(p.ColumnName))
		placeholders = append(placeholders, "@"+p.ColumnName)
	}
	sql := "insert into " + d.QuoteIdentifier(m.RelationName) +
		" (" + strings.Join(columns, ", ") + ")" +
		" values (" + strings.Join(placeholders, ", ") + ")"
	if m.KeyStrategy == core.KeyDatabaseGenerated {
		sql += "; select cast(scope_identity() as bigint)"
	}
	return sql
}

func (d *Dialect) UpdateSQL(m *core.EntityMap) string {
	// Generated non-key columns are database-owned: never in SET (mirrors the
	// insert exclusion and Update's binding filter).
	var set []*core.PropertyMap
	for _, p := range m.Properties {
		if !p.IsKey && !p.IsVersion && !p.IsGenerated {
			set = append(set, p)
		}
	}
	return reportingCount(d.renderUpdate(m, set))
}

func (d *Dialect) UpdateOnlySQL(m *core.EntityMap, properties []*core.PropertyMap) string {
	return reportingCount(d.renderUpdate(m, properties))
}

// reportingCount wraps a generated UPDATE or DELETE so the batch reports its own affected-row count
// (ADR-0040 add.3, spec/dialects.md Clarification 23): the counts are suppressed (a trigger's rows would
// join the statement's), the statement's @@rowcount comes back as the batch's one scalar, and nocount is
// switched off again because the setting outlives the batch and a registry command on the same connection
// keeps the driver's count. Not OUTPUT: it is refused on a triggered table.
func reportingCount(statement string) string {
	return "set nocount on; " + statement + "; select @@rowcount; set nocount off"
}

// ReportsAffectedRowsBySelect is true: UpdateSQL, UpdateOnlySQL, and DeleteSQL render the count-reporting
// batch and the session reads its scalar (spec/dialects.md Clarification 23).
func (*Dialect) ReportsAffectedRowsBySelect() bool { return true }

func (d *Dialect) renderUpdate(m *core.EntityMap, set []*core.PropertyMap) string {
	var assignments []string
	for _, p := range set {
		assignments = append(assignments, d.QuoteIdentifier(p.ColumnName)+" = @"+p.ColumnName)
	}
	if version := m.VersionProperty; version != nil {
		column := d.QuoteIdentifier(version.ColumnName)
		assignments = append(assignments, column+" = "+column+" + 1")
	}
	sql := "update " + d.QuoteIdentifier(m.RelationName) +
		" set " + strings.Join(assignments, ", ") + " where " + d.keyPredicate(m)
	if version := m.VersionProperty; version != nil {
		sql += " and " + d.QuoteIdentifier(version.ColumnName) + " = @" + version.ColumnName
	}
	return sql
}

func (d *Dialect) DeleteSQL(m *core.EntityMap, checkVersion bool) string {
	sql := "delete from " + d.QuoteIdentifier(m.RelationName) + " where " + d.keyPredicate(m)
	if version := m.VersionProperty; checkVersion && version != nil {
		sql += " and " + d.QuoteIdentifier(version.ColumnName) + " = @" + version.ColumnName
	}
	return reportingCount(sql)
}

func (d *Dialect) keyPredicate(m *core.EntityMap) string {
	parts := make([]string, len(m.KeyProperties))
	for i, key := range m.KeyProperties {
		parts[i] = d.QuoteIdentifier(key.ColumnName) + " = @" + key.ColumnName
	}
	return strings.Join(parts, " and ")
}

func (d *Dialect) quotedColumns(properties []*core.PropertyMap) []string {
	names := make([]string, len(properties))
	for i, p := range properties {
		names[i] = d.QuoteIdentifier(p.ColumnName)
	}
	return names
}
