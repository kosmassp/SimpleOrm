using System.Data.Common;
using System.Text;
using Microsoft.Data.SqlClient;

namespace SimpleOrm.SqlServer;

/// <summary>
/// SQL Server implementation of <see cref="IDialect"/> backed by
/// Microsoft.Data.SqlClient (ADR-0024, the first Level 4 dialect — pulled forward
/// for the Fidelis field test). The divergences ADR-0023 predicted, resolved here:
/// generated keys via <c>scope_identity()</c> (not OUTPUT — it breaks on tables
/// with triggers, and legacy schemas have triggers), paging via OFFSET/FETCH
/// behind <see cref="PagingRequiresOrderBy"/>, composite subquery membership via
/// the renderer's EXISTS rewrite (<see cref="SupportsRowValueIn"/>), the migration
/// run lock via <c>sp_getapplock</c>, and identifiers always bracketed — a legacy
/// schema is full of reserved words. Types are enforced natively, so the SQLite
/// STRICT story needs no analog; the §7.9 date convention becomes
/// <c>datetimeoffset</c> (the marker-carrying storage — a plain
/// <c>datetime/datetime2</c> reads back as <c>VAL-020</c> unless an ITypeHandler
/// declares its kind).
/// </summary>
public sealed class SqlServerDialect : IDialect
{
    public string Name => "sqlserver";

    public DbConnection CreateConnection(string connectionString)
        => new SqlConnection(connectionString);

    /// <summary>Always bracketed (ADR-0024): predictable, and immune to reserved words.</summary>
    public string QuoteIdentifier(string identifier)
        => "[" + identifier.Replace("]", "]]") + "]";

    public string LimitOffsetClause(string? limitParameter, string? offsetParameter) => (limitParameter, offsetParameter) switch
    {
        (not null, not null) => $"offset {offsetParameter} rows fetch next {limitParameter} rows only",
        (not null, null) => $"offset 0 rows fetch next {limitParameter} rows only",   // FETCH requires OFFSET
        (null, not null) => $"offset {offsetParameter} rows",
        _ => string.Empty,
    };

    public string SelectSql(SelectAst select, BindCriteriaParameter bindParameter)
        => AnsiSelectRenderer.SelectSql(this, select, bindParameter);

    public bool SupportsArrayParameters => false;   // TVPs are not simple array parameters; IN-expansion applies (§7.12)

    /// <summary>ISO strings convert implicitly to datetimeoffset — the ADR-0024 binding, kept as shipped (ADR-0025).</summary>
    public bool BindsTemporalsNatively => false;

    public bool PagingRequiresOrderBy => true;   // OFFSET/FETCH is only legal after ORDER BY

    public bool SupportsRowValueIn => false;   // (a, b) in (select …) does not parse; the renderer rewrites as EXISTS

    /// <summary>Indexed views are a different mechanism (no <c>CREATE MATERIALIZED VIEW</c>, no explicit refresh).</summary>
    public bool SupportsMaterializedViews => false;

    public bool SupportsProcedures => true;

    public bool SupportsTransactionalDdl => true;

    // The introspection result columns carry the spec's names (`name, type, notnull, pk`;
    // `index_name, unique, seqno, column, desc`; `definition`) on every non-SQLite dialect —
    // SQLite's pragmas name them so already — so a consumer may read by name or by ordinal
    // (spec/dialects.md "Introspection result shapes"). The reference reads by ordinal.
    public string ColumnsInfoSql
        => """
           select c.name as [name],
                  case
                      when t.name in (N'decimal', N'numeric')
                          then t.name + N'(' + cast(c.precision as nvarchar(8)) + N',' + cast(c.scale as nvarchar(8)) + N')'
                      when t.name in (N'nvarchar', N'nchar')
                          then t.name + N'(' + case when c.max_length = -1 then N'max' else cast(c.max_length / 2 as nvarchar(8)) end + N')'
                      when t.name in (N'varchar', N'char', N'varbinary', N'binary')
                          then t.name + N'(' + case when c.max_length = -1 then N'max' else cast(c.max_length as nvarchar(8)) end + N')'
                      else t.name
                  end as [type],
                  cast(case when c.is_nullable = 1 then 0 else 1 end as bigint) as [notnull],
                  cast(isnull(pk.key_ordinal, 0) as bigint) as [pk],
                  cast(c.is_identity as bigint) as [generated]
           from sys.columns c
           join sys.types t on t.user_type_id = c.user_type_id
           left join (
               select ic.object_id, ic.column_id, ic.key_ordinal
               from sys.index_columns ic
               join sys.indexes i on i.object_id = ic.object_id and i.index_id = ic.index_id
               where i.is_primary_key = 1
           ) pk on pk.object_id = c.object_id and pk.column_id = c.column_id
           where c.object_id = object_id(@relation)
           order by c.column_id
           """;

