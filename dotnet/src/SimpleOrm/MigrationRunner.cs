using System.Data.Common;
using System.Diagnostics;
using System.Globalization;
using System.Reflection;
using System.Security.Cryptography;
using System.Text;

namespace SimpleOrm;

public enum MigrationState
{
    Pending,
    Applied,
    Drifted,
    Unknown,
}

/// <summary>One (version, object) entry of the migration status.</summary>
public sealed class MigrationEntry(long version, string objectName, string description, MigrationState state)
{
    public long Version { get; } = version;

    public string ObjectName { get; } = objectName;

    public string Description { get; } = description;

    public MigrationState State { get; } = state;

    public override string ToString() => $"V{Version:0000} {ObjectName,-28} {Description,-30} {State}";
}

/// <summary>
/// Applies versioned code migrations (ADR-0013). The whole run executes inside the
/// dialect's run lock — on SQLite one <c>BEGIN IMMEDIATE</c> transaction, which also
/// makes a failed run fully atomic. Every plan is validated (names, duplicates,
/// composition, drift) before any statement executes. The application never calls
/// this at startup; migration is an explicit act (§7.24).
/// </summary>
public sealed class MigrationRunner
{
    private readonly Db _db;
    private readonly IReadOnlyList<MigrationVersion> _versions;
    private readonly SnapshotSet _snapshots;

    /// <summary>Snapshots default to the assembly's embedded resources; pass <paramref name="snapshots"/> to read them elsewhere.</summary>
    public MigrationRunner(Db db, Assembly assembly, string? migrationsNamespace = null, SnapshotSet? snapshots = null)
        : this(db, Discover(assembly, migrationsNamespace, out var strays), straySteps: strays,
            snapshots ?? SnapshotSet.FromAssembly(assembly))
    {
    }

    public MigrationRunner(Db db, IEnumerable<MigrationVersion> versions, SnapshotSet? snapshots = null)
        : this(db, versions.ToArray(), straySteps: [], snapshots)
    {
    }

    private MigrationRunner(
        Db db, IReadOnlyList<MigrationVersion> versions, IReadOnlyList<MigrationStep> straySteps,
        SnapshotSet? snapshots = null)
    {
        _db = db;
        _snapshots = snapshots ?? new SnapshotSet();
        _versions = versions.OrderBy(v => v.Version).ToArray();

        var duplicate = _versions.GroupBy(v => v.Version).FirstOrDefault(g => g.Count() > 1);
        if (duplicate is not null)
        {
            throw new SimpleOrmException(
                "MIG-002", "V" + duplicate.Key, "more than one root migration declares this version");
        }

        ValidateComposition(straySteps);
    }

    /// <summary>Applies pending versions in order; returns how many were applied.</summary>
    public Task<int> MigrateAsync(CancellationToken ct)
        => MigrateAsync(allowViewDrift: false, notify: null, ct);

    /// <summary>
    /// As <see cref="MigrateAsync(CancellationToken)"/>. A view step's
    /// <c>ExpectDefinition</c> guard normally refuses on a live definition that was
    /// changed outside the code (<c>MIG-012</c>); with
    /// <paramref name="allowViewDrift"/> the drift is reported through
    /// <paramref name="notify"/> and the view is recreated anyway.
    /// </summary>
    public async Task<int> MigrateAsync(bool allowViewDrift, Action<string>? notify, CancellationToken ct)
    {
        var plan = RenderAll();
        using var run = _db.Options.Dialect.BeginMigrationRunLock(_db.Connection);
        await EnsureVersionTableAsync(run, ct).ConfigureAwait(false);
        var recorded = await ReadRecordedAsync(run, ct).ConfigureAwait(false);
        ValidateHistory(plan, recorded);

        var applied = 0;
        foreach (var version in plan.Where(v => !recorded.Versions.Contains(v.Version)))
        {
            foreach (var step in version.Steps)
            {
                var stopwatch = Stopwatch.StartNew();
                foreach (var statement in step.Up)
                {
                    await ApplyStatementAsync(run, version.Version, statement, allowViewDrift, notify, ct)
                        .ConfigureAwait(false);
                }

                await RecordAsync(run, version.Version, step, stopwatch.ElapsedMilliseconds, ct).ConfigureAwait(false);
            }

            applied++;
        }

        run.Commit();
        return applied;
    }

