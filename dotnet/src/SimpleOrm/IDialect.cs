using System.Data.Common;

namespace SimpleOrm;

/// <summary>
/// The seam between the provider-neutral core and a database provider.
/// Minimal and capability-based; members are added only when a milestone needs them
/// (see CLAUDE.md §7.25 for the full Level 1 member list).
/// </summary>
public interface IDialect
{
    /// <summary>
    /// The dialect's name as the CLI spells it (<c>sqlite</c>, <c>sqlserver</c>,
    /// <c>postgres</c>; ADR-0043). Stamped into non-SQLite snapshots and the
    /// generated root; a snapshot stamped for another dialect is <c>MIG-014</c>.
    /// </summary>
    string Name { get; }

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

    // --- snapshot-driven DDL (ADR-0043): the derived rollback, the shadow's trusted
    // baseline, and the MIG-012 probe render from recorded shapes, not from metadata,
    // so every dialect renders them itself — the tooling paths carry no SQLite text.

    /// <summary>Renders the create statement of a view from its neutral snapshot (name and body; ADR-0043) — the same text <see cref="CreateViewSql(EntityMap)"/> renders for a map of that shape.</summary>
    string CreateViewSql(string view, string body, bool materialized);

    /// <summary>Drops a view (or a materialized view, where the dialect has them) if it exists.</summary>
    string DropViewSql(string view, bool materialized);

    /// <summary>Renders a whole table from a snapshot (the shadow's trusted <c>--from</c> baseline, ADR-0017): storage types as recorded, the key per its flags.</summary>
    string CreateTableSql(TableSchema schema);

    /// <summary>Renders one index from a snapshot (the derived rollback restoring it, the generator emitting it).</summary>
    string CreateIndexSql(string table, TableSchema.Index index);

    /// <summary>
    /// Whether <see cref="ViewDefinitionSql"/> returns the view's definition as
    /// submitted (SQLite, SQL Server), so the <c>MIG-012</c> guard compares
    /// normalized text; false where the engine reformats it (PostgreSQL's
    /// <c>pg_get_viewdef</c>), so the guard round-trips the expected body through
    /// a probe view and compares the engine's two renderings (ADR-0043).
    /// </summary>
    bool ViewDefinitionIsVerbatim { get; }

    // --- triggers (ADR-0042) ---------------------------------------------------------

    /// <summary>
    /// The create statements of a trigger, from its neutral declaration: the
    /// dialect's header (timing, events, UPDATE OF, granularity), its wrapper
    /// (<c>begin/end</c>, <c>set nocount on</c>, a PostgreSQL function and its
    /// <c>return</c>), and the body with the <c>{new}</c>/<c>{old}</c>/<c>{now}</c>
    /// tokens expanded. No drop: <see cref="DropTriggerSql"/> precedes it when the
    /// caller wants replace semantics. These statements are also what
    /// <see cref="TriggerDefinitionSql"/> reports back for the trigger, normalized —
    /// the symmetric contract the <c>MIG-012</c> guard relies on. A declaration the
    /// dialect has no form for throws <c>DDL-005</c>; <paramref name="onView"/>
    /// says whether the relation is a view (INSTEAD OF on a table exists only on
    /// SQL Server).
    /// </summary>
    IReadOnlyList<string> CreateTriggerSql(string relation, bool onView, TriggerDefinition trigger);

    /// <summary>
    /// Drops the trigger named by a declaration, idempotently, with whatever the
    /// dialect created beside it (PostgreSQL's function; SQLite's per-event
    /// triggers of a multi-event declaration).
    /// </summary>
    IReadOnlyList<string> DropTriggerSql(string relation, string triggerName);

    /// <summary>
    /// Introspection query for a trigger's stored definition: parameter
    /// <c>@relation</c> is the declared trigger name, one result column
    /// <c>definition</c>, one row per statement the dialect created for it (a
    /// multi-event SQLite declaration yields one row per event; PostgreSQL the
    /// function and the trigger); no rows when absent. Each row, normalized,
    /// equals the matching <see cref="CreateTriggerSql"/> statement. Backs the
    /// <c>MIG-012</c> guard on trigger steps and the shadow replayer.
    /// </summary>
    string TriggerDefinitionSql { get; }

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
    /// result columns <c>name, type, notnull, pk, generated</c> (empty result =
    /// relation missing); <c>type</c> is the storage type as the dialect spells it,
    /// <c>generated</c> whether the database generates the value (identity; SQLite's
    /// rowid alias) — ADR-0043. Used by SchemaGuard, force sync, and the shadow.
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

    /// <summary>
    /// The storage (declared) type a mapped property renders to — what CREATE TABLE
    /// emits, what introspection reports back, and what snapshots, diff, and force
    /// sync compare (§7.25, ADR-0043). Spelled exactly as the dialect's
    /// <see cref="ColumnsInfoSql"/> reports it. Needs the map because a key column
    /// or a column of a declared index may take an indexable length (SQL Server),
    /// and the property's storage facets (<see cref="PropertyMap.Length"/>,
    /// <see cref="PropertyMap.Precision"/>) override the dialect's defaults.
    /// </summary>
    string StorageType(EntityMap map, PropertyMap property);

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

    /// <summary>
    /// Renders the update-by-column-list UPDATE (ADR-0028): the SET holds exactly
    /// <paramref name="properties"/> (already validated by the session: mapped,
    /// non-key, non-version, non-generated, no repeats); the version and WHERE
    /// rules are those of <see cref="UpdateSql"/>.
    /// </summary>
    string UpdateOnlySql(EntityMap map, IReadOnlyList<PropertyMap> properties);

    /// <summary>Renders the generated DELETE by key; with <paramref name="checkVersion"/>, the WHERE also requires the version (§7.16).</summary>
    string DeleteSql(EntityMap map, bool checkVersion);

    /// <summary>
    /// Whether the generated UPDATE and DELETE renderings (<see cref="UpdateSql"/>,
    /// <see cref="UpdateOnlySql"/>, <see cref="DeleteSql"/>) report their affected
    /// row count as a trailing single-row, single-column select instead of the
    /// driver's count (ADR-0040 add.3). On SQL Server a trigger without
    /// <c>SET NOCOUNT ON</c> makes the driver add the trigger's rows to the
    /// statement's, so the "zero rows affected" rule behind <c>CRUD-010</c> and
    /// <c>CRUD-001</c> would pass a stale write on a triggered table; its renderings end in
    /// <c>select @@rowcount</c> and the session reads that scalar. SQLite and
    /// PostgreSQL report the statement's own count and keep the driver's. Only the
    /// generated writes are affected: a registry command's count is always the
    /// driver's — hand-written SQL is the author's.
    /// </summary>
    bool ReportsAffectedRowsBySelect { get; }
}