    /// <summary><c>sys.sql_modules</c> keeps the text as submitted (<c>or alter</c> aside, which the normalizer strips): the guard compares text.</summary>
    public bool ViewDefinitionIsVerbatim => true;

    public string ViewDefinitionSql
        => "select m.definition as [definition] from sys.sql_modules m "
            + "join sys.views v on v.object_id = m.object_id "
            + "where v.object_id = object_id(@relation)";

    public string IndexesInfoSql
        => "select i.name as [index_name], cast(i.is_unique as bigint) as [unique], cast(ic.key_ordinal - 1 as bigint) as [seqno], "
            + "c.name as [column], cast(ic.is_descending_key as bigint) as [desc] "
            + "from sys.indexes i "
            + "join sys.index_columns ic on ic.object_id = i.object_id and ic.index_id = i.index_id and ic.is_included_column = 0 "
            + "join sys.columns c on c.object_id = ic.object_id and c.column_id = ic.column_id "
            + "where i.object_id = object_id(@relation) "
            + "and i.type > 0 and i.is_primary_key = 0 and i.is_unique_constraint = 0 "
            + "order by i.name, ic.key_ordinal";

    public bool IsDeclaredTypeCompatible(string declaredType, Type clrType, bool enumAsInt)
    {
        var type = Nullable.GetUnderlyingType(clrType) ?? clrType;
        var declared = BaseTypeName(declaredType);
        if (declared.Length == 0)
        {
            return false;   // SQL Server always declares a type; an empty one is a lie
        }

        if (type.IsEnum)
        {
            return enumAsInt
                ? declared is "int" or "bigint" or "smallint" or "tinyint"
                : declared is "nvarchar" or "varchar" or "nchar" or "char";
        }

        return declared switch
        {
            "int" or "tinyint" => type == typeof(int) || type == typeof(long) || type == typeof(short),
            "bigint" => type == typeof(long),
            "smallint" => type == typeof(short) || type == typeof(int) || type == typeof(long),
            "bit" => type == typeof(bool),
            "decimal" or "numeric" or "money" or "smallmoney" => type == typeof(decimal),
            "float" => type == typeof(double),
            "real" => type == typeof(float),
            "nvarchar" or "varchar" or "nchar" or "char" or "ntext" or "text" => type == typeof(string),
            "varbinary" or "binary" or "image" => type == typeof(byte[]),
            "uniqueidentifier" => type == typeof(Guid),
            // Markerless storage is still *type*-compatible with DateTime; the
            // VAL-020 kind rule applies per value at read time (TypeConverter).
            "datetime" or "datetime2" or "smalldatetime" => type == typeof(DateTime),
            "datetimeoffset" => type == typeof(DateTime) || type == typeof(DateTimeOffset),
            "date" => type == typeof(DateTime) || type.FullName == "System.DateOnly",
            "time" => type == typeof(TimeSpan) || type.FullName == "System.TimeOnly",
            _ => false,
        };
    }

    /// <summary>
    /// The run lock (§7.23): one transaction for the whole run, made exclusive by
    /// <c>sp_getapplock</c> — the ADR-0023 answer to SQLite's BEGIN IMMEDIATE. DDL
    /// is transactional on SQL Server, so a failed run still applies nothing.
    /// </summary>
    public DbTransaction BeginMigrationRunLock(DbConnection connection)
    {
        var transaction = connection.BeginTransaction();
        try
        {
            using var command = connection.CreateCommand();
            command.Transaction = transaction;
            command.CommandText =
                "declare @rc int; "
                + "exec @rc = sp_getapplock @Resource = N'simpleorm:migrations', @LockMode = 'Exclusive', "
                + "@LockOwner = 'Transaction', @LockTimeout = 60000; "
                + "if @rc < 0 raiserror(N'the SimpleOrm migration lock is held by another run', 16, 1)";
            command.ExecuteNonQuery();
            return transaction;
        }
        catch
        {
            transaction.Dispose();
            throw;
        }
    }