    /// <summary>Reverts versions above <paramref name="targetVersion"/>, newest first; refuses when any lacks down statements (<c>MIG-020</c>).</summary>
    public Task<int> MigrateDownAsync(long targetVersion, CancellationToken ct)
        => MigrateDownAsync(targetVersion, allowViewDrift: false, notify: null, ct);

    /// <summary>As <see cref="MigrateDownAsync(long, CancellationToken)"/>, with the view-drift override (see MigrateAsync).</summary>
    public async Task<int> MigrateDownAsync(
        long targetVersion, bool allowViewDrift, Action<string>? notify, CancellationToken ct)
    {
        var plan = RenderAll();
        using var run = _db.Options.Dialect.BeginMigrationRunLock(_db.Connection);
        await EnsureVersionTableAsync(run, ct).ConfigureAwait(false);
        var recorded = await ReadRecordedAsync(run, ct).ConfigureAwait(false);
        ValidateHistory(plan, recorded);

        var reverting = plan
            .Where(v => v.Version > targetVersion && recorded.Versions.Contains(v.Version))
            .OrderByDescending(v => v.Version)
            .ToArray();

        // Resolve every step's rollback before anything executes (§7.23): the
        // hand-written Down() wins as the manual override; otherwise it derives
        // from the versioned snapshots (ADR-0018). Only a step the snapshots
        // cannot support still refuses (MIG-020).
        var notices = new List<string>();
        foreach (var step in reverting.SelectMany(v => v.Steps))
        {
            if (step.Up.Count == 0 || step.ManualDownCore.Count > 0)
            {
                continue;
            }

            step.DerivedDownCore = DownDeriver.Derive(
                step.ObjectName, step.Version, _snapshots, step.UpRenames, notices, _db.Options.Dialect);
            if (step.DerivedDownCore is null)
            {
                throw new SimpleOrmException(
                    "MIG-020", $"V{step.Version:0000} {step.ObjectName}",
                    "no snapshot to derive the rollback from (run simpleorm snapshot/shadow and embed the .schema.json files), or override Down()");
            }
        }

        foreach (var notice in notices)
        {
            notify?.Invoke(notice);
        }

        foreach (var version in reverting)
        {
            foreach (var step in version.Steps.Reverse())
            {
                var statements = step.DownPre
                    .Concat(step.ManualDownCore.Count > 0 ? step.ManualDownCore : step.DerivedDownCore ?? [])
                    .Concat(step.DownPost);
                foreach (var statement in statements)
                {
                    await ApplyStatementAsync(run, version.Version, statement, allowViewDrift, notify, ct)
                        .ConfigureAwait(false);
                }
            }

            await ExecuteAsync(run, $"delete from schema_version where version = {version.Version}", ct).ConfigureAwait(false);
        }

        run.Commit();
        return reverting.Length;
    }

    /// <summary>Records versions ≤ <paramref name="version"/> as applied without running them (§7.23).</summary>
    public async Task BaselineAsync(long version, CancellationToken ct)
    {
        var plan = RenderAll();
        using var run = _db.Options.Dialect.BeginMigrationRunLock(_db.Connection);
        await EnsureVersionTableAsync(run, ct).ConfigureAwait(false);
        var recorded = await ReadRecordedAsync(run, ct).ConfigureAwait(false);

        foreach (var entry in plan.Where(v => v.Version <= version && !recorded.Versions.Contains(v.Version)))
        {
            foreach (var step in entry.Steps)
            {
                await RecordAsync(run, entry.Version, step, executionMs: 0, ct).ConfigureAwait(false);
            }
        }

        run.Commit();
    }

