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

    public bool PagingRequiresOrderBy => true;   // OFFSET/FETCH is only legal after ORDER BY

    public bool SupportsRowValueIn => false;   // (a, b) in (select …) does not parse; the renderer rewrites as EXISTS

    /// <summary>Indexed views are a different mechanism (no <c>CREATE MATERIALIZED VIEW</c>, no explicit refresh).</summary>
    public bool SupportsMaterializedViews => false;

    public bool SupportsProcedures => true;

    public bool SupportsTransactionalDdl => true;

    public string ColumnsInfoSql
        => """
           select c.name,
                  case
                      when t.name in (N'decimal', N'numeric')
                          then t.name + N'(' + cast(c.precision as nvarchar(8)) + N',' + cast(c.scale as nvarchar(8)) + N')'
                      when t.name in (N'nvarchar', N'nchar')
                          then t.name + N'(' + case when c.max_length = -1 then N'max' else cast(c.max_length / 2 as nvarchar(8)) end + N')'
                      when t.name in (N'varchar', N'char', N'varbinary', N'binary')
                          then t.name + N'(' + case when c.max_length = -1 then N'max' else cast(c.max_length as nvarchar(8)) end + N')'
                      else t.name
                  end,
                  cast(case when c.is_nullable = 1 then 0 else 1 end as bigint),
                  cast(isnull(pk.key_ordinal, 0) as bigint)
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

    public string ViewDefinitionSql
        => "select m.definition from sys.sql_modules m "
            + "join sys.views v on v.object_id = m.object_id "
            + "where v.object_id = object_id(@relation)";

    public string IndexesInfoSql
        => "select i.name, cast(i.is_unique as bigint), cast(ic.key_ordinal - 1 as bigint), c.name, cast(ic.is_descending_key as bigint) "
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

    public string DropColumnSql(string table, string column)
        => $"alter table {QuoteIdentifier(table)} drop column {QuoteIdentifier(column)}";

    public string DropTableSql(string table)
        => "drop table " + QuoteIdentifier(table);

    public string DropIndexSql(string table, string index)
        => "drop index " + QuoteIdentifier(index) + " on " + QuoteIdentifier(table);

    public string CreateViewSql(EntityMap map)
        => "create or alter view " + QuoteIdentifier(map.RelationName!) + " as\n" + map.DefiningSql;

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
                builder.Append(StorageType(property)).Append(" identity(1,1) primary key");
                continue;
            }

            builder.Append(StorageType(property));
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
    {
        var assignments = map.Properties
            .Where(p => !p.IsKey && !p.IsVersion && !p.IsGenerated)
            .Select(p => QuoteIdentifier(p.ColumnName) + " = @" + p.ColumnName)
            .ToList();
        if (map.VersionProperty is { } version)
        {
            assignments.Add(QuoteIdentifier(version.ColumnName) + " = " + QuoteIdentifier(version.ColumnName) + " + 1");
        }

        return "update " + QuoteIdentifier(map.RelationName!)
            + " set " + string.Join(", ", assignments)
            + " where " + KeyPredicate(map)
            + (map.VersionProperty is { } v ? " and " + QuoteIdentifier(v.ColumnName) + " = @" + v.ColumnName : string.Empty);
    }

    public string DeleteSql(EntityMap map, bool checkVersion)
        => "delete from " + QuoteIdentifier(map.RelationName!)
            + " where " + KeyPredicate(map)
            + (checkVersion && map.VersionProperty is { } v ? " and " + QuoteIdentifier(v.ColumnName) + " = @" + v.ColumnName : string.Empty);

    private string KeyPredicate(EntityMap map)
        => string.Join(" and ", map.KeyProperties.Select(k => QuoteIdentifier(k.ColumnName) + " = @" + k.ColumnName));

    /// <summary>
    /// CLR → SQL Server storage type (ADR-0024). Dates are <c>datetimeoffset</c> —
    /// the storage that carries the §7.9 UTC marker; strings and byte arrays are
    /// <c>(max)</c> except keys, which need an indexable length; decimals default
    /// to <c>decimal(38, 9)</c> — declare the real precision in migrations.
    /// </summary>
    public string StorageType(PropertyMap property)
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
            return "decimal(38, 9)";
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
            return property.IsKey ? "varbinary(450)" : "varbinary(max)";
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
        return property.IsKey ? "nvarchar(450)" : "nvarchar(max)";
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