    public string VersionTableSql
        => """
           if object_id(N'schema_version', N'U') is null
           create table schema_version (
               version      bigint        not null,
               object       nvarchar(200) not null,
               description  nvarchar(400) not null,
               checksum     nvarchar(64)  not null,
               applied_at   nvarchar(40)  not null,
               execution_ms bigint        not null,
               primary key (version, object)
           )
           """;

    public string RenameTableSql(string fromName, string toName)
        => $"exec sp_rename N'{Escape(fromName)}', N'{Escape(toName)}'";

    public string RenameColumnSql(string table, string fromName, string toName)
        => $"exec sp_rename N'{Escape(table)}.{Escape(fromName)}', N'{Escape(toName)}', 'COLUMN'";

    public string AddColumnSql(string table, string column, string storageType, bool nullable, string? defaultSql)
        => $"alter table {QuoteIdentifier(table)} add {QuoteIdentifier(column)} {storageType}"
            + (nullable ? string.Empty : " not null")
            + (defaultSql is null ? string.Empty : " default " + defaultSql);   // NOT NULL + default backfills existing rows

    /// <summary>
    /// A <c>not null … default</c> addition leaves a system-named default
    /// constraint behind, and <c>drop column</c> refuses while it exists (error
    /// 5074) — so the drop is one batch: find the constraint in
    /// <c>sys.default_constraints</c>, drop it when present, then drop the column
    /// (ADR-0043). SQLite and PostgreSQL drop a default with its column.
    /// </summary>
    public string DropColumnSql(string table, string column)
        => "declare @df sysname = (select dc.name from sys.default_constraints dc "
            + "join sys.columns c on c.object_id = dc.parent_object_id and c.column_id = dc.parent_column_id "
            + $"where dc.parent_object_id = object_id(N'{Escape(table)}') and c.name = N'{Escape(column)}'); "
            + $"if @df is not null exec(N'alter table {Escape(QuoteIdentifier(table))} drop constraint ' + quotename(@df)); "   // inside an N'…' literal: quotes doubled
            + $"alter table {QuoteIdentifier(table)} drop column {QuoteIdentifier(column)}";

    public string DropTableSql(string table)
        => "drop table " + QuoteIdentifier(table);

    public string DropIndexSql(string table, string index)
        => "drop index " + QuoteIdentifier(index) + " on " + QuoteIdentifier(table);

    public string CreateViewSql(EntityMap map)
        => CreateViewSql(map.RelationName!, map.DefiningSql!, map.Kind == RelationKind.MaterializedView);

    public string CreateViewSql(string view, string body, bool materialized)
        => "create or alter view " + QuoteIdentifier(view) + " as\n" + body;   // no materialized views (DDL-002 upstream)

    public string DropViewSql(string view, bool materialized)
        => "drop view if exists " + QuoteIdentifier(view);   // SQL Server 2016+

    /// <summary>A table from its snapshot (ADR-0043): one generated key renders inline as identity; any other key as the trailing constraint.</summary>
    public string CreateTableSql(TableSchema schema)
    {
        var builder = new StringBuilder("if object_id(N'").Append(Escape(schema.Name))
            .Append("', N'U') is null\ncreate table ").Append(QuoteIdentifier(schema.Name)).Append(" (");
        var generatedKey = schema.Columns.Count(c => c.Key && c.Generated) == 1 && schema.Columns.Count(c => c.Key) == 1;
        var first = true;
        foreach (var column in schema.Columns)
        {
            builder.Append(first ? "\n    " : ",\n    ").Append(QuoteIdentifier(column.Name)).Append(' ').Append(column.StorageType);
            first = false;
            if (column.Key && generatedKey)
            {
                builder.Append(" identity(1,1) primary key");
                continue;
            }

            if (!column.Nullable)
            {
                builder.Append(" not null");
            }
        }

        if (!generatedKey && schema.Columns.Any(c => c.Key))
        {
            builder.Append(",\n    primary key (")
                .Append(string.Join(", ", schema.Columns.Where(c => c.Key).Select(c => QuoteIdentifier(c.Name))))
                .Append(')');
        }

        return builder.Append("\n)").ToString();
    }