    public async Task<IReadOnlyList<MigrationEntry>> StatusAsync(CancellationToken ct)
    {
        var plan = RenderAll();
        var recorded = await ReadRecordedAsync(transaction: null, ct).ConfigureAwait(false);

        var entries = new List<MigrationEntry>();
        foreach (var version in plan)
        {
            foreach (var step in version.Steps)
            {
                var state = recorded.Rows.TryGetValue((version.Version, step.ObjectName), out var row)
                    ? row.Checksum == step.Checksum ? MigrationState.Applied : MigrationState.Drifted
                    : recorded.Versions.Contains(version.Version) ? MigrationState.Drifted : MigrationState.Pending;
                entries.Add(new MigrationEntry(version.Version, step.ObjectName, step.Description, state));
            }
        }

        foreach (var row in recorded.Rows.Where(r =>
            !plan.Any(v => v.Version == r.Key.Version && v.Steps.Any(s => s.ObjectName == r.Key.Object))))
        {
            entries.Add(new MigrationEntry(row.Key.Version, row.Key.Object, row.Value.Description, MigrationState.Unknown));
        }

        return entries.OrderBy(e => e.Version).ThenBy(e => e.ObjectName).ToArray();
    }

    /// <summary>True when any version is unapplied — the <c>MIG-030</c> check SchemaGuard runs at milestone 6.</summary>
    public async Task<bool> HasPendingAsync(CancellationToken ct)
        => (await StatusAsync(ct).ConfigureAwait(false)).Any(e => e.State == MigrationState.Pending);

    // --- discovery and validation -------------------------------------------------

    private static IReadOnlyList<MigrationVersion> Discover(
        Assembly assembly, string? migrationsNamespace, out IReadOnlyList<MigrationStep> steps)
    {
        bool InScope(Type type) => migrationsNamespace is null
            || (type.Namespace ?? string.Empty).StartsWith(migrationsNamespace, StringComparison.Ordinal);

        var types = assembly.GetTypes()
            .Where(t => !t.IsAbstract && InScope(t) && t.GetConstructor(Type.EmptyTypes) is not null)
            .ToArray();

        steps = types
            .Where(t => typeof(MigrationStep).IsAssignableFrom(t))
            .Select(t => (MigrationStep)Activator.CreateInstance(t)!)
            .ToArray();
        return types
            .Where(t => typeof(MigrationVersion).IsAssignableFrom(t))
            .Select(t => (MigrationVersion)Activator.CreateInstance(t)!)
            .ToArray();
    }

    private void ValidateComposition(IReadOnlyList<MigrationStep> strayCandidates)
    {
        var composedTypes = new HashSet<Type>();
        foreach (var version in _versions)
        {
            var builder = new VersionBuilder();
            version.Compose(builder);

            var seenObjects = new HashSet<string>();
            foreach (var step in builder.Steps)
            {
                composedTypes.Add(step.GetType());
                if (step.Version != version.Version)
                {
                    throw new SimpleOrmException(
                        "MIG-003", step.GetType().Name,
                        $"declares version {step.Version} but is composed by V{version.Version:0000}");
                }

                var objectName = step.ObjectName(_db.Maps);
                if (!seenObjects.Add(objectName))
                {
                    throw new SimpleOrmException(
                        "MIG-002", $"V{version.Version:0000} {objectName}", "composed twice in one version");
                }
            }
        }

        var stray = strayCandidates.FirstOrDefault(s => !composedTypes.Contains(s.GetType()));
        if (stray is not null)
        {
            throw new SimpleOrmException(
                "MIG-004", stray.GetType().Name, "exists but no root version composes it");
        }
    }

