using System.Data.Common;

namespace SimpleOrm;

/// <summary>
/// The seam between the provider-neutral core and a database provider.
/// Minimal and capability-based; members are added only when a milestone needs them
/// (see CLAUDE.md §7.25 for the full Level 1 member list).
/// </summary>
public interface IDialect
{
    /// <summary>Creates an unopened connection for the given connection string.</summary>
    DbConnection CreateConnection(string connectionString);

    /// <summary>
    /// Quotes one identifier for this dialect's SQL (ADR-0024). SQLite returns the
    /// name unquoted — snake_case names need nothing, and the reference renderings
    /// (conformance pins, migration checksums) stay byte-identical; SQL Server
    /// brackets everything, because a legacy schema is full of reserved words.
    /// </summary>
    string QuoteIdentifier(string identifier);

    /// <summary>
    /// Renders <c>CREATE TABLE IF NOT EXISTS</c> for a table-backed map (ADR-0011):
    /// column types from the metadata, NOT NULL from nullability, the key per its
    /// strategy, STRICT on SQLite.
    /// </summary>
    string CreateTableSql(EntityMap map);

    /// <summary>Renders <c>CREATE INDEX IF NOT EXISTS</c> for each declared index.</summary>
    IReadOnlyList<string> CreateIndexSql(EntityMap map);

    /// <summary>Renders <c>CREATE (MATERIALIZED) VIEW</c> from the map's defining SQL (ADR-0008 addendum 3).</summary>
    string CreateViewSql(EntityMap map);

    /// <summary>Renders the limit/offset clause from pre-bound parameter names (§7.25); either may be null.</summary>
    string LimitOffsetClause(string? limitParameter, string? offsetParameter);

    /// <summary>
    /// Whether the paging clause is only legal after ORDER BY (ADR-0024; SQL
    /// Server's OFFSET/FETCH). When true, a paged select with no orderings gets the
    /// dialect-neutral placeholder <c>order by (select null)</c> from the renderer.
    /// </summary>
    bool PagingRequiresOrderBy { get; }

    /// <summary>
    /// Whether <c>(a, b) in (select …)</c> row-value membership parses (ADR-0022
    /// add.1 anticipated the override; SQLite: yes, SQL Server: no). When false the
    /// renderer rewrites a composite membership as a correlated EXISTS over the
    /// same subquery — identical semantics, identical parameter order.
    /// </summary>
    bool SupportsRowValueIn { get; }

    /// <summary>
    /// Renders the SELECT for a criteria query AST (§10.4, ADR-0020): front-ends
    /// produce <see cref="SelectAst"/> and never SQL text — the dialect turns the
    /// AST into SQL. <paramref name="bindParameter"/> binds a value (with the
    /// mapped property it compares against, so per-column conversion like
    /// <c>[EnumAsInt]</c> applies) and returns its placeholder; call it in render
    /// order. Delegate to <see cref="AnsiSelectRenderer.SelectSql"/> unless this
    /// dialect's SQL disagrees with the reference rendering.
    /// </summary>
    string SelectSql(SelectAst select, BindCriteriaParameter bindParameter);

    /// <summary>
    /// Whether the database has real array parameters (§7.12; SQLite: no). False
    /// is the IN-expansion strategy (<c>@ids_0…</c>); true (ADR-0025 Postgres)
    /// binds a collection-typed property as <b>one</b> parameter, SQL untouched —
    /// the registry SQL is written dialect-natively (<c>= any(@ids)</c>), and the
    /// observable contract (always parameterized, empty matches no rows) holds
    /// either way (spec/session.md).
    /// </summary>
    bool SupportsArrayParameters { get; }

    /// <summary>
    /// Whether temporal values bind as native CLR values instead of the §7.9
    /// ISO-8601 strings (ADR-0025). SQLite stores TEXT — strings are the storage;
    /// SQL Server accepts strings via implicit conversion; Postgres does neither
    /// (<c>timestamptz >= text</c> refuses), so its provider receives
    /// DateTime/DateTimeOffset (UTC-normalized; Kind=Unspecified still refuses
    /// with <c>VAL-020</c>) and DateOnly/TimeOnly directly.
    /// </summary>
    bool BindsTemporalsNatively { get; }

