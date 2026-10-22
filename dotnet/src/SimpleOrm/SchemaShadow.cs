using System.Data.Common;
using System.Reflection;

namespace SimpleOrm;

/// <summary>
/// Rebuilds the versioned schema snapshots (<c>V000N.schema.json</c>) by replaying
/// the migration history into a throwaway database and recording what each
/// version leaves behind (ADR-0017, every dialect since ADR-0043). Tables are
/// introspected after each version through the dialect's <c>ColumnsInfoSql</c>
/// and <c>IndexesInfoSql</c>; views and triggers are taken from the replayed step
/// — its create DDL and its declaration — because an engine's catalog may not
/// hand them back as declared (PostgreSQL reformats a view, SQL Server's
/// <c>inserted</c> has lost a token's column list); where the catalog is
/// lossless the shadow reads it too and notes a disagreement. The full rebuild
/// replays from empty; the range form (<c>--from V000N [--to V000M]</c>)
/// **trusts version N as correct** — the baseline is rendered from the committed
/// snapshots at ≤ N through the dialect, versions ≤ N are baselined, and only
/// (N, M] replays and re-snapshots. The database comes from the dialect's
/// <see cref="IShadowHost"/>; a snapshot stamped for another dialect is <c>MIG-014</c>.
/// </summary>
public static class SchemaShadow
{
    public sealed class Result
    {
        public List<string> WrittenFiles { get; } = [];

        public List<string> Notes { get; } = [];
    }

    public static Task<Result> RebuildSnapshotsAsync(
        Assembly assembly,
        string? migrationsNamespace,
        string migrationsDir,
        IDialect dialect,
        IShadowHost host,
        string? adminConnectionString,
        long fromVersion = 0,
        long toVersion = long.MaxValue,
        CancellationToken ct = default)
        => RebuildSnapshotsAsync(
            DiscoverVersions(assembly, migrationsNamespace),
            EntityMapLoader.ScannedEntityTypes(assembly.GetExportedTypes()),   // ADR-0041: a base class refuses the scan
            migrationsDir, dialect, host, adminConnectionString, fromVersion, toVersion, ct);

