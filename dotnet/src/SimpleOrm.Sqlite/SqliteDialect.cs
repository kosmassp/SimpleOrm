using System.Data.Common;
using System.Text;
using Microsoft.Data.Sqlite;

namespace SimpleOrm.Sqlite;

/// <summary>SQLite implementation of <see cref="IDialect"/> backed by Microsoft.Data.Sqlite.</summary>
public sealed class SqliteDialect : IDialect
{
    public string Name => "sqlite";

    public DbConnection CreateConnection(string connectionString)
        => new SqliteConnection(connectionString);

    /// <summary>Unquoted (ADR-0024): snake_case names need nothing, and the reference renderings stay byte-identical.</summary>
    public string QuoteIdentifier(string identifier) => identifier;

    public string LimitOffsetClause(string? limitParameter, string? offsetParameter) => (limitParameter, offsetParameter) switch
    {
        (not null, not null) => $"limit {limitParameter} offset {offsetParameter}",
        (not null, null) => $"limit {limitParameter}",
        (null, not null) => $"limit -1 offset {offsetParameter}",   // SQLite requires LIMIT before OFFSET
        _ => string.Empty,
    };

    public string SelectSql(SelectAst select, BindCriteriaParameter bindParameter)
        => AnsiSelectRenderer.SelectSql(this, select, bindParameter);

    public bool SupportsArrayParameters => false;

    public bool BindsTemporalsNatively => false;   // TEXT storage: the ISO-8601 string IS the value (§7.9)

    public bool PagingRequiresOrderBy => false;

    public bool SupportsRowValueIn => true;

    public bool SupportsMaterializedViews => false;

    public bool SupportsProcedures => false;

    public bool SupportsTransactionalDdl => true;

    // The fifth column (ADR-0043): the rowid alias — a lone INTEGER PRIMARY KEY — is the
    // one database-generated column SQLite has.
    public string ColumnsInfoSql
        => "select name, type, \"notnull\", pk, "
            + "case when pk = 1 and upper(type) = 'INTEGER' and (select count(*) from pragma_table_info(@relation) where pk > 0) = 1 then 1 else 0 end as generated "
            + "from pragma_table_info(@relation)";

    /// <summary>sqlite_master keeps the text as submitted (minus IF NOT EXISTS, the prefix recased): the guard compares text.</summary>
    public bool ViewDefinitionIsVerbatim => true;

    public string ViewDefinitionSql
        => "select sql from sqlite_master where type = 'view' and name = @relation";

    public string IndexesInfoSql
        => "select il.name, il.\"unique\", ii.seqno, ii.name, ii.\"desc\" "
            + "from pragma_index_list(@relation) il, pragma_index_xinfo(il.name) ii "
            + "where il.origin = 'c' and ii.key = 1 order by il.name, ii.seqno";

    public bool IsDeclaredTypeCompatible(string declaredType, Type clrType, bool enumAsInt)
    {
        var type = Nullable.GetUnderlyingType(clrType) ?? clrType;
        var declared = declaredType.Trim().ToUpperInvariant();
        if (declared is "" or "ANY")
        {
            return true;   // untyped: SQLite enforces nothing, nothing to contradict
        }

        if (type.IsEnum)
        {
            return enumAsInt ? declared is "INT" or "INTEGER" : declared == "TEXT";
        }

        return declared switch
        {
            "INT" or "INTEGER" => type == typeof(int) || type == typeof(long) || type == typeof(short) || type == typeof(bool),
            "REAL" => type == typeof(double) || type == typeof(float),
            "BLOB" => type == typeof(byte[]) || type == typeof(Guid),
            "TEXT" => type == typeof(string) || type == typeof(decimal) || type == typeof(Guid)
                || type == typeof(DateTime) || type == typeof(DateTimeOffset)
                || type.FullName is "System.DateOnly" or "System.TimeOnly",
            _ => false,
        };
    }

    public DbTransaction BeginMigrationRunLock(DbConnection connection)
        => ((SqliteConnection)connection).BeginTransaction(deferred: false);   // BEGIN IMMEDIATE

    public string VersionTableSql
        => """
           create table if not exists schema_version (
               version      INTEGER NOT NULL,
               object       TEXT NOT NULL,
               description  TEXT NOT NULL,
               checksum     TEXT NOT NULL,
               applied_at   TEXT NOT NULL,
               execution_ms INTEGER NOT NULL,
               primary key (version, object)
           ) STRICT
           """;

    // Migration-action DDL (ADR-0024): these strings are frozen — recorded
    // checksums hash them.

    public string RenameTableSql(string fromName, string toName)
        => $"alter table {fromName} rename to {toName}";

