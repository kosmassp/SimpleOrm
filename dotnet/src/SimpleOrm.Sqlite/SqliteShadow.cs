using System.Reflection;
using System.Text;
using Microsoft.Data.Sqlite;

namespace SimpleOrm.Sqlite;

/// <summary>
/// Rebuilds the versioned schema snapshots (<c>V000N.schema.json</c>) by replaying
/// the migration history into a throwaway shadow database and introspecting the
/// touched tables after each version (ADR-0017). The full rebuild replays from
/// empty; the range form (<c>--from V000N [--to V000M]</c>) **trusts version N as
/// correct** — the baseline state is reconstructed from the committed snapshots at
/// ≤ N without verifying anything below N, versions ≤ N are baselined, and only
/// (N, M] replays and re-snapshots. Tables only; views self-reflect.
/// </summary>
public static class SqliteShadow
{
    public sealed class Result
    {
        public List<string> WrittenFiles { get; } = [];

        public List<string> Notes { get; } = [];
    }

    public static async Task<Result> RebuildSnapshotsAsync(
        Assembly assembly,
        string? migrationsNamespace,
        string migrationsDir,
        long fromVersion = 0,
        long toVersion = long.MaxValue,
        CancellationToken ct = default)
    {
        var result = new Result();
        var versions = DiscoverVersions(assembly, migrationsNamespace);
        if (versions.Count == 0)
        {
            result.Notes.Add("no migration versions found");
            return result;
        }

        var entityByRelation = MapEntities(assembly);
        var shadowFile = Path.Combine(Path.GetTempPath(), "simpleorm-shadow-" + Guid.NewGuid().ToString("n") + ".db");
        var generatedAt = DateTimeOffset.UtcNow;
        try
        {
            var db = await Db.OpenAsync(
                $"Data Source={shadowFile}", new DbOptions { Dialect = new SqliteDialect() }, ct).ConfigureAwait(false);
            await using (db.ConfigureAwait(false))
            {
                using var probe = new SqliteConnection($"Data Source={shadowFile}");
                probe.Open();

                if (fromVersion > 0)
                {
                    // Trust: rebuild the state at N from the committed snapshots, verify nothing below.
                    RestoreBaseline(probe, migrationsDir, fromVersion, result);
                    await new MigrationRunner(db, versions).BaselineAsync(fromVersion, ct).ConfigureAwait(false);
                }

                foreach (var version in versions.Where(v => v.Version > fromVersion && v.Version <= toVersion))
                {
                    var runner = new MigrationRunner(db, versions.Where(v => v.Version <= version.Version));
                    var touched = (await runner.StatusAsync(ct).ConfigureAwait(false))
                        .Where(e => e.Version == version.Version)
                        .Select(e => e.ObjectName)
                        .Distinct()
                        .ToArray();
                    await runner.MigrateAsync(ct).ConfigureAwait(false);

                    foreach (var relation in touched)
                    {
                        if (!entityByRelation.TryGetValue(relation, out var entity))
                        {
                            result.Notes.Add(
                                $"V{version.Version:0000}: '{relation}' has no mapped entity; snapshot skipped");
                            continue;
                        }

                        string? content = null;
                        if (entity.Kind == RelationKind.Table)
                        {
                            var schema = Introspect(probe, relation);
                            if (schema is not null)
                            {
                                content = SchemaSnapshot.Export(schema, version.Version, generatedAt);
                            }
                        }
                        else
                        {
                            // Views self-reflect: their history is the DDL, verbatim from sqlite_master.
                            var ddl = ViewDdl(probe, relation);
                            if (ddl is not null)
                            {
                                content = SchemaSnapshot.ExportDdl(
                                    relation, entity.KindToken, ddl, version.Version, generatedAt);
                            }
                        }

                        if (content is null)
                        {
                            continue;   // dropped at this version — its snapshot history ends here
                        }

                        var directory = Path.Combine(migrationsDir, entity.KindFolder, entity.TypeName);
                        Directory.CreateDirectory(directory);
                        var file = Path.Combine(directory, $"V{version.Version:0000}.schema.json");
                        File.WriteAllText(file, content + "\n");
                        result.WrittenFiles.Add(file);
                    }
                }
            }
        }
        finally
        {
            SqliteConnection.ClearAllPools();
            TryDelete(shadowFile);
            TryDelete(shadowFile + "-wal");
            TryDelete(shadowFile + "-shm");
        }

        return result;
    }