    /// <summary>As above, from explicit versions and entity types — the form the tests and an application without an assembly scan use.</summary>
    public static async Task<Result> RebuildSnapshotsAsync(
        IReadOnlyList<MigrationVersion> versions,
        IEnumerable<Type> entityTypes,
        string migrationsDir,
        IDialect dialect,
        IShadowHost host,
        string? adminConnectionString,
        long fromVersion = 0,
        long toVersion = long.MaxValue,
        CancellationToken ct = default)
    {
        var result = new Result();
        versions = versions.OrderBy(v => v.Version).ToArray();
        if (versions.Count == 0)
        {
            result.Notes.Add("no migration versions found");
            return result;
        }

        var entityByRelation = MapEntities(entityTypes);
        var generatedAt = DateTimeOffset.UtcNow;
        var stamp = dialect.Name;
        var connectionString = await host.ProvisionAsync(adminConnectionString, ct).ConfigureAwait(false);
        try
        {
            var db = await Db.OpenAsync(connectionString, new DbOptions { Dialect = dialect }, ct).ConfigureAwait(false);
            await using (db.ConfigureAwait(false))
            {
                if (fromVersion > 0)
                {
                    // Trust: rebuild the state at N from the committed snapshots, verify nothing below.
                    await RestoreBaselineAsync(db, migrationsDir, fromVersion, result, ct).ConfigureAwait(false);
                    await new MigrationRunner(db, versions) { ReplayDrafts = true }.BaselineAsync(fromVersion, ct).ConfigureAwait(false);
                }

                foreach (var version in versions.Where(v => v.Version > fromVersion && v.Version <= toVersion))
                {
                    var runner = new MigrationRunner(db, versions.Where(v => v.Version <= version.Version)) { ReplayDrafts = true };   // a draft replays here (Level 3 M5)
                    var shapes = runner.ReplayShapes().Where(s => s.Version == version.Version).ToArray();
                    await runner.MigrateAsync(ct).ConfigureAwait(false);

                    foreach (var relation in shapes.Select(s => s.ObjectName).Distinct())
                    {
                        if (!entityByRelation.TryGetValue(relation, out var entity))
                        {
                            result.Notes.Add(
                                $"V{version.Version:0000}: '{relation}' has no mapped entity; snapshot skipped");
                            continue;
                        }

                        var shape = shapes.Last(s => s.ObjectName == relation);
                        string? content;
                        if (entity.IsTrigger)
                        {
                            content = TriggerContent(db, host, shape, relation, version.Version, generatedAt, stamp, result);
                        }
                        else if (entity.Kind == RelationKind.Table)
                        {
                            var schema = await IntrospectAsync(db, relation, ct).ConfigureAwait(false);
                            content = schema is null ? null : SchemaSnapshot.Export(schema, version.Version, generatedAt, stamp);
                        }
                        else
                        {
                            content = await ViewContentAsync(db, shape, relation, entity.KindToken, version.Version, generatedAt, stamp, result, ct).ConfigureAwait(false);
                        }

                        if (content is null)
                        {
                            continue;   // dropped at this version (or unreadable, noted) — its snapshot history ends here
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
            try
            {
                await host.DropAsync(ct).ConfigureAwait(false);
            }
            catch (Exception exception) when (exception is DbException or IOException)
            {
                // Best effort (Clarification): the replay's result stands; the straggler is reported, never thrown over it.
                result.Notes.Add("the shadow database could not be dropped: " + exception.Message);
            }
        }

        return result;
    }

    // --- views and triggers: the step is the history --------------------------------

    /// <summary>
    /// A trigger's snapshot from the step's declaration; a step that drops it ends
    /// the history. Where the dialect reads triggers back, the stored text is the
    /// self-check. A raw-SQL step says nothing, so the catalog is the only source
    /// there — and a dialect that cannot read back skips with a note.
    /// </summary>
    private static string? TriggerContent(
        Db db, IShadowHost host, MigrationRunner.ReplayShape shape, string relation,
        long version, DateTimeOffset generatedAt, string stamp, Result result)
    {
        TriggerSnapshot? declared;
        if (shape.IsTriggerStep)
        {
            declared = shape.DeclaredTrigger;
            if (declared is not null && host.TriggerReadBack is { } check)
            {
                var stored = check.Read(db.Connection, relation, out var note);
                if (stored is null)
                {
                    result.Notes.Add($"V{version:0000}: trigger {relation}: {note ?? "the stored trigger could not be read back"}; the declaration was snapshotted from the step");
                }
                else if (!stored.Definition.SameAs(declared.Definition) || !string.Equals(stored.Relation, declared.Relation, StringComparison.OrdinalIgnoreCase))
                {
                    result.Notes.Add($"V{version:0000}: trigger {relation}: the stored trigger reads back differently from the step's declaration; the declaration was snapshotted");
                }
            }
        }
        else if (host.TriggerReadBack is { } readBack)
        {
            declared = readBack.Read(db.Connection, relation, out var note);
            if (declared is null && note is not null)
            {
                result.Notes.Add($"V{version:0000}: {note}");   // hand-written: skipped with a note
            }
        }
        else
        {
            result.Notes.Add($"V{version:0000}: trigger {relation}: the step carries no declaration and {db.Options.Dialect.Name} cannot read a trigger back; snapshot skipped");
            return null;
        }

        return declared is null ? null : SchemaSnapshot.ExportTrigger(declared, version, generatedAt, stamp);
    }

    /// <summary>
    /// A view's snapshot from the step's last create DDL (normalized, neutral); a
    /// later drop ends the history. A step with no create at all (a hook-only
    /// step) falls back to the catalog where the dialect stores the text as
    /// submitted, and notes the skip where it does not.
    /// </summary>
    private static async Task<string?> ViewContentAsync(
        Db db, MigrationRunner.ReplayShape shape, string relation, string kindToken,
        long version, DateTimeOffset generatedAt, string stamp, Result result, CancellationToken ct)
    {
        string? ddl = null;
        var found = false;
        foreach (var statement in shape.Up)
        {
            var normalized = SchemaSnapshot.NormalizeDdl(statement.Sql);
            if (SchemaSnapshot.TryParseViewDdl(normalized, out var name, out _, out _)
                && string.Equals(name, relation, StringComparison.OrdinalIgnoreCase))
            {
                ddl = normalized;
                found = true;
            }
            else if (SchemaSnapshot.IsDropView(normalized, relation))
            {
                ddl = null;
                found = true;
            }
        }

        var verbatim = db.Options.Dialect.ViewDefinitionIsVerbatim;
        var live = verbatim ? await ReadViewDefinitionAsync(db, relation, ct).ConfigureAwait(false) : null;
        if (!found)
        {
            if (!verbatim)
            {
                result.Notes.Add($"V{version:0000}: view {relation}: the step holds no create and {db.Options.Dialect.Name} does not store the definition as submitted; snapshot skipped");
                return null;
            }

            ddl = live is null ? null : SchemaSnapshot.NormalizeDdl(live);
        }
        else if (verbatim && ddl is not null && live is not null && SchemaSnapshot.NormalizeDdl(live) != ddl)
        {
            result.Notes.Add($"V{version:0000}: view {relation}: the stored definition differs from the step's; the step's was snapshotted");
        }

        return ddl is null ? null : SchemaSnapshot.ExportDdl(relation, kindToken, ddl, version, generatedAt, stamp);
    }

    private static async Task<string?> ReadViewDefinitionAsync(Db db, string relation, CancellationToken ct)
    {
        using var command = db.Connection.CreateCommand();
        command.CommandText = db.Options.Dialect.ViewDefinitionSql;
        AddParameter(command, relation);
        return await command.ExecuteScalarAsync(ct).ConfigureAwait(false) as string;
    }

    // --- trusted baseline (--from) --------------------------------------------------

    private static async Task RestoreBaselineAsync(Db db, string migrationsDir, long fromVersion, Result result, CancellationToken ct)
    {
        var dialect = db.Options.Dialect;
        var tableRoot = Path.Combine(migrationsDir, "Table");
        if (Directory.Exists(tableRoot))
        {
            foreach (var objectDir in Directory.GetDirectories(tableRoot))
            {
                var latest = Directory.GetFiles(objectDir, "V*.schema.json")
                    .Select(file => (File: file, Json: File.ReadAllText(file)))
                    .Select(file => (file.File, file.Json, Parsed: SchemaSnapshot.Parse(file.Json)))
                    .Where(s => s.Parsed.AsOfVersion <= fromVersion)
                    .OrderByDescending(s => s.Parsed.AsOfVersion)
                    .FirstOrDefault();
                if (latest.File is null)
                {
                    continue;   // table born after the trusted version
                }

                RequireStamp(latest.File, latest.Json, dialect);

                await ExecuteAsync(db, dialect.CreateTableSql(latest.Parsed.Schema), ct).ConfigureAwait(false);
                foreach (var index in latest.Parsed.Schema.Indexes)
                {
                    await ExecuteAsync(db, dialect.CreateIndexSql(latest.Parsed.Schema.Name, index), ct).ConfigureAwait(false);
                }

                result.Notes.Add(
                    $"baseline: {latest.Parsed.Schema.Name} restored from V{latest.Parsed.AsOfVersion:0000} snapshot (trusted)");
            }
        }
        else
        {
            result.Notes.Add($"--from V{fromVersion:0000}: no committed snapshots under {tableRoot}; starting empty");
        }

        // Views restore after tables — their neutral DDL renders through the dialect.
        foreach (var kindFolder in new[] { "View", "MaterializedView" })
        {
            var kindRoot = Path.Combine(migrationsDir, kindFolder);
            if (!Directory.Exists(kindRoot))
            {
                continue;
            }

            foreach (var objectDir in Directory.GetDirectories(kindRoot))
            {
                var latestFile = Directory.GetFiles(objectDir, "V*.schema.json")
                    .Select(file => (File: file, Json: File.ReadAllText(file)))
                    .Select(file => (file.File, file.Json, Parsed: SchemaSnapshot.ParseDdl(file.Json)))
                    .Where(s => s.Parsed.AsOfVersion <= fromVersion)
                    .OrderByDescending(s => s.Parsed.AsOfVersion)
                    .FirstOrDefault();
                if (latestFile.File is null)
                {
                    continue;
                }

                RequireStamp(latestFile.File, latestFile.Json, dialect);
                var latest = latestFile.Parsed;

                var sql = SchemaSnapshot.TryParseViewDdl(latest.Ddl, out var name, out var body, out var materialized)
                    ? dialect.CreateViewSql(name, body, materialized)
                    : latest.Ddl;
                await ExecuteAsync(db, sql, ct).ConfigureAwait(false);
                result.Notes.Add(
                    $"baseline: {latest.Object} restored from V{latest.AsOfVersion:0000} snapshot (trusted)");
            }
        }

        // Triggers restore last — their relations exist by now (ADR-0042).
        var triggerRoot = Path.Combine(migrationsDir, "Trigger");
        if (Directory.Exists(triggerRoot))
        {
            foreach (var objectDir in Directory.GetDirectories(triggerRoot))
            {
                var latestFile = Directory.GetFiles(objectDir, "V*.schema.json")
                    .Select(file => (File: file, Json: File.ReadAllText(file)))
                    .Select(file => (file.File, file.Json, Parsed: SchemaSnapshot.ParseTrigger(file.Json)))
                    .Where(s => s.Parsed.AsOfVersion <= fromVersion)
                    .OrderByDescending(s => s.Parsed.AsOfVersion)
                    .FirstOrDefault();
                if (latestFile.File is null)
                {
                    continue;
                }

                RequireStamp(latestFile.File, latestFile.Json, dialect);
                var latest = latestFile.Parsed;

                foreach (var sql in dialect.CreateTriggerSql(latest.Snapshot.Relation, latest.Snapshot.OnView, latest.Snapshot.Definition))
                {
                    await ExecuteAsync(db, sql, ct).ConfigureAwait(false);
                }

                result.Notes.Add(
                    $"baseline: {latest.Snapshot.Definition.Name} restored from V{latest.AsOfVersion:0000} snapshot (trusted)");
            }
        }
    }

    /// <summary>The consulted snapshot's stamp must be the dialect's (<c>MIG-014</c>) — checked on the baseline the shadow uses, not on every file in the folder.</summary>
    private static void RequireStamp(string file, string json, IDialect dialect)
        => SchemaSnapshot.RequireDialect(SchemaSnapshot.DialectOf(json), dialect, file);

    // --- introspection (dialect-neutral, through the seam's fixed shapes) -------------

    /// <summary>The live shape of a table through <c>ColumnsInfoSql</c>/<c>IndexesInfoSql</c>, or null when the relation is gone.</summary>
    public static async Task<TableSchema?> IntrospectAsync(Db db, string relation, CancellationToken ct)
    {
        var dialect = db.Options.Dialect;
        var columns = new List<TableSchema.Column>();
        using (var command = db.Connection.CreateCommand())
        {
            command.CommandText = dialect.ColumnsInfoSql;
            AddParameter(command, relation);
            using var reader = await command.ExecuteReaderAsync(ct).ConfigureAwait(false);
            while (await reader.ReadAsync(ct).ConfigureAwait(false))
            {
                var notNull = Convert.ToInt64(reader.GetValue(2), System.Globalization.CultureInfo.InvariantCulture) != 0;
                var isKey = Convert.ToInt64(reader.GetValue(3), System.Globalization.CultureInfo.InvariantCulture) != 0;
                var generated = Convert.ToInt64(reader.GetValue(4), System.Globalization.CultureInfo.InvariantCulture) != 0;
                columns.Add(new TableSchema.Column(reader.GetString(0), reader.GetString(1), nullable: !notNull && !isKey, isKey, generated));
            }
        }

        if (columns.Count == 0)
        {
            return null;
        }

        var indexes = new List<TableSchema.Index>();
        using (var command = db.Connection.CreateCommand())
        {
            command.CommandText = dialect.IndexesInfoSql;
            AddParameter(command, relation);
            using var reader = await command.ExecuteReaderAsync(ct).ConfigureAwait(false);
            var parts = new Dictionary<string, (bool Unique, List<TableSchema.Index.Part> Columns)>(StringComparer.Ordinal);
            var order = new List<string>();
            while (await reader.ReadAsync(ct).ConfigureAwait(false))
            {
                var name = reader.GetString(0);
                if (!parts.TryGetValue(name, out var index))
                {
                    parts[name] = index = (Convert.ToInt64(reader.GetValue(1), System.Globalization.CultureInfo.InvariantCulture) != 0, []);
                    order.Add(name);
                }

                index.Columns.Add(new TableSchema.Index.Part(
                    reader.GetString(3), Convert.ToInt64(reader.GetValue(4), System.Globalization.CultureInfo.InvariantCulture) != 0));
            }

            indexes.AddRange(order.Select(name => new TableSchema.Index(name, parts[name].Columns, parts[name].Unique)));
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

    private sealed class MappedEntity(string typeName, RelationKind kind, bool isTrigger = false)
    {
        public string TypeName { get; } = typeName;

        public RelationKind Kind { get; } = kind;

        /// <summary>A trigger object (ADR-0042): the type is the trigger class, the folder <c>Trigger/</c>.</summary>
        public bool IsTrigger { get; } = isTrigger;

        public string KindFolder => IsTrigger ? "Trigger" : Kind switch
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

    private static Dictionary<string, MappedEntity> MapEntities(IEnumerable<Type> entityTypes)
    {
        var loader = new EntityMapLoader();
        var byRelation = new Dictionary<string, MappedEntity>(StringComparer.OrdinalIgnoreCase);
        foreach (var type in entityTypes)
        {
            var map = loader.Load(type);
            if (map.RelationName is not null && map.Kind != RelationKind.Statement)
            {
                byRelation[map.RelationName] = new MappedEntity(type.Name, map.Kind);
            }

            foreach (var trigger in map.Triggers)
            {
                byRelation[trigger.Name] = new MappedEntity(trigger.TriggerType.Name, map.Kind, isTrigger: true);
            }
        }

        return byRelation;
    }

    private static async Task ExecuteAsync(Db db, string sql, CancellationToken ct)
    {
        using var command = db.Connection.CreateCommand();
        command.CommandText = sql;
        await command.ExecuteNonQueryAsync(ct).ConfigureAwait(false);
    }

    private static void AddParameter(DbCommand command, string relation)
    {
        var parameter = command.CreateParameter();
        parameter.ParameterName = "@relation";
        parameter.Value = relation;
        command.Parameters.Add(parameter);
    }
}
