using System.Data.Common;

namespace SimpleOrm;

/// <summary>
/// Force-sync planning (ADR-0013 add.3 / ADR-0017): after migrations complete, the
/// real database is compared against the model. Additive fixes (missing tables,
/// missing nullable columns, missing indexes on new tables) are safe; deletions
/// (extra columns) are gated behind an explicit flag; type/nullability changes and
/// non-nullable additions are never auto-applied. Renames are never inferred.
/// </summary>
public static class SchemaSync
{
    public sealed class Plan
    {
        /// <summary>Safe, additive statements.</summary>
        public List<string> Additive { get; } = [];

        /// <summary>Destructive statements — executed only with allow-delete.</summary>
        public List<string> Deletions { get; } = [];

        /// <summary>Differences sync cannot express safely (<c>DDL-004</c>): write a migration.</summary>
        public List<string> Unsupported { get; } = [];

        public bool IsEmpty => Additive.Count == 0 && Deletions.Count == 0 && Unsupported.Count == 0;
    }

    public static async Task<Plan> PlanAsync(Db db, IEnumerable<Type> tableEntities, CancellationToken ct)
    {
        var plan = new Plan();
        var dialect = db.Options.Dialect;

        foreach (var type in tableEntities)
        {
            var map = db.Maps.Load(type);
            if (map.Kind != RelationKind.Table)
            {
                continue;
            }

            var live = await ReadColumnsAsync(db, map.RelationName!, ct).ConfigureAwait(false);
            if (live.Count == 0)
            {
                plan.Additive.Add(dialect.CreateTableSql(map));
                plan.Additive.AddRange(dialect.CreateIndexSql(map));
                continue;
            }

            foreach (var property in map.Properties)
            {
                if (!live.TryGetValue(property.ColumnName, out var column))
                {
                    if (property.IsNullable)
                    {
                        plan.Additive.Add(
                            $"alter table {map.RelationName} add column {property.ColumnName} {dialect.StorageType(property)}");
                    }
                    else
                    {
                        plan.Unsupported.Add(
                            $"{map.RelationName}.{property.ColumnName}: adding a non-nullable column needs a default/backfill — write a migration");
                    }

                    continue;
                }

                var expected = dialect.StorageType(property);
                if (!string.Equals(column.DeclaredType, expected, StringComparison.OrdinalIgnoreCase)
                    || column.NotNull == property.IsNullable)
                {
                    plan.Unsupported.Add(
                        $"{map.RelationName}.{property.ColumnName}: is {column.DeclaredType}{(column.NotNull ? " not null" : string.Empty)}, "
                        + $"model wants {expected}{(property.IsNullable ? string.Empty : " not null")} — write a migration");
                }
            }

            foreach (var extra in live.Keys.Where(c =>
                map.Properties.All(p => !string.Equals(p.ColumnName, c, StringComparison.OrdinalIgnoreCase))))
            {
                plan.Deletions.Add($"alter table {map.RelationName} drop column {extra}");
            }
        }

        return plan;
    }

    /// <summary>Executes plan statements in order (the CLI's force-sync apply step).</summary>
    public static async Task ApplyAsync(Db db, IEnumerable<string> statements, CancellationToken ct)
    {
        foreach (var sql in statements)
        {
            using var command = db.Connection.CreateCommand();
            command.CommandText = sql;
            await command.ExecuteNonQueryAsync(ct).ConfigureAwait(false);
        }
    }

    private sealed record LiveColumn(string DeclaredType, bool NotNull);

    private static async Task<Dictionary<string, LiveColumn>> ReadColumnsAsync(Db db, string relation, CancellationToken ct)
    {
        var columns = new Dictionary<string, LiveColumn>(StringComparer.OrdinalIgnoreCase);
        using var command = db.Connection.CreateCommand();
        command.CommandText = db.Options.Dialect.ColumnsInfoSql;
        var parameter = command.CreateParameter();
        parameter.ParameterName = "@relation";
        parameter.Value = relation;
        command.Parameters.Add(parameter);

        DbDataReader reader = await command.ExecuteReaderAsync(ct).ConfigureAwait(false);
        try
        {
            while (await reader.ReadAsync(ct).ConfigureAwait(false))
            {
                var notNull = (long)reader.GetValue(2) != 0 || (long)reader.GetValue(3) != 0;
                columns[reader.GetString(0)] = new LiveColumn(reader.GetString(1), notNull);
            }
        }
        finally
        {
            reader.Dispose();
        }

        return columns;
    }
}
