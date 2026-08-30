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
                        plan.Additive.Add(dialect.AddColumnSql(
                            map.RelationName!, property.ColumnName, dialect.StorageType(property),
                            nullable: true, defaultSql: null));
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
                plan.Deletions.Add(dialect.DropColumnSql(map.RelationName!, extra));
            }

            // Indexes match structurally (ADR-0017 add.2): what matters is the indexed
            // columns (order, direction, uniqueness), never the name — indexes get
            // added directly to the database in urgencies, and one that already exists
            // under another name counts as implemented.
            var liveIndexes = await ReadIndexesAsync(db, map.RelationName!, ct).ConfigureAwait(false);
            var liveSignatures = new HashSet<string>(liveIndexes.Select(i => i.Signature), StringComparer.Ordinal);
            var modelSignatures = new HashSet<string>(
                map.Indexes.Select(MigrationGenerator.IndexSignature), StringComparer.Ordinal);
            var createIndexSql = dialect.CreateIndexSql(map);
            for (var i = 0; i < map.Indexes.Count; i++)
            {
                if (!liveSignatures.Contains(MigrationGenerator.IndexSignature(map.Indexes[i])))
                {
                    plan.Additive.Add(createIndexSql[i]);
                }
            }

            foreach (var index in liveIndexes.Where(i => !modelSignatures.Contains(i.Signature)))
            {
                plan.Deletions.Add(dialect.DropIndexSql(map.RelationName!, index.Name));
            }
        }

        return plan;
    }

    private sealed record LiveIndex(string Name, string Signature);

    private static async Task<IReadOnlyList<LiveIndex>> ReadIndexesAsync(Db db, string relation, CancellationToken ct)
    {
        var parts = new Dictionary<string, (bool Unique, List<(string Column, bool Descending)> Columns)>();
        var order = new List<string>();
        using var command = db.Connection.CreateCommand();
        command.CommandText = db.Options.Dialect.IndexesInfoSql;
        var parameter = command.CreateParameter();
        parameter.ParameterName = "@relation";
        parameter.Value = relation;
        command.Parameters.Add(parameter);

        DbDataReader reader = await command.ExecuteReaderAsync(ct).ConfigureAwait(false);
        try
        {
            while (await reader.ReadAsync(ct).ConfigureAwait(false))
            {
                var name = reader.GetString(0);
                if (!parts.TryGetValue(name, out var index))
                {
                    parts[name] = index = ((long)reader.GetValue(1) != 0, []);
                    order.Add(name);
                }

                index.Columns.Add((reader.GetString(3), (long)reader.GetValue(4) != 0));
            }
        }
        finally
        {
            reader.Dispose();
        }

        return order
            .Select(name => new LiveIndex(
                name, MigrationGenerator.Signature(parts[name].Unique, parts[name].Columns)))
            .ToArray();
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