    public string CreateIndexSql(string table, TableSchema.Index index)
        => $"if not exists (select 1 from sys.indexes where name = N'{Escape(index.Name)}' "
            + $"and object_id = object_id(N'{Escape(table)}'))\n"
            + "create " + (index.Unique ? "unique " : string.Empty) + "index " + QuoteIdentifier(index.Name)
            + " on " + QuoteIdentifier(table) + " ("
            + string.Join(", ", index.Columns.Select(c => QuoteIdentifier(c.ColumnName) + (c.Descending ? " desc" : string.Empty)))
            + ")";

    // --- triggers (ADR-0042) ---------------------------------------------------------

    /// <summary>
    /// SQL Server triggers are statement-level: the body's rows are the
    /// <c>inserted</c>/<c>deleted</c> pseudo-tables. No BEFORE (<c>DDL-005</c>);
    /// INSTEAD OF on tables and views. <c>set nocount on</c> opens every body —
    /// the ruling that this is configured, never written by the author. An
    /// <c>UpdateOf</c> narrowing has no header form; it renders as an
    /// <c>if not (update(…)) return;</c> prologue. One statement: <c>CREATE
    /// TRIGGER</c> must open its batch, so the drop is a separate statement
    /// (<see cref="DropTriggerSql"/>) and <c>create or alter</c> is avoided (2016 SP1).
    /// </summary>
    public IReadOnlyList<string> CreateTriggerSql(string relation, bool onView, TriggerDefinition trigger)
    {
        if (trigger.Timing == TriggerTiming.Before)
        {
            throw new SimpleOrmException("DDL-005", trigger.Name, "SQL Server has no BEFORE triggers; declare After or InsteadOf");
        }

        var body = TriggerTokens.Render(trigger.Sql, segment => segment.Kind switch
        {
            "new" => "inserted",
            "old" => "deleted",
            _ => "todatetimeoffset(sysutcdatetime(), 0)",
        });
        // update(col) is false in a delete, so a declaration that also fires on delete
        // applies the narrowing only when rows were inserted or updated.
        var prologue = trigger.UpdateOf.Count > 0
            ? "    if " + (trigger.Events.HasFlag(TriggerEvent.Delete) ? "exists (select 1 from inserted) and " : string.Empty)
                + "not (" + string.Join(" or ", trigger.UpdateOf.Select(c => "update(" + QuoteIdentifier(c) + ")")) + ") return;\n"
            : string.Empty;
        return
        [
            "create trigger " + QuoteIdentifier(trigger.Name) + " on " + QuoteIdentifier(relation)
                + " " + (trigger.Timing == TriggerTiming.After ? "after" : "instead of") + " "
                + string.Join(", ", trigger.EventList().Select(EventWord))
                + " as\nbegin\n    set nocount on;\n" + prologue + body.Trim() + "\nend",
        ];
    }

    public IReadOnlyList<string> DropTriggerSql(string relation, string triggerName)
        => ["if object_id(N'" + Escape(triggerName) + "', N'TR') is not null drop trigger " + QuoteIdentifier(triggerName)];

    /// <summary>The stored text, exactly as submitted (<c>sys.sql_modules</c>).</summary>
    public string TriggerDefinitionSql
        => "select m.definition as [definition] from sys.sql_modules m "
            + "join sys.triggers t on t.object_id = m.object_id "
            + "where t.object_id = object_id(@relation)";

    private static string EventWord(TriggerEvent e) => e switch
    {
        TriggerEvent.Insert => "insert",
        TriggerEvent.Update => "update",
        _ => "delete",
    };

