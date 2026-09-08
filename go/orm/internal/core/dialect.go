package core

import (
	"context"
	"database/sql"
)

// BindCriteriaParameter binds one criteria value; property is the mapped
// property the value compares against (its conversion rules — e.g. enum_int —
// apply), or nil for paging values. It returns the placeholder to render.
type BindCriteriaParameter func(value any, property *PropertyMap) (string, error)

// Dialect is the seam between the provider-neutral core and a database
// provider (§7.25). The member set is exactly the C# IDialect's — same names
// under Go's initialism rule (CreateTableSql → CreateTableSQL), same contracts
// — so a fix or a new member in one implementation is findable in the other.
// Minimal and capability-based: members are added only when a second dialect
// needs them, and SQLite's renderings then stay byte-identical (they are pinned
// by conformance and hashed into migration checksums).
type Dialect interface {
	// CreateConnection opens the driver-level pool for a connection string (a
	// bare SQLite path is accepted). Nothing connects until first use.
	CreateConnection(connectionString string) (*sql.DB, error)

	// QuoteIdentifier quotes one identifier (ADR-0024). SQLite returns the name
	// unquoted — the reference renderings stay byte-identical.
	QuoteIdentifier(identifier string) string

	// --- generated DDL and CRUD from the EntityMap (ADR-0011, ADR-0008 add.3) ---

	// CreateTableSQL renders CREATE TABLE IF NOT EXISTS for a table-backed map:
	// column types from the metadata, NOT NULL from nullability, the key per its
	// strategy, STRICT on SQLite.
	CreateTableSQL(m *EntityMap) string

	// CreateIndexSQL renders CREATE INDEX IF NOT EXISTS for each declared index.
	CreateIndexSQL(m *EntityMap) []string

	// CreateViewSQL renders CREATE (MATERIALIZED) VIEW from the map's defining SQL.
	CreateViewSQL(m *EntityMap) string

	// InsertSQL renders the generated INSERT (§7.14): explicit column list, every
	// non-generated column, RETURNING the key when the database generates it.
	// Placeholders are @<column>.
	InsertSQL(m *EntityMap) string

	// UpdateSQL renders the generated full-row UPDATE by key (§7.15/§7.16): the
	// version set to version + 1 and required in the WHERE when mapped.
	UpdateSQL(m *EntityMap) string

	// UpdateOnlySQL renders the update-by-column-list UPDATE (ADR-0028): the SET
	// holds exactly properties (validated by the session: mapped, non-key,
	// non-version, non-generated, no repeats); version and WHERE rules as UpdateSQL.
	UpdateOnlySQL(m *EntityMap, properties []*PropertyMap) string

	// DeleteSQL renders the generated DELETE by key; with checkVersion the WHERE also requires the version (§7.16).
	DeleteSQL(m *EntityMap, checkVersion bool) string

	// --- criteria rendering (§10.4, ADR-0020) ---

	// LimitOffsetClause renders the paging clause from pre-bound placeholder names; either may be "".
	LimitOffsetClause(limitParameter, offsetParameter string) string

	// SelectSQL renders the SELECT for a criteria query AST. bind binds a value
	// (with the mapped property it compares against, so per-column conversion
	// applies; nil for paging values) and returns its placeholder; call it in
	// render order. Delegate to the reference renderer unless this dialect's SQL
	// disagrees with it. QRY-006/007/008 come back as errors.
	SelectSQL(ast *SelectAst, bind BindCriteriaParameter) (string, error)

	// PagingRequiresOrderBy: whether the paging clause is only legal after ORDER BY (ADR-0024; SQL Server).
	PagingRequiresOrderBy() bool

	// SupportsRowValueIn: whether (a, b) in (select …) row-value membership parses (ADR-0024; SQLite: yes).
	SupportsRowValueIn() bool

	// --- capability flags ---

	// SupportsArrayParameters: real array parameters (§7.12; SQLite: no — the IN-expansion strategy applies).
	SupportsArrayParameters() bool

	// BindsTemporalsNatively: temporals bind as native values instead of §7.9 ISO-8601 strings (ADR-0025; SQLite: no).
	BindsTemporalsNatively() bool

	SupportsMaterializedViews() bool

	SupportsProcedures() bool

	// SupportsTransactionalDDL: whether DDL participates in transactions (§7.23; SQLite: yes).
	SupportsTransactionalDDL() bool

	// --- introspection (§7.25) ---

	// ColumnsInfoSQL: columns of a relation — parameter @relation; result
	// columns name, type, notnull, pk (empty = relation missing).
	ColumnsInfoSQL() string

	// ViewDefinitionSQL: a view's stored create DDL — parameter @relation; no
	// rows = view absent (backs MIG-012 and view snapshots).
	ViewDefinitionSQL() string

	// IndexesInfoSQL: a table's explicitly created indexes — parameter
	// @relation; columns index_name, unique, seqno, column, desc, ordered by
	// index then position.
	IndexesInfoSQL() string

	// IsDeclaredTypeCompatible is the declared-type → neutral-type compatibility
	// table (§7.19 VAL-011). A custom token is compatible only through a handler,
	// which the caller checks.
	IsDeclaredTypeCompatible(declaredType string, columnType ColumnType) bool

	// StorageType is the storage (declared) type a mapped property renders to —
	// what CREATE TABLE emits (ADR-0017).
	StorageType(property *PropertyMap) string

	// --- migrations (§7.23) ---

	// BeginMigrationRunLock starts the run lock on the connection: one
	// transaction held for the whole run (SQLite: BEGIN IMMEDIATE).
	BeginMigrationRunLock(ctx context.Context, conn *sql.Conn) (*sql.Tx, error)

	// VersionTableSQL is the idempotent DDL for the schema_version table
	// (ADR-0024): same columns everywhere, dialect-native types and guard.
	VersionTableSQL() string

	// Typed migration actions render through the dialect (ADR-0024); SQLite's
	// strings are frozen — recorded checksums hash them.

	RenameTableSQL(fromName, toName string) string

	RenameColumnSQL(table, fromName, toName string) string

	// AddColumnSQL adds a column with a literal storage type; a non-null addition carries the caller's default ("" for none).
	AddColumnSQL(table, column, storageType string, nullable bool, defaultSQL string) string

	DropColumnSQL(table, column string) string

	DropTableSQL(table string) string

	// DropIndexSQL drops an index; dialects that scope index names to the table (SQL Server) need both.
	DropIndexSQL(table, index string) string
}