    private sealed class RenderedStep(
        long version, string objectName, string description,
        IReadOnlyList<MigrationStatement> up, DownPlan down,
        IReadOnlyList<(string From, string To)> upRenames)
    {
        public long Version { get; } = version;

        public string ObjectName { get; } = objectName;

        public string Description { get; } = description;

        public IReadOnlyList<MigrationStatement> Up { get; } = up;

        /// <summary>The hand-written down core — the manual override (ADR-0018); empty means derive.</summary>
        public IReadOnlyList<MigrationStatement> ManualDownCore { get; } = down.Core;

        public IReadOnlyList<MigrationStatement> DownPre { get; } = down.Pre;

        public IReadOnlyList<MigrationStatement> DownPost { get; } = down.Post;

        /// <summary>The step's declared renames — the one piece the snapshot diff can't recover.</summary>
        public IReadOnlyList<(string From, string To)> UpRenames { get; } = upRenames;

        /// <summary>The rollback derived from the snapshots, resolved during down validation.</summary>
        public IReadOnlyList<MigrationStatement>? DerivedDownCore { get; set; }

        public string Checksum { get; } = ComputeChecksum(up);
    }

    private sealed class RenderedVersion(long version, IReadOnlyList<RenderedStep> steps)
    {
        public long Version { get; } = version;

        public IReadOnlyList<RenderedStep> Steps { get; } = steps;
    }

    private IReadOnlyList<RenderedVersion> RenderAll()
        => _versions.Select(version =>
        {
            var builder = new VersionBuilder();
            version.Compose(builder);
            return new RenderedVersion(version.Version, builder.Steps
                .Select(step => new RenderedStep(
                    version.Version,
                    step.ObjectName(_db.Maps),
                    step.Description,
                    step.RenderUp(_db.Maps, _db.Options.Dialect),
                    step.RenderDown(_db.Maps, _db.Options.Dialect),
                    step.UpRenames(_db.Maps, _db.Options.Dialect)))
                .ToArray());
        }).ToArray();

    private static void ValidateHistory(IReadOnlyList<RenderedVersion> plan, RecordedHistory recorded)
    {
        foreach (var version in plan.Where(v => recorded.Versions.Contains(v.Version)))
        {
            foreach (var step in version.Steps)
            {
                if (!recorded.Rows.TryGetValue((version.Version, step.ObjectName), out var row))
                {
                    throw new SimpleOrmException(
                        "MIG-010", $"V{version.Version:0000} {step.ObjectName}",
                        "the applied version has no record for this object; history and code disagree");
                }

                if (row.Checksum != step.Checksum)
                {
                    throw new SimpleOrmException(
                        "MIG-010", $"V{version.Version:0000} {step.ObjectName}",
                        "checksum changed since it was applied; applied migrations must not change");
                }
            }
        }

        var unknown = recorded.Rows.Keys.FirstOrDefault(k => plan.All(v => v.Version != k.Version));
        if (unknown != default)
        {
            throw new SimpleOrmException(
                "MIG-011", $"V{unknown.Version:0000} {unknown.Object}", "applied in the database but unknown to the code");
        }
    }

    // --- storage ------------------------------------------------------------------

    private static string ComputeChecksum(IReadOnlyList<MigrationStatement> statements)
    {
        using var sha = SHA256.Create();
        var bytes = sha.ComputeHash(Encoding.UTF8.GetBytes(string.Join("\n;\n", statements.Select(s => s.Sql))));
        var builder = new StringBuilder(bytes.Length * 2);
        foreach (var b in bytes)
        {
            builder.Append(b.ToString("x2", CultureInfo.InvariantCulture));
        }

        return builder.ToString();
    }

    private sealed class RecordedHistory(
        Dictionary<(long Version, string Object), (string Description, string Checksum)> rows)
    {
        public Dictionary<(long Version, string Object), (string Description, string Checksum)> Rows { get; } = rows;

        public HashSet<long> Versions { get; } = [.. rows.Keys.Select(k => k.Version)];
    }

    private Task EnsureVersionTableAsync(DbTransaction transaction, CancellationToken ct)
        => ExecuteAsync(transaction, _db.Options.Dialect.VersionTableSql, ct);

