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
