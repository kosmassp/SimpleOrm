package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	sqlitedriver "modernc.org/sqlite"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/render"
)

// Dialect is the SQLite implementation of core.Dialect (§7.25), backed by
// modernc.org/sqlite. Every rendered string is byte-identical to the C#
// SqliteDialect: the conformance pins and the recorded migration checksums
// hash them. It also implements core.StatementDescriber for SchemaGuard.
type Dialect struct{}

// New is the dialect; it carries no state.
func New() *Dialect { return &Dialect{} }

var (
	_ core.Dialect            = (*Dialect)(nil)
	_ core.StatementDescriber = (*Dialect)(nil)
)

// CreateConnection opens the driver pool for a bare file path, a
// `Data Source=<path>` connection string (the reference's form), or a `file:`
// URL. Transactions begin IMMEDIATE (`_txlock=immediate`), as
// Microsoft.Data.Sqlite's BeginTransaction does — the session's transactions
// and the migration run lock share the reference's locking semantics.
func (*Dialect) CreateConnection(connectionString string) (*sql.DB, error) {
	return sql.Open(DriverName, DataSourceName(connectionString))
}

// DataSourceName is the driver DSN for a connection string; exported so tests
// and tools open the same database the session would.
func DataSourceName(connectionString string) string {
	path := strings.TrimSpace(connectionString)
	for _, part := range strings.Split(path, ";") {
		key, value, found := strings.Cut(part, "=")
		if found && strings.EqualFold(strings.TrimSpace(key), "Data Source") {
			path = strings.TrimSpace(value)
			break
		}
	}
	separator := "?"
	if strings.Contains(path, "?") {
		separator = "&"
	}
	return path + separator + "_txlock=immediate"
}

// QuoteIdentifier returns the name unquoted (ADR-0024): snake_case names need
// nothing, and the reference renderings stay byte-identical.
func (*Dialect) QuoteIdentifier(identifier string) string { return identifier }

func (*Dialect) LimitOffsetClause(limitParameter, offsetParameter string) string {
	switch {
	case limitParameter != "" && offsetParameter != "":
		return "limit " + limitParameter + " offset " + offsetParameter
	case limitParameter != "":
		return "limit " + limitParameter
	case offsetParameter != "":
		return "limit -1 offset " + offsetParameter // SQLite requires LIMIT before OFFSET
	}
	return ""
}

func (d *Dialect) SelectSQL(ast *core.SelectAst, bind core.BindCriteriaParameter) (string, error) {
	return render.SelectSQL(d, ast, bind)
}

func (*Dialect) SupportsArrayParameters() bool { return false }

// BindsTemporalsNatively is false: TEXT storage — the ISO-8601 string IS the value (§7.9).
func (*Dialect) BindsTemporalsNatively() bool { return false }

func (*Dialect) PagingRequiresOrderBy() bool { return false }

func (*Dialect) SupportsRowValueIn() bool { return true }

func (*Dialect) SupportsMaterializedViews() bool { return false }

func (*Dialect) SupportsProcedures() bool { return false }

func (*Dialect) SupportsTransactionalDDL() bool { return true }

func (*Dialect) ColumnsInfoSQL() string {
	return `select name, type, "notnull", pk from pragma_table_info(@relation)`
}

func (*Dialect) ViewDefinitionSQL() string {
	return "select sql from sqlite_master where type = 'view' and name = @relation"
}

func (*Dialect) IndexesInfoSQL() string {
	return `select il.name, il."unique", ii.seqno, ii.name, ii."desc" ` +
		"from pragma_index_list(@relation) il, pragma_index_xinfo(il.name) ii " +
		"where il.origin = 'c' and ii.key = 1 order by il.name, ii.seqno"
}