    private async Task<RecordedHistory> ReadRecordedAsync(DbTransaction? transaction, CancellationToken ct)
    {
        var rows = new Dictionary<(long, string), (string, string)>();
        using var command = _db.Connection.CreateCommand();
        command.Transaction = transaction;
        command.CommandText = "select version, object, description, checksum from schema_version";
        try
        {
            var reader = await command.ExecuteReaderAsync(ct).ConfigureAwait(false);
            try
            {
                while (await reader.ReadAsync(ct).ConfigureAwait(false))
                {
                    rows[((long)reader.GetValue(0), reader.GetString(1))] = (reader.GetString(2), reader.GetString(3));
                }
            }
            finally
            {
                reader.Dispose();
            }
        }
        catch (DbException)
        {
            // No schema_version table yet: nothing recorded.
        }

        return new RecordedHistory(rows);
    }

    private async Task RecordAsync(
        DbTransaction transaction, long version, RenderedStep step, long executionMs, CancellationToken ct)
    {
        using var command = _db.Connection.CreateCommand();
        command.Transaction = transaction;
        command.CommandText =
            "insert into schema_version (version, object, description, checksum, applied_at, execution_ms) "
            + "values (@version, @object, @description, @checksum, @applied_at, @execution_ms)";
        AddParameter(command, "@version", version);
        AddParameter(command, "@object", step.ObjectName);
        AddParameter(command, "@description", step.Description);
        AddParameter(command, "@checksum", step.Checksum);
        AddParameter(command, "@applied_at", DateTime.UtcNow.ToString("o", CultureInfo.InvariantCulture));
        AddParameter(command, "@execution_ms", executionMs);
        await command.ExecuteNonQueryAsync(ct).ConfigureAwait(false);
    }

    /// <summary>Executes one rendered statement — or, for a guard, checks the view's live definition instead.</summary>
    private async Task ApplyStatementAsync(
        DbTransaction transaction, long version, MigrationStatement statement,
        bool allowViewDrift, Action<string>? notify, CancellationToken ct)
    {
        if (statement.GuardView is null)
        {
            await ExecuteAsync(transaction, statement.Sql, ct).ConfigureAwait(false);
            return;
        }

        var live = await ReadViewDefinitionAsync(transaction, statement.GuardView, ct).ConfigureAwait(false);
        var normalized = live is null ? null : SchemaSnapshot.NormalizeDdl(live);
        if (normalized == statement.Sql)
        {
            return;
        }

        var drift = live is null
            ? $"V{version:0000} {statement.GuardView}: the view is absent; expected the previous definition"
            : $"V{version:0000} {statement.GuardView}: the live definition does not match the expected one — it was changed outside migrations";
        if (!allowViewDrift)
        {
            throw new SimpleOrmException(
                "MIG-012", $"V{version:0000} {statement.GuardView}",
                (live is null
                    ? "the view is absent but a previous definition was expected"
                    : "the live definition was changed outside migrations")
                + "; review the drift, then rerun with --force to recreate it from the code");
        }

        notify?.Invoke(drift + "; recreating (--force)");
    }

    private async Task<string?> ReadViewDefinitionAsync(DbTransaction transaction, string view, CancellationToken ct)
    {
        using var command = _db.Connection.CreateCommand();
        command.Transaction = transaction;
        command.CommandText = _db.Options.Dialect.ViewDefinitionSql;
        var parameter = command.CreateParameter();
        parameter.ParameterName = "@relation";
        parameter.Value = view;
        command.Parameters.Add(parameter);
        return await command.ExecuteScalarAsync(ct).ConfigureAwait(false) as string;
    }

    private async Task ExecuteAsync(DbTransaction transaction, string sql, CancellationToken ct)
    {
        using var command = _db.Connection.CreateCommand();
        command.Transaction = transaction;
        command.CommandText = sql;
        try
        {
            await command.ExecuteNonQueryAsync(ct).ConfigureAwait(false);
        }
        catch (DbException exception)
        {
            throw new SimpleOrmException("MIG-021", "migration statement", exception.Message + " -- while executing: " + sql);
        }
    }

    private static void AddParameter(DbCommand command, string name, object value)
    {
        var parameter = command.CreateParameter();
        parameter.ParameterName = name;
        parameter.Value = value;
        command.Parameters.Add(parameter);
    }
}
