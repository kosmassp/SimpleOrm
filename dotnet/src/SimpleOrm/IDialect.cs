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

    /// <summary>Whether the database has materialized views (SQLite: no; creating one throws <c>DDL-002</c>).</summary>
    bool SupportsMaterializedViews { get; }

    /// <summary>Whether the database has stored procedures / set-returning functions (SQLite: no).</summary>
    bool SupportsProcedures { get; }

    /// <summary>
    /// Renders the generated INSERT (§7.14): explicit column list, every
    /// non-generated column, <c>RETURNING</c> the key when the database generates it.
    /// Placeholders are <c>@&lt;column&gt;</c>.
    /// </summary>
    string InsertSql(EntityMap map);
}