    public string CreateTableSql(EntityMap map)
    {
        var builder = new StringBuilder("if object_id(N'").Append(Escape(map.RelationName!))
            .Append("', N'U') is null\ncreate table ").Append(QuoteIdentifier(map.RelationName!)).Append(" (");

        var first = true;
        foreach (var property in map.Properties)
        {
            builder.Append(first ? "\n    " : ",\n    ").Append(QuoteIdentifier(property.ColumnName)).Append(' ');
            first = false;

            if (map.KeyStrategy == KeyStrategy.DatabaseGenerated && property.IsKey)
            {
                builder.Append(StorageType(map, property)).Append(" identity(1,1) primary key");
                continue;
            }

            builder.Append(StorageType(map, property));
            if (!property.IsNullable)
            {
                builder.Append(" not null");
            }

            if (map.KeyStrategy == KeyStrategy.ClientGuid && property.IsKey)
            {
                builder.Append(" primary key");
            }
        }

        if (map.KeyStrategy == KeyStrategy.Natural && map.KeyProperties.Count > 0)
        {
            builder.Append(",\n    primary key (")
                .Append(string.Join(", ", map.KeyProperties.Select(k => QuoteIdentifier(k.ColumnName))))
                .Append(')');
        }

        return builder.Append("\n)").ToString();
    }

    public IReadOnlyList<string> CreateIndexSql(EntityMap map)
        => map.Indexes
            .Select(index =>
                $"if not exists (select 1 from sys.indexes where name = N'{Escape(index.Name)}' "
                + $"and object_id = object_id(N'{Escape(map.RelationName!)}'))\n"
                + "create " + (index.Unique ? "unique " : string.Empty) + "index " + QuoteIdentifier(index.Name)
                + " on " + QuoteIdentifier(map.RelationName!) + " ("
                + string.Join(", ", index.Columns.Select(c => QuoteIdentifier(c.ColumnName) + (c.Descending ? " desc" : string.Empty)))
                + ")")
            .ToArray();

    public string InsertSql(EntityMap map)
    {
        var columns = map.Properties.Where(p => !p.IsGenerated).ToArray();
        var sql = "insert into " + QuoteIdentifier(map.RelationName!)
            + " (" + string.Join(", ", columns.Select(c => QuoteIdentifier(c.ColumnName))) + ")"
            + " values (" + string.Join(", ", columns.Select(c => "@" + c.ColumnName)) + ")";

        if (map.KeyStrategy == KeyStrategy.DatabaseGenerated)
        {
            // scope_identity(), not OUTPUT: an OUTPUT clause without INTO fails on
            // tables with triggers, and the generated-key strategy is identity-only
            // (ADR-0020) so scope_identity always covers it.
            sql += "; select cast(scope_identity() as bigint)";
        }

        return sql;
    }

    public string UpdateSql(EntityMap map)
        => RenderUpdate(map, map.Properties.Where(p => !p.IsKey && !p.IsVersion && !p.IsGenerated));

    public string UpdateOnlySql(EntityMap map, IReadOnlyList<PropertyMap> properties)
        => RenderUpdate(map, properties);

    private string RenderUpdate(EntityMap map, IEnumerable<PropertyMap> set)
    {
        var assignments = set
            .Select(p => QuoteIdentifier(p.ColumnName) + " = @" + p.ColumnName)
            .ToList();
        if (map.VersionProperty is { } version)
        {
            assignments.Add(QuoteIdentifier(version.ColumnName) + " = " + QuoteIdentifier(version.ColumnName) + " + 1");
        }

        return ReportingAffectedRows("update " + QuoteIdentifier(map.RelationName!)
            + " set " + string.Join(", ", assignments)
            + " where " + KeyPredicate(map)
            + (map.VersionProperty is { } v ? " and " + QuoteIdentifier(v.ColumnName) + " = @" + v.ColumnName : string.Empty));
    }

    public string DeleteSql(EntityMap map, bool checkVersion)
        => ReportingAffectedRows("delete from " + QuoteIdentifier(map.RelationName!)
            + " where " + KeyPredicate(map)
            + (checkVersion && map.VersionProperty is { } v ? " and " + QuoteIdentifier(v.ColumnName) + " = @" + v.ColumnName : string.Empty));