    public string RenameColumnSql(string table, string fromName, string toName)
        => $"alter table {table} rename column {fromName} to {toName}";

    public string AddColumnSql(string table, string column, string storageType, bool nullable, string? defaultSql)
        => $"alter table {table} add column {column} {storageType}"
            + (nullable ? string.Empty : " not null")
            + (defaultSql is null ? string.Empty : " default " + defaultSql);

    public string DropColumnSql(string table, string column)
        => $"alter table {table} drop column {column}";

    public string DropTableSql(string table)
        => "drop table " + table;

    public string DropIndexSql(string table, string index)
        => "drop index " + index;

    public string CreateViewSql(EntityMap map)
        => CreateViewSql(map.RelationName!, map.DefiningSql!, map.Kind == RelationKind.MaterializedView);

    public string CreateViewSql(string view, string body, bool materialized)
        => "create view if not exists " + view + " as\n" + body;   // no materialized views here (DDL-002 upstream)

    public string DropViewSql(string view, bool materialized)
        => "drop view if exists " + view;

    /// <summary>The snapshot rendering (ADR-0017): the reference text, SQLite-spelled, STRICT.</summary>
    public string CreateTableSql(TableSchema schema) => SnapshotDdl.CreateTableSql(schema);

    public string CreateIndexSql(string table, TableSchema.Index index) => SnapshotDdl.CreateIndexSql(table, index);

    // --- triggers (ADR-0042) ---------------------------------------------------------

    /// <summary>
    /// SQLite triggers are row-level and single-event: a multi-event declaration
    /// renders one trigger per event named <c>&lt;name&gt;_&lt;event&gt;</c>, a
    /// single-event one keeps the declared name. INSTEAD OF exists only on views
    /// (<c>DDL-005</c> on a table). The body's rows are one-row derived tables
    /// over <c>new</c>/<c>old</c>; <c>{now}</c> is the §7.9 ISO-8601 UTC text.
    /// </summary>
    public IReadOnlyList<string> CreateTriggerSql(string relation, bool onView, TriggerDefinition trigger)
    {
        if (trigger.Timing == TriggerTiming.InsteadOf && !onView)
        {
            throw new SimpleOrmException("DDL-005", trigger.Name, "SQLite has INSTEAD OF triggers on views only; declare Before or After on a table");
        }

        var body = TriggerTokens.Render(trigger.Sql, segment => segment.Kind switch
        {
            "new" => TriggerTokens.RowSelect("new", segment.Columns, QuoteIdentifier),
            "old" => TriggerTokens.RowSelect("old", segment.Columns, QuoteIdentifier),
            _ => "strftime('%Y-%m-%dT%H:%M:%fZ', 'now')",
        });
        var events = trigger.EventList().ToArray();
        return events
            .Select(e => "create trigger " + TriggerObjectName(trigger.Name, e, events.Length)
                + " " + Timing(trigger.Timing) + " " + EventWord(e)
                + (e == TriggerEvent.Update && trigger.UpdateOf.Count > 0 ? " of " + string.Join(", ", trigger.UpdateOf) : string.Empty)
                + " on " + relation + " for each row\nbegin\n" + body.Trim() + "\nend")
            .ToArray();
    }

    public IReadOnlyList<string> DropTriggerSql(string relation, string triggerName)
        => new[] { triggerName, triggerName + "_insert", triggerName + "_update", triggerName + "_delete" }
            .Select(name => "drop trigger if exists " + name)
            .ToArray();

    /// <summary>The stored create text, as SQLite keeps it (the leading keywords recased); the declared name or its per-event forms.</summary>
    public string TriggerDefinitionSql
        => "select sql as definition from sqlite_master where type = 'trigger' "
            + "and name in (@relation, @relation || '_insert', @relation || '_update', @relation || '_delete') order by name";

    /// <summary>The stored object name: the declared name, or <c>&lt;name&gt;_&lt;event&gt;</c> when the declaration has several events.</summary>
    public static string TriggerObjectName(string name, TriggerEvent e, int eventCount)
        => eventCount == 1 ? name : name + "_" + EventWord(e);

    private static string Timing(TriggerTiming timing) => timing switch
    {
        TriggerTiming.Before => "before",
        TriggerTiming.After => "after",
        _ => "instead of",
    };

    private static string EventWord(TriggerEvent e) => e switch
    {
        TriggerEvent.Insert => "insert",
        TriggerEvent.Update => "update",
        _ => "delete",
    };