    /// <summary>
    /// Introspection query for a relation's columns (§7.25): parameter <c>@relation</c>,
    /// result columns <c>name, type, notnull, pk</c> (empty result = relation missing).
    /// Used by SchemaGuard and the future diff generator.
    /// </summary>
    string ColumnsInfoSql { get; }

    /// <summary>The declared-type → CLR compatibility table (§7.25), honoring [EnumAsInt].</summary>
    bool IsDeclaredTypeCompatible(string declaredType, Type clrType, bool enumAsInt);

    /// <summary>
    /// Query for a view's stored create DDL (parameter <c>@relation</c>; no rows =
    /// view absent). Backs the <c>ExpectDefinition</c> migration guard (MIG-012)
    /// and the shadow replayer's view snapshots (ADR-0017).
    /// </summary>
    string ViewDefinitionSql { get; }

    /// <summary>
    /// Introspection query for a table's explicitly created indexes (§7.25):
    /// parameter <c>@relation</c>, one row per key column — columns
    /// <c>index_name, unique, seqno, column, desc</c>, ordered by index then
    /// position. Backs force sync's structural index match (ADR-0017 add.2):
    /// indexes compare by columns/direction/uniqueness, never by name.
    /// </summary>
    string IndexesInfoSql { get; }

    /// <summary>The storage (declared) type a mapped property renders to — what CREATE TABLE emits (§7.25).</summary>
    string StorageType(PropertyMap property);

    /// <summary>Whether the database has materialized views (SQLite: no; creating one throws <c>DDL-002</c>).</summary>
    bool SupportsMaterializedViews { get; }

    /// <summary>Whether the database has stored procedures / set-returning functions (SQLite: no).</summary>
    bool SupportsProcedures { get; }

    /// <summary>Whether DDL participates in transactions (SQLite: yes) — §7.23.</summary>
    bool SupportsTransactionalDdl { get; }

    /// <summary>
    /// The migration run lock (§7.23): a transaction held for the whole run. On
    /// SQLite this is <c>BEGIN IMMEDIATE</c> — exclusive writer, and with
    /// transactional DDL it also makes a failed run fully atomic. A future Postgres
    /// dialect evolves this member (advisory lock + per-migration transactions).
    /// </summary>
    DbTransaction BeginMigrationRunLock(DbConnection connection);

    /// <summary>
    /// Idempotent DDL for the runner's <c>schema_version</c> table (§7.23,
    /// ADR-0024): same columns and semantics everywhere, dialect-native types and
    /// existence guard (SQLite: <c>IF NOT EXISTS</c> + STRICT; SQL Server:
    /// <c>if object_id(…) is null</c>).
    /// </summary>
    string VersionTableSql { get; }

    // --- migration-action DDL (ADR-0024): the typed actions (§7.22) and the
    // derived rollback render through the dialect, because ALTER TABLE grammar
    // diverges (SQLite: "add column"; SQL Server: "add", sp_rename). The SQLite
    // renderings are frozen — recorded checksums hash them.

    /// <summary>Renames a table.</summary>
    string RenameTableSql(string fromName, string toName);

    /// <summary>Renames a column, data-preservingly.</summary>
    string RenameColumnSql(string table, string fromName, string toName);

    /// <summary>Adds a column with a literal storage type; non-null additions carry the caller's default.</summary>
    string AddColumnSql(string table, string column, string storageType, bool nullable, string? defaultSql);

    /// <summary>Drops a column.</summary>
    string DropColumnSql(string table, string column);

    /// <summary>Drops a table.</summary>
    string DropTableSql(string table);

    /// <summary>Drops an index; dialects that scope index names to the table (SQL Server) need both.</summary>
    string DropIndexSql(string table, string index);

    /// <summary>
    /// Renders the generated INSERT (§7.14): explicit column list, every
    /// non-generated column, <c>RETURNING</c> the key when the database generates it.
    /// Placeholders are <c>@&lt;column&gt;</c>.
    /// </summary>
    string InsertSql(EntityMap map);

    /// <summary>
    /// Renders the generated full-row UPDATE by key (§7.15/§7.16): every mapped
    /// non-key, non-version column; when a version column is mapped, it is set to
    /// <c>version + 1</c> and the WHERE clause requires the caller's version.
    /// </summary>
    string UpdateSql(EntityMap map);

    /// <summary>Renders the generated DELETE by key; with <paramref name="checkVersion"/>, the WHERE also requires the version (§7.16).</summary>
    string DeleteSql(EntityMap map, bool checkVersion);
}