    /// <summary>
    /// The generated writes report their own row count (ADR-0040 add.3, owner
    /// ruling on item 3). A trigger without <c>SET NOCOUNT ON</c> makes the driver
    /// sum the trigger's rows into the statement's (3 reported for a 1-row update,
    /// spec/dialects.md Clarification 22), so the batch suppresses the counts,
    /// writes, and selects <c>@@rowcount</c> — the statement's own count, triggers
    /// or not — which the session reads as a scalar. <c>SET NOCOUNT</c> is
    /// session-scoped on SQL Server (it outlives the batch), so the batch restores
    /// it: a registry command on the same connection keeps the driver's count.
    /// Not <c>OUTPUT</c>: refused without <c>INTO</c> on a triggered table (error 334).
    /// </summary>
    public bool ReportsAffectedRowsBySelect => true;

    private static string ReportingAffectedRows(string statement)
        => "set nocount on; " + statement + "; select @@rowcount; set nocount off";

    private string KeyPredicate(EntityMap map)
        => string.Join(" and ", map.KeyProperties.Select(k => QuoteIdentifier(k.ColumnName) + " = @" + k.ColumnName));

    /// <summary>
    /// CLR → SQL Server storage type (ADR-0024, ADR-0043), spelled as
    /// <see cref="ColumnsInfoSql"/> reports it back. Dates are <c>datetimeoffset</c> —
    /// the storage that carries the §7.9 UTC marker. Strings and byte arrays are
    /// <c>(max)</c>, except a key column or a column of a declared index, which
    /// takes the indexable length <c>(450)</c> — <c>nvarchar(max)</c> cannot be an
    /// index key (error 1919) — and a declared <c>Length</c> facet wins over both.
    /// Decimals default to <c>decimal(38,9)</c>; a <c>Precision</c>/<c>Scale</c>
    /// facet renders <c>decimal(p,s)</c>.
    /// </summary>
    public string StorageType(EntityMap map, PropertyMap property)
        => StorageType(property, indexable: property.IsKey
            || map.Indexes.Any(i => i.Columns.Any(c => string.Equals(c.ColumnName, property.ColumnName, StringComparison.Ordinal))));

    private static string StorageType(PropertyMap property, bool indexable)
    {
        var type = Nullable.GetUnderlyingType(property.ClrType) ?? property.ClrType;
        if (type.IsEnum)
        {
            return property.EnumAsInt ? "int" : "nvarchar(100)";
        }

        if (type == typeof(int))
        {
            return "int";
        }

        if (type == typeof(long))
        {
            return "bigint";
        }

        if (type == typeof(short))
        {
            return "smallint";
        }

        if (type == typeof(bool))
        {
            return "bit";
        }

        if (type == typeof(decimal))
        {
            return property.Precision is { } precision ? $"decimal({precision},{property.Scale ?? 0})" : "decimal(38,9)";
        }

        if (type == typeof(double))
        {
            return "float";
        }

        if (type == typeof(float))
        {
            return "real";
        }

        if (type == typeof(byte[]))
        {
            return property.Length is { } bytesLength ? $"varbinary({bytesLength})" : indexable ? "varbinary(450)" : "varbinary(max)";
        }

        if (type == typeof(Guid))
        {
            return "uniqueidentifier";
        }

        if (type == typeof(DateTime) || type == typeof(DateTimeOffset))
        {
            return "datetimeoffset";
        }

        if (type.FullName == "System.DateOnly")
        {
            return "date";
        }

        if (type == typeof(TimeSpan) || type.FullName == "System.TimeOnly")
        {
            return "time";
        }

        // string, JSON, handler types.
        return property.Length is { } length ? $"nvarchar({length})" : indexable ? "nvarchar(450)" : "nvarchar(max)";
    }

    /// <summary>The declared type's base name: lowercased, any <c>(length)</c> stripped.</summary>
    private static string BaseTypeName(string declaredType)
    {
        var declared = declaredType.Trim();
        var paren = declared.IndexOf('(');
        if (paren >= 0)
        {
            declared = declared.Substring(0, paren);
        }

        return declared.Trim().ToLowerInvariant();
    }

    /// <summary>Escapes a name for embedding in an N'…' literal (sp_rename, object_id guards).</summary>
    private static string Escape(string name) => name.Replace("'", "''");
}