    // --- trusted baseline (--from) --------------------------------------------------

    private static void RestoreBaseline(SqliteConnection connection, string migrationsDir, long fromVersion, Result result)
    {
        var tableRoot = Path.Combine(migrationsDir, "Table");
        if (Directory.Exists(tableRoot))
        {
            foreach (var objectDir in Directory.GetDirectories(tableRoot))
            {
                var latest = Directory.GetFiles(objectDir, "V*.schema.json")
                    .Select(file => (File: file, Parsed: SchemaSnapshot.Parse(File.ReadAllText(file))))
                    .Where(s => s.Parsed.AsOfVersion <= fromVersion)
                    .OrderByDescending(s => s.Parsed.AsOfVersion)
                    .FirstOrDefault();
                if (latest.File is null)
                {
                    continue;   // table born after the trusted version
                }

                Execute(connection, CreateTableSql(latest.Parsed.Schema));
                foreach (var indexSql in CreateIndexSql(latest.Parsed.Schema))
                {
                    Execute(connection, indexSql);
                }

                result.Notes.Add(
                    $"baseline: {latest.Parsed.Schema.Name} restored from V{latest.Parsed.AsOfVersion:0000} snapshot (trusted)");
            }
        }
        else
        {
            result.Notes.Add($"--from V{fromVersion:0000}: no committed snapshots under {tableRoot}; starting empty");
        }

        // Views restore after tables — their DDL snapshots are executable as stored.
        foreach (var kindFolder in new[] { "View", "MaterializedView" })
        {
            var kindRoot = Path.Combine(migrationsDir, kindFolder);
            if (!Directory.Exists(kindRoot))
            {
                continue;
            }

            foreach (var objectDir in Directory.GetDirectories(kindRoot))
            {
                var latest = Directory.GetFiles(objectDir, "V*.schema.json")
                    .Select(file => SchemaSnapshot.ParseDdl(File.ReadAllText(file)))
                    .Where(s => s.AsOfVersion <= fromVersion)
                    .OrderByDescending(s => s.AsOfVersion)
                    .FirstOrDefault();
                if (latest.Ddl is null)
                {
                    continue;
                }

                Execute(connection, latest.Ddl);
                result.Notes.Add(
                    $"baseline: {latest.Object} restored from V{latest.AsOfVersion:0000} snapshot (trusted)");
            }
        }
    }

    /// <summary>The stored create statement of a view, normalized — null when absent (dropped).</summary>
    private static string? ViewDdl(SqliteConnection connection, string relation)
    {
        using var command = connection.CreateCommand();
        command.CommandText = "select sql from sqlite_master where type = 'view' and name = @name";
        command.Parameters.AddWithValue("@name", relation);
        return command.ExecuteScalar() is string sql ? SchemaSnapshot.NormalizeDdl(sql) : null;
    }

    private static string CreateTableSql(TableSchema schema)
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

    private static IEnumerable<string> CreateIndexSql(TableSchema schema)
        => schema.Indexes.Select(index =>
            "create " + (index.Unique ? "unique " : string.Empty) + "index if not exists " + index.Name
            + " on " + schema.Name + " ("
            + string.Join(", ", index.Columns.Select(p => p.ColumnName + (p.Descending ? " desc" : string.Empty)))
            + ")");

    // --- introspection --------------------------------------------------------------