// IsDeclaredTypeCompatible is the declared-type → token table (§7.19). An
// untyped column (`""`/`ANY`) contradicts nothing; a custom token is compatible
// only through a handler, which the caller checks.
func (*Dialect) IsDeclaredTypeCompatible(declaredType string, columnType core.ColumnType) bool {
	declared := strings.ToUpper(strings.TrimSpace(declaredType))
	if declared == "" || declared == "ANY" {
		return true
	}
	switch columnType {
	case core.TypeEnumInt:
		return declared == "INT" || declared == "INTEGER"
	case core.TypeEnumText:
		return declared == "TEXT"
	}
	switch declared {
	case "INT", "INTEGER":
		return columnType.IsInteger() || columnType == core.TypeBool
	case "REAL":
		return columnType == core.TypeDouble || columnType == core.TypeFloat
	case "BLOB":
		return columnType == core.TypeBytes || columnType == core.TypeGUID
	case "TEXT":
		return columnType == core.TypeString || columnType == core.TypeDecimal || columnType == core.TypeGUID ||
			columnType.IsTemporal()
	}
	return false
}

// BeginMigrationRunLock is BEGIN IMMEDIATE (via the connection's `_txlock`):
// exclusive writer, and with transactional DDL a failed run is fully atomic.
func (*Dialect) BeginMigrationRunLock(ctx context.Context, conn *sql.Conn) (*sql.Tx, error) {
	return conn.BeginTx(ctx, nil)
}

func (*Dialect) VersionTableSQL() string {
	return "create table if not exists schema_version (\n" +
		"    version      INTEGER NOT NULL,\n" +
		"    object       TEXT NOT NULL,\n" +
		"    description  TEXT NOT NULL,\n" +
		"    checksum     TEXT NOT NULL,\n" +
		"    applied_at   TEXT NOT NULL,\n" +
		"    execution_ms INTEGER NOT NULL,\n" +
		"    primary key (version, object)\n" +
		") STRICT"
}

// Migration-action DDL (ADR-0024): these strings are frozen — recorded checksums hash them.

func (*Dialect) RenameTableSQL(fromName, toName string) string {
	return "alter table " + fromName + " rename to " + toName
}

func (*Dialect) RenameColumnSQL(table, fromName, toName string) string {
	return "alter table " + table + " rename column " + fromName + " to " + toName
}

func (*Dialect) AddColumnSQL(table, column, storageType string, nullable bool, defaultSQL string) string {
	sql := "alter table " + table + " add column " + column + " " + storageType
	if !nullable {
		sql += " not null"
	}
	if defaultSQL != "" {
		sql += " default " + defaultSQL
	}
	return sql
}

func (*Dialect) DropColumnSQL(table, column string) string {
	return "alter table " + table + " drop column " + column
}

func (*Dialect) DropTableSQL(table string) string { return "drop table " + table }

func (*Dialect) DropIndexSQL(table, index string) string { return "drop index " + index }

func (*Dialect) CreateViewSQL(m *core.EntityMap) string {
	return "create view if not exists " + m.RelationName + " as\n" + m.DefiningSQL
}

func (d *Dialect) CreateTableSQL(m *core.EntityMap) string {
	var b strings.Builder
	b.WriteString("create table if not exists ")
	b.WriteString(m.RelationName)
	b.WriteString(" (")
	first := true
	for _, property := range m.Properties {
		if first {
			b.WriteString("\n    ")
		} else {
			b.WriteString(",\n    ")
		}
		first = false
		b.WriteString(property.ColumnName)
		b.WriteByte(' ')
		if m.KeyStrategy == core.KeyDatabaseGenerated && property.IsKey {
			// The exact spelling that makes the column the rowid alias.
			b.WriteString("INTEGER PRIMARY KEY")
			continue
		}
		b.WriteString(d.StorageType(property))
		if !property.IsNullable {
			b.WriteString(" NOT NULL")
		}
		if m.KeyStrategy == core.KeyClientGuid && property.IsKey {
			b.WriteString(" PRIMARY KEY")
		}
	}
	if m.KeyStrategy == core.KeyNatural && len(m.KeyProperties) > 0 {
		b.WriteString(",\n    primary key (")
		b.WriteString(strings.Join(columnNames(m.KeyProperties), ", "))
		b.WriteByte(')')
	}
	b.WriteString("\n) STRICT")
	return b.String()
}

func (*Dialect) CreateIndexSQL(m *core.EntityMap) []string {
	statements := make([]string, 0, len(m.Indexes))
	for _, index := range m.Indexes {
		unique := ""
		if index.Unique {
			unique = "unique "
		}
		columns := make([]string, len(index.Columns))
		for i, column := range index.Columns {
			columns[i] = column.ColumnName
			if column.Descending {
				columns[i] += " desc"
			}
		}
		statements = append(statements,
			"create "+unique+"index if not exists "+index.Name+" on "+m.RelationName+" ("+strings.Join(columns, ", ")+")")
	}
	return statements
}

