using System.Data.Common;
using System.Text;
using System.Text.RegularExpressions;
using Npgsql;

namespace SimpleOrm.Postgres;

/// <summary>
/// PostgreSQL implementation of <see cref="IDialect"/> backed by Npgsql
/// (ADR-0025, the second Level 4 dialect — the one several Level 1 seams were
/// designed for). The promises come due here: <c>SupportsArrayParameters</c>
/// is realized (<c>= any(@ids)</c>, one typed-array parameter, SQL untouched),
/// the migration run lock evolves to an advisory lock
/// (<c>pg_advisory_xact_lock</c> inside one transaction — Postgres DDL is
/// transactional, so the SQLite atomicity carries over without per-migration
/// transactions), materialized views come alive
/// (<c>SupportsMaterializedViews</c>), and <c>RETURNING</c> works exactly as on
/// SQLite. Identifiers are always double-quoted: names are snake_case lowercase
/// by convention, so quoting matches the catalog's fold while staying
/// reserved-word-proof (<c>user</c>, <c>order</c>, <c>desc</c>). Temporals bind
/// natively (<see cref="BindsTemporalsNatively"/>): Postgres refuses
/// <c>timestamptz >= text</c>, and <c>timestamptz</c> is the marker-carrying
/// storage — it reads back Kind=Utc, while a <c>timestamp without time zone</c>
/// reads back Kind=Unspecified and refuses with <c>VAL-020</c> (an
/// ITypeHandler declaring the column's kind is the legacy escape, as on SQL
/// Server).
/// </summary>
public sealed class PostgresDialect : IDialect
{
    public DbConnection CreateConnection(string connectionString)
        => new NpgsqlConnection(connectionString);

    /// <summary>Always double-quoted (ADR-0025): lowercase names match the catalog's fold, reserved words stop mattering.</summary>
    public string QuoteIdentifier(string identifier)
        => "\"" + identifier.Replace("\"", "\"\"") + "\"";

    public string LimitOffsetClause(string? limitParameter, string? offsetParameter) => (limitParameter, offsetParameter) switch
    {
        (not null, not null) => $"limit {limitParameter} offset {offsetParameter}",
        (not null, null) => $"limit {limitParameter}",
        (null, not null) => $"offset {offsetParameter}",   // no SQLite-style limit -1 workaround needed
        _ => string.Empty,
    };

    public string SelectSql(SelectAst select, BindCriteriaParameter bindParameter)
        => AnsiSelectRenderer.SelectSql(this, select, bindParameter);

    public bool SupportsArrayParameters => true;   // §7.12 realized: one typed-array parameter, registry SQL says = any(@ids)

    public bool BindsTemporalsNatively => true;   // timestamptz >= text refuses; the provider gets UTC CLR values

    public bool PagingRequiresOrderBy => false;

    public bool SupportsRowValueIn => true;   // (a, b) in (select …) parses natively

    public bool SupportsMaterializedViews => true;

    public bool SupportsProcedures => true;

    public bool SupportsTransactionalDdl => true;

    public string ColumnsInfoSql
        => """
           select a.attname,
                  format_type(a.atttypid, a.atttypmod),
                  (case when a.attnotnull then 1 else 0 end)::bigint,
                  (case when exists (
                      select 1 from pg_index i
                      where i.indrelid = a.attrelid and i.indisprimary and a.attnum = any(i.indkey)
                  ) then 1 else 0 end)::bigint
           from pg_attribute a
           where a.attrelid = to_regclass(@relation) and a.attnum > 0 and not a.attisdropped
           order by a.attnum
           """;

    public string ViewDefinitionSql
        => "select pg_get_viewdef(c.oid, true) from pg_class c "
            + "where c.oid = to_regclass(@relation) and c.relkind in ('v', 'm')";

    public string IndexesInfoSql
        => """
           select ic.relname,
                  (case when i.indisunique then 1 else 0 end)::bigint,
                  k.n::bigint,
                  a.attname,
                  (case when (i.indoption[k.n] & 1) <> 0 then 1 else 0 end)::bigint
           from pg_index i
           join pg_class ic on ic.oid = i.indexrelid
           cross join lateral generate_series(0, i.indnkeyatts - 1) as k(n)
           join pg_attribute a on a.attrelid = i.indrelid and a.attnum = i.indkey[k.n]
           where i.indrelid = to_regclass(@relation)
             and not i.indisprimary
             and not exists (select 1 from pg_constraint con where con.conindid = i.indexrelid)
           order by ic.relname, k.n
           """;

    public bool IsDeclaredTypeCompatible(string declaredType, Type clrType, bool enumAsInt)
    {
        var type = Nullable.GetUnderlyingType(clrType) ?? clrType;
        var declared = BaseTypeName(declaredType);
        if (declared.Length == 0)
        {
            return false;
        }

        if (type.IsEnum)
        {
            return enumAsInt
                ? declared is "integer" or "int4" or "bigint" or "int8" or "smallint" or "int2"
                : declared is "text" or "character varying" or "varchar" or "character" or "char";
        }

        return declared switch
        {
            "integer" or "int4" => type == typeof(int) || type == typeof(long) || type == typeof(short),
            "bigint" or "int8" => type == typeof(long),
            "smallint" or "int2" => type == typeof(short) || type == typeof(int) || type == typeof(long),
            "boolean" or "bool" => type == typeof(bool),
            "numeric" or "decimal" or "money" => type == typeof(decimal),
            "double precision" or "float8" => type == typeof(double),
            "real" or "float4" => type == typeof(float),
            "text" or "character varying" or "varchar" or "character" or "char" or "name" => type == typeof(string),
            "bytea" => type == typeof(byte[]),
            "uuid" => type == typeof(Guid),
            // Markerless storage is still *type*-compatible with DateTime; the
            // VAL-020 kind rule applies per value at read time (as on SQL Server).
            "timestamp with time zone" or "timestamptz" => type == typeof(DateTime) || type == typeof(DateTimeOffset),
            "timestamp without time zone" or "timestamp" => type == typeof(DateTime),
            "date" => type == typeof(DateTime) || type.FullName == "System.DateOnly",
            "time without time zone" or "time" => type == typeof(TimeSpan) || type.FullName == "System.TimeOnly",
            _ => false,
        };
    }

