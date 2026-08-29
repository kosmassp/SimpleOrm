using System.Data.Common;
using System.Text;
using Microsoft.Data.Sqlite;

namespace SimpleOrm.Sqlite;

/// <summary>SQLite implementation of <see cref="IDialect"/> backed by Microsoft.Data.Sqlite.</summary>
public sealed class SqliteDialect : IDialect
{
    public DbConnection CreateConnection(string connectionString)
        => new SqliteConnection(connectionString);

    public string LimitOffsetClause(string? limitParameter, string? offsetParameter) => (limitParameter, offsetParameter) switch
    {
        (not null, not null) => $"limit {limitParameter} offset {offsetParameter}",
        (not null, null) => $"limit {limitParameter}",
        (null, not null) => $"limit -1 offset {offsetParameter}",   // SQLite requires LIMIT before OFFSET
        _ => string.Empty,
    };

    public bool SupportsMaterializedViews => false;

    public bool SupportsProcedures => false;

    public bool SupportsTransactionalDdl => true;

    public string ColumnsInfoSql
        => "select name, type, \"notnull\", pk from pragma_table_info(@relation)";

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

    public string CreateViewSql(EntityMap map)
        => "create view if not exists " + map.RelationName + " as\n" + map.DefiningSql;

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
    {
        var assignments = map.Properties
            .Where(p => !p.IsKey && !p.IsVersion)
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

    private static string KeyPredicate(EntityMap map)
        => string.Join(" and ", map.KeyProperties.Select(k => k.ColumnName + " = @" + k.ColumnName));

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