func (*Dialect) InsertSQL(m *core.EntityMap) string {
	var columns, placeholders []string
	for _, p := range m.Properties {
		if p.IsGenerated {
			continue
		}
		columns = append(columns, p.ColumnName)
		placeholders = append(placeholders, "@"+p.ColumnName)
	}
	sql := "insert into " + m.RelationName +
		" (" + strings.Join(columns, ", ") + ")" +
		" values (" + strings.Join(placeholders, ", ") + ")"
	if m.KeyStrategy == core.KeyDatabaseGenerated {
		sql += " returning " + m.KeyProperties[0].ColumnName
	}
	return sql
}

func (*Dialect) UpdateSQL(m *core.EntityMap) string {
	// Generated non-key columns are database-owned: never in SET (mirrors the
	// insert exclusion and Update's binding filter).
	var assignments []string
	for _, p := range m.Properties {
		if !p.IsKey && !p.IsVersion && !p.IsGenerated {
			assignments = append(assignments, p.ColumnName+" = @"+p.ColumnName)
		}
	}
	sql := "update " + m.RelationName
	if version := m.VersionProperty; version != nil {
		assignments = append(assignments, version.ColumnName+" = "+version.ColumnName+" + 1")
	}
	sql += " set " + strings.Join(assignments, ", ") + " where " + keyPredicate(m)
	if version := m.VersionProperty; version != nil {
		sql += " and " + version.ColumnName + " = @" + version.ColumnName
	}
	return sql
}

func (*Dialect) DeleteSQL(m *core.EntityMap, checkVersion bool) string {
	sql := "delete from " + m.RelationName + " where " + keyPredicate(m)
	if version := m.VersionProperty; checkVersion && version != nil {
		sql += " and " + version.ColumnName + " = @" + version.ColumnName
	}
	return sql
}

func keyPredicate(m *core.EntityMap) string {
	parts := make([]string, len(m.KeyProperties))
	for i, key := range m.KeyProperties {
		parts[i] = key.ColumnName + " = @" + key.ColumnName
	}
	return strings.Join(parts, " and ")
}

func columnNames(properties []*core.PropertyMap) []string {
	names := make([]string, len(properties))
	for i, p := range properties {
		names[i] = p.ColumnName
	}
	return names
}

// StorageType is the token → SQLite storage type per the §7.9 conventions
// (dates, decimals, GUIDs, JSON, and handler types as TEXT).
func (*Dialect) StorageType(property *core.PropertyMap) string {
	switch property.ColumnType {
	case core.TypeEnumInt, core.TypeInt16, core.TypeInt32, core.TypeInt64, core.TypeBool:
		return "INTEGER"
	case core.TypeDouble, core.TypeFloat:
		return "REAL"
	case core.TypeBytes:
		return "BLOB"
	}
	return "TEXT"
}

// DescribeStatement prepares the statement and reads its result columns
// without executing it (core.StatementDescriber): the driver's ColumnInfo
// exposes sqlite3_column_decltype/table_name/origin_name — what the
// reference reads through GetColumnSchema.
func (*Dialect) DescribeStatement(ctx context.Context, conn *sql.Conn, statement string) ([]core.ColumnDescription, error) {
	var described []core.ColumnDescription
	err := conn.Raw(func(driverConn any) error {
		ci, ok := driverConn.(interface {
			ColumnInfo(query string) ([]sqlitedriver.ColumnInfo, error)
		})
		if !ok {
			return fmt.Errorf("the %T driver connection does not expose ColumnInfo", driverConn)
		}
		info, err := ci.ColumnInfo(statement)
		if err != nil {
			return err
		}
		described = make([]core.ColumnDescription, len(info))
		for i, column := range info {
			described[i] = core.ColumnDescription{
				Name:         column.Name,
				DeclaredType: column.DeclType,
				Table:        column.TableName,
				OriginColumn: column.OriginName,
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return described, nil
}