    /// <summary>
    /// The run lock (§7.23, evolved as anticipated): one transaction for the whole
    /// run holding an exclusive advisory lock scoped to it — released on
    /// commit/rollback, never leaked. DDL is transactional on Postgres, so a
    /// failed run still applies nothing; the anticipated per-migration
    /// transactions turned out unnecessary (ADR-0025).
    /// </summary>
    public DbTransaction BeginMigrationRunLock(DbConnection connection)
    {
        var transaction = connection.BeginTransaction();
        try
        {
            using var command = connection.CreateCommand();
            command.Transaction = transaction;
            // The key pair is arbitrary but fixed: ASCII 'Simp', 'leOr'.
            command.CommandText =
                "set local lock_timeout = '60s'; "
                + "select pg_advisory_xact_lock(1399418224, 1818578802)";
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
           create table if not exists schema_version (
               version      bigint not null,
               object       text not null,
               description  text not null,
               checksum     text not null,
               applied_at   text not null,
               execution_ms bigint not null,
               primary key (version, object)
           )
           """;

    public string RenameTableSql(string fromName, string toName)
        => $"alter table {QuoteIdentifier(fromName)} rename to {QuoteIdentifier(toName)}";

    public string RenameColumnSql(string table, string fromName, string toName)
        => $"alter table {QuoteIdentifier(table)} rename column {QuoteIdentifier(fromName)} to {QuoteIdentifier(toName)}";

    public string AddColumnSql(string table, string column, string storageType, bool nullable, string? defaultSql)
        => $"alter table {QuoteIdentifier(table)} add column {QuoteIdentifier(column)} {storageType}"
            + (nullable ? string.Empty : " not null")
            + (defaultSql is null ? string.Empty : " default " + defaultSql);

    public string DropColumnSql(string table, string column)
        => $"alter table {QuoteIdentifier(table)} drop column {QuoteIdentifier(column)}";

    public string DropTableSql(string table)
        => "drop table " + QuoteIdentifier(table);

    public string DropIndexSql(string table, string index)
        => "drop index " + QuoteIdentifier(index);   // index names are schema-scoped, not table-scoped

    public string CreateViewSql(EntityMap map)
        => map.Kind == RelationKind.MaterializedView
            ? "create materialized view if not exists " + QuoteIdentifier(map.RelationName!) + " as\n" + map.DefiningSql
            : "create or replace view " + QuoteIdentifier(map.RelationName!) + " as\n" + map.DefiningSql;

    public string CreateTableSql(EntityMap map)
    {
        var builder = new StringBuilder("create table if not exists ")
            .Append(QuoteIdentifier(map.RelationName!)).Append(" (");

        var first = true;
        foreach (var property in map.Properties)
        {
            builder.Append(first ? "\n    " : ",\n    ").Append(QuoteIdentifier(property.ColumnName)).Append(' ');
            first = false;

            if (map.KeyStrategy == KeyStrategy.DatabaseGenerated && property.IsKey)
            {
                // "by default" (not ALWAYS) so fixtures may insert explicit keys.
                builder.Append(StorageType(property)).Append(" generated by default as identity primary key");
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
                "create " + (index.Unique ? "unique " : string.Empty) + "index if not exists "
                + QuoteIdentifier(index.Name)
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
            sql += " returning " + QuoteIdentifier(map.KeyProperties[0].ColumnName);
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
    /// CLR → PostgreSQL storage type (ADR-0025), spelled the way
    /// <c>format_type</c> reports it so snapshots/diff/sync compare cleanly.
    /// <c>numeric</c> is unconstrained (arbitrary precision — no SQL Server-style
    /// default needed); keys need no length hack (<c>text</c> is indexable);
    /// temporals are <c>timestamp with time zone</c>, the marker-carrying storage.
    /// </summary>
    public string StorageType(PropertyMap property)
    {
        var type = Nullable.GetUnderlyingType(property.ClrType) ?? property.ClrType;
        if (type.IsEnum)
        {
            return property.EnumAsInt ? "integer" : "text";
        }

        if (type == typeof(int))
        {
            return "integer";
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
            return "boolean";
        }

        if (type == typeof(decimal))
        {
            return "numeric";
        }

        if (type == typeof(double))
        {
            return "double precision";
        }

        if (type == typeof(float))
        {
            return "real";
        }

        if (type == typeof(byte[]))
        {
            return "bytea";
        }

        if (type == typeof(Guid))
        {
            return "uuid";
        }

        if (type == typeof(DateTime) || type == typeof(DateTimeOffset))
        {
            return "timestamp with time zone";
        }

        if (type.FullName == "System.DateOnly")
        {
            return "date";
        }

        if (type == typeof(TimeSpan) || type.FullName == "System.TimeOnly")
        {
            return "time without time zone";
        }

        // string, JSON, handler types.
        return "text";
    }

    /// <summary>
    /// The declared type's base name: lowercased, any parenthesized length or
    /// precision removed — including mid-name, as in
    /// <c>timestamp(3) with time zone</c>.
    /// </summary>
    private static string BaseTypeName(string declaredType)
    {
        var declared = Regex.Replace(declaredType, @"\([^)]*\)", string.Empty);
        return Regex.Replace(declared, @"\s+", " ").Trim().ToLowerInvariant();
    }
}