    /// <summary>The live shape of a table, or null when the relation is not a table (view, or dropped).</summary>
    private static TableSchema? Introspect(SqliteConnection connection, string relation)
    {
        if (!Scalar(connection, "select count(*) from sqlite_master where type = 'table' and name = @name", relation))
        {
            return null;
        }

        var columns = new List<TableSchema.Column>();
        var keyCount = 0;
        using (var command = connection.CreateCommand())
        {
            command.CommandText = "select name, type, \"notnull\", pk from pragma_table_info(@name)";
            command.Parameters.AddWithValue("@name", relation);
            using var reader = command.ExecuteReader();
            var raw = new List<(string Name, string Type, bool NotNull, long Pk)>();
            while (reader.Read())
            {
                raw.Add((reader.GetString(0), reader.GetString(1), reader.GetInt64(2) != 0, reader.GetInt64(3)));
            }

            keyCount = raw.Count(c => c.Pk > 0);
            foreach (var c in raw)
            {
                var isKey = c.Pk > 0;
                // The rowid alias: a lone INTEGER PRIMARY KEY column is database-generated.
                var generated = isKey && keyCount == 1
                    && string.Equals(c.Type, "INTEGER", StringComparison.OrdinalIgnoreCase);
                columns.Add(new TableSchema.Column(c.Name, c.Type, nullable: !c.NotNull && !isKey, isKey, generated));
            }
        }

        var indexes = new List<TableSchema.Index>();
        using (var listCommand = connection.CreateCommand())
        {
            listCommand.CommandText = "select name, \"unique\" from pragma_index_list(@name) where origin = 'c'";
            listCommand.Parameters.AddWithValue("@name", relation);
            var found = new List<(string Name, bool Unique)>();
            using (var reader = listCommand.ExecuteReader())
            {
                while (reader.Read())
                {
                    found.Add((reader.GetString(0), reader.GetInt64(1) != 0));
                }
            }

            foreach (var (name, unique) in found)
            {
                using var partsCommand = connection.CreateCommand();
                partsCommand.CommandText =
                    "select name, \"desc\" from pragma_index_xinfo(@name) where key = 1 order by seqno";
                partsCommand.Parameters.AddWithValue("@name", name);
                var parts = new List<TableSchema.Index.Part>();
                using var partsReader = partsCommand.ExecuteReader();
                while (partsReader.Read())
                {
                    parts.Add(new TableSchema.Index.Part(partsReader.GetString(0), partsReader.GetInt64(1) != 0));
                }

                indexes.Add(new TableSchema.Index(name, parts, unique));
            }
        }

        return new TableSchema(relation, columns, indexes);
    }

    // --- helpers --------------------------------------------------------------------

    private static IReadOnlyList<MigrationVersion> DiscoverVersions(Assembly assembly, string? migrationsNamespace)
        => assembly.GetTypes()
            .Where(t => !t.IsAbstract
                && typeof(MigrationVersion).IsAssignableFrom(t)
                && t.GetConstructor(Type.EmptyTypes) is not null
                && (migrationsNamespace is null
                    || (t.Namespace ?? string.Empty).StartsWith(migrationsNamespace, StringComparison.Ordinal)))
            .Select(t => (MigrationVersion)Activator.CreateInstance(t)!)
            .OrderBy(v => v.Version)
            .ToArray();

    private sealed class MappedEntity(string typeName, RelationKind kind)
    {
        public string TypeName { get; } = typeName;

        public RelationKind Kind { get; } = kind;

        public string KindFolder => Kind switch
        {
            RelationKind.Table => "Table",
            RelationKind.View => "View",
            RelationKind.MaterializedView => "MaterializedView",
            _ => "Procedure",
        };

        public string KindToken => Kind switch
        {
            RelationKind.View => "view",
            RelationKind.MaterializedView => "materialized_view",
            _ => "procedure",
        };
    }

    private static Dictionary<string, MappedEntity> MapEntities(Assembly assembly)
    {
        var loader = new EntityMapLoader();
        var byRelation = new Dictionary<string, MappedEntity>(StringComparer.OrdinalIgnoreCase);
        foreach (var type in assembly.GetExportedTypes()
            .Where(t => t is { IsClass: true, IsAbstract: false } && EntityMapLoader.HasMappingAttributes(t)))
        {
            var map = loader.Load(type);
            if (map.RelationName is not null && map.Kind != RelationKind.Statement)
            {
                byRelation[map.RelationName] = new MappedEntity(type.Name, map.Kind);
            }
        }

        return byRelation;
    }

    private static void Execute(SqliteConnection connection, string sql)
    {
        using var command = connection.CreateCommand();
        command.CommandText = sql;
        command.ExecuteNonQuery();
    }

    private static bool Scalar(SqliteConnection connection, string sql, string name)
    {
        using var command = connection.CreateCommand();
        command.CommandText = sql;
        command.Parameters.AddWithValue("@name", name);
        return (long)command.ExecuteScalar()! > 0;
    }

    private static void TryDelete(string path)
    {
        try
        {
            if (File.Exists(path))
            {
                File.Delete(path);
            }
        }
        catch (IOException)
        {
            // A straggling handle on Windows; the temp file is harmless.
        }
    }
}