    public string CreateTableSql(EntityMap map)
    {
        var builder = new StringBuilder("create table if not exists ")
            .Append(map.RelationName).Append(" (");

        var first = true;
        foreach (var property in map.Properties)
        {
            builder.Append(first ? "\n    " : ",\n    ").Append(property.ColumnName).Append(' ');
            first = false;

            if (map.KeyStrategy == KeyStrategy.DatabaseGenerated && property.IsKey)
            {
                // The exact spelling that makes the column the rowid alias.
                builder.Append("INTEGER PRIMARY KEY");
                continue;
            }

            builder.Append(ColumnType(property));
            if (!property.IsNullable)
            {
                builder.Append(" NOT NULL");
            }

            if (map.KeyStrategy == KeyStrategy.ClientGuid && property.IsKey)
            {
                builder.Append(" PRIMARY KEY");
            }
        }

        if (map.KeyStrategy == KeyStrategy.Natural && map.KeyProperties.Count > 0)
        {
            builder.Append(",\n    primary key (")
                .Append(string.Join(", ", map.KeyProperties.Select(k => k.ColumnName)))
                .Append(')');
        }

        return builder.Append("\n) STRICT").ToString();
    }

    public IReadOnlyList<string> CreateIndexSql(EntityMap map)
        => map.Indexes
            .Select(index =>
                "create " + (index.Unique ? "unique " : string.Empty) + "index if not exists " + index.Name
                + " on " + map.RelationName + " ("
                + string.Join(", ", index.Columns.Select(c => c.ColumnName + (c.Descending ? " desc" : string.Empty)))
                + ")")
            .ToArray();

    public string InsertSql(EntityMap map)
    {
        var columns = map.Properties.Where(p => !p.IsGenerated).ToArray();
        var sql = "insert into " + map.RelationName
            + " (" + string.Join(", ", columns.Select(c => c.ColumnName)) + ")"
            + " values (" + string.Join(", ", columns.Select(c => "@" + c.ColumnName)) + ")";

        if (map.KeyStrategy == KeyStrategy.DatabaseGenerated)
        {
            sql += " returning " + map.KeyProperties[0].ColumnName;
        }

        return sql;
    }

    public string UpdateSql(EntityMap map)
        // Generated non-key columns are database-owned: never in SET (mirrors the
        // insert exclusion and Db.UpdateAsync's binding filter).
        => RenderUpdate(map, map.Properties.Where(p => !p.IsKey && !p.IsVersion && !p.IsGenerated));

    public string UpdateOnlySql(EntityMap map, IReadOnlyList<PropertyMap> properties)
        => RenderUpdate(map, properties);

    private static string RenderUpdate(EntityMap map, IEnumerable<PropertyMap> set)
    {
        var assignments = set
            .Select(p => p.ColumnName + " = @" + p.ColumnName)
            .ToList();
        if (map.VersionProperty is { } version)
        {
            assignments.Add(version.ColumnName + " = " + version.ColumnName + " + 1");
        }

        return "update " + map.RelationName
            + " set " + string.Join(", ", assignments)
            + " where " + KeyPredicate(map)
            + (map.VersionProperty is { } v ? " and " + v.ColumnName + " = @" + v.ColumnName : string.Empty);
    }

    public string DeleteSql(EntityMap map, bool checkVersion)
        => "delete from " + map.RelationName
            + " where " + KeyPredicate(map)
            + (checkVersion && map.VersionProperty is { } v ? " and " + v.ColumnName + " = @" + v.ColumnName : string.Empty);

    /// <summary>SQLite has no triggers-inflate-the-count problem: <c>changes()</c> is the statement's own count (ADR-0040 add.3).</summary>
    public bool ReportsAffectedRowsBySelect => false;

    private static string KeyPredicate(EntityMap map)
        => string.Join(" and ", map.KeyProperties.Select(k => k.ColumnName + " = @" + k.ColumnName));

    /// <summary>SQLite's storage classes have no facets: a declared length or precision is ignored here.</summary>
    public string StorageType(EntityMap map, PropertyMap property) => ColumnType(property);

    /// <summary>CLR → SQLite storage type per the §7.9 conventions (dates/decimals/GUIDs as TEXT).</summary>
    private static string ColumnType(PropertyMap property)
    {
        var type = Nullable.GetUnderlyingType(property.ClrType) ?? property.ClrType;
        if (type.IsEnum)
        {
            return property.EnumAsInt ? "INTEGER" : "TEXT";
        }

        if (type == typeof(int) || type == typeof(long) || type == typeof(short) || type == typeof(bool))
        {
            return "INTEGER";
        }

        if (type == typeof(double) || type == typeof(float))
        {
            return "REAL";
        }

        if (type == typeof(byte[]))
        {
            return "BLOB";
        }

        // string, decimal, DateTime/Offset, DateOnly/TimeOnly, Guid, JSON, handler types.
        return "TEXT";
    }
}
