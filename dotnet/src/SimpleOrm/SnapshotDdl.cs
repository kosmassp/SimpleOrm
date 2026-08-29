using System.Text;

namespace SimpleOrm;

/// <summary>
/// Renders DDL straight from a snapshot — what makes the recorded history
/// executable: derived rollbacks restore columns and indexes from it (ADR-0018),
/// and the shadow replayer's trusted <c>--from</c> baseline rebuilds whole tables
/// from it (ADR-0017).
/// </summary>
public static class SnapshotDdl
{
    public static string CreateTableSql(TableSchema schema)
    {
        var builder = new StringBuilder("create table if not exists ").Append(schema.Name).Append(" (");
        var first = true;
        foreach (var column in schema.Columns)
        {
            builder.Append(first ? "\n    " : ",\n    ").Append(column.Name).Append(' ');
            first = false;
            if (column.Key && column.Generated)
            {
                builder.Append("INTEGER PRIMARY KEY");   // the rowid alias spelling
                continue;
            }

            builder.Append(column.StorageType);
            if (!column.Nullable)
            {
                builder.Append(" NOT NULL");
            }
        }

        var plainKeys = schema.Columns.Where(c => c.Key && !c.Generated).ToArray();
        if (plainKeys.Length > 0 && !schema.Columns.Any(c => c.Key && c.Generated))
        {
            builder.Append(",\n    primary key (").Append(string.Join(", ", plainKeys.Select(k => k.Name))).Append(')');
        }

        return builder.Append("\n) STRICT").ToString();
    }

    public static string CreateIndexSql(string objectName, TableSchema.Index index)
        => "create " + (index.Unique ? "unique " : string.Empty) + "index if not exists " + index.Name
            + " on " + objectName + " ("
            + string.Join(", ", index.Columns.Select(p => p.ColumnName + (p.Descending ? " desc" : string.Empty)))
            + ")";

    public static IEnumerable<string> CreateIndexSql(TableSchema schema)
        => schema.Indexes.Select(index => CreateIndexSql(schema.Name, index));
}
