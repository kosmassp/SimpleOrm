using System.Reflection;
using SimpleOrm;

namespace SimpleOrm.Cli;

/// <summary>Inputs of <c>simpleorm diff</c> (ADR-0017), the amend mode included (add.3).</summary>
public sealed class DiffOptions
{
    /// <summary>The assembly holding the migration versions.</summary>
    public required Assembly Assembly { get; init; }

    /// <summary>The entities to diff — every mapped type of the assembly, for the CLI.</summary>
    public required IReadOnlyList<Type> EntityTypes { get; init; }

    /// <summary>The migrations directory: snapshots are read from it, sources are written into it.</summary>
    public required string OutDir { get; init; }

    /// <summary>The migrations root namespace: the version-discovery filter and the namespace of the emitted code.</summary>
    public required string RootNamespace { get; init; }

    public required IDialect Dialect { get; init; }

    /// <summary>The CLI's <c>--dialect</c> name, stamped into the generated root.</summary>
    public required string DialectLabel { get; init; }

    /// <summary>The step description (file and class name suffix); "Auto" when absent.</summary>
    public string? Name { get; init; }

    /// <summary>Declared column renames per table — never inferred (ADR-0017).</summary>
    public IReadOnlyDictionary<string, IReadOnlyDictionary<string, string>> Renames { get; init; }
        = new Dictionary<string, IReadOnlyDictionary<string, string>>(StringComparer.OrdinalIgnoreCase);

    /// <summary>Destructive changes (<c>DDL-003</c>) are emitted only when set.</summary>
    public bool AllowRemove { get; init; }

    /// <summary>Regenerate the newest version in place instead of emitting the next one (ADR-0017 add.3).</summary>
    public bool Amend { get; init; }

    /// <summary>
    /// Amend only: replace a hand-written version too. Its raw SQL, hooks, data
    /// steps, and <c>Down()</c> overrides are not reproducible from the diff —
    /// the run names every such file so they can be re-added by hand.
    /// </summary>
    public bool Force { get; init; }

    /// <summary>
    /// Amend only, when a database was given: whether it has the version recorded
    /// as applied. The answer only warns — other databases exist the tool can't see.
    /// </summary>
    public Func<long, Task<bool>>? IsApplied { get; init; }
}

/// <summary>
/// <c>simpleorm diff</c>: the model is the final truth, the committed snapshots are
/// the recorded past, the difference is the next migration — ordinary source with
/// literal SQL, no database needed (ADR-0017). Tables diff by columns, views by
/// normalized DDL; new tables order FK-referenced first; views compose after
/// tables (§7.22). The recorded past must exist: a table that earlier versions
/// migrated but no snapshot describes would diff as brand new, so it refuses
/// and points at <c>shadow</c>/<c>snapshot</c>.
///
/// <b>Amend</b> (ADR-0017 add.3) regenerates the <i>newest</i> version instead:
/// the model against the schema the migrations below it produce (the snapshot
/// history <i>below</i> it — a snapshot taken of the draft would otherwise hide
/// the change), replacing the version's files. A version without the
/// generator's header is hand-written — its raw SQL, hooks, and data steps are
/// not reproducible from a diff — so replacing it takes <c>--force</c> and names
/// every such file. Nothing on disk changes until every refusal has passed:
/// steps 1–5 read, step 6 writes. Git, push state, and team agreement are human
/// protocol; the databases' recorded checksums (<c>MIG-010</c>) are the tool's
/// boundary.
/// </summary>
public static class DiffCommand
{
    private static readonly string[] KindFolders = ["Table", "View", "MaterializedView"];

    public static async Task<int> ExecuteAsync(DiffOptions options, TextWriter output, TextWriter error)
    {
        int Fail(string message)
        {
            error.WriteLine(message);
            return 1;
        }

        // 1. The target version: the next one, or — amending — the newest one.
        var versions = options.Assembly.GetTypes()
            .Where(t => !t.IsAbstract && typeof(MigrationVersion).IsAssignableFrom(t)
                && t.GetConstructor(Type.EmptyTypes) is not null
                && InNamespace(t, options.RootNamespace))
            .Select(t => ((MigrationVersion)Activator.CreateInstance(t)!).Version)
            .ToArray();
        var latest = versions.DefaultIfEmpty(0).Max();
        if (options.Amend && latest == 0)
        {
            return Fail($"nothing to amend: no migration versions under namespace {options.RootNamespace} in {options.Assembly.GetName().Name}");
        }

        var version = options.Amend ? latest : latest + 1;
        var prefix = $"V{version:0000}";

        // 2–3. Amend: locate the version's files and vet them.
        var replaceable = new List<string>();
        var notices = new List<string>();
        if (options.Amend)
        {
            var refusal = LocateVersionFiles(options, version, prefix, replaceable, notices);
            if (refusal is not null)
            {
                return Fail(refusal);
            }
        }

        // 4–5. The diff, against the schema the migrations below the target
        // version produce — the snapshot history below it. An object those
        // migrations touched but no snapshot records has no recorded past to
        // diff against: it would come out as brand new, so it refuses instead.
        var migratedBelow = EntitiesMigratedBelow(options, version);
        var loader = new EntityMapLoader();
        var changed = new List<(Type Type, EntityMap Map, MigrationGenerator.TableDiff Diff)>();
        var viewChanges = new List<(Type Type, string Folder, string ObjectName, string Ddl, string? PreviousDdl)>();
        var problems = new List<string>();
        var removals = new List<string>();
        var unrecorded = new List<string>();
        foreach (var type in options.EntityTypes.OrderBy(t => t.Name, StringComparer.Ordinal))
        {
            var map = loader.Load(type);
            if (map.Kind == RelationKind.Table)
            {
                var snapshotDir = Path.Combine(options.OutDir, "Table", type.Name);
                var baseline = Directory.Exists(snapshotDir)
                    ? Directory.GetFiles(snapshotDir, "V*.schema.json")
                        .Select(f => SchemaSnapshot.Parse(File.ReadAllText(f)))
                        .Where(s => s.AsOfVersion < version)
                        .OrderByDescending(s => s.AsOfVersion)
                        .Select(s => s.Schema)
                        .FirstOrDefault()
                    : null;
                if (baseline is null && migratedBelow.Contains(type))
                {
                    unrecorded.Add(map.RelationName!);
                    continue;
                }

                options.Renames.TryGetValue(map.RelationName!, out var tableRenames);
                var diff = MigrationGenerator.Diff(
                    map, options.Dialect, baseline, tableRenames ?? new Dictionary<string, string>());
                problems.AddRange(diff.Unsupported.Select(m => $"{map.RelationName}: {m}"));
                removals.AddRange(diff.Removed.Select(c => $"{map.RelationName}.{c.Name}")
                    .Concat(diff.RemovedIndexNames.Select(n => $"index {n}")));
                if (diff.HasChanges)
                {
                    changed.Add((type, map, diff));
                }
            }
            else if (map.Kind == RelationKind.View
                || (map.Kind == RelationKind.MaterializedView && options.Dialect.SupportsMaterializedViews))
            {
                // Views diff by DDL (ADR-0017 add.1): the definition is the schema.
                var folder = map.Kind == RelationKind.View ? "View" : "MaterializedView";
                var current = SchemaSnapshot.NormalizeDdl(options.Dialect.CreateViewSql(map));
                var snapshotDir = Path.Combine(options.OutDir, folder, type.Name);
                var previous = Directory.Exists(snapshotDir)
                    ? Directory.GetFiles(snapshotDir, "V*.schema.json")
                        .Select(f => SchemaSnapshot.ParseDdl(File.ReadAllText(f)))
                        .Where(s => s.AsOfVersion < version)
                        .OrderByDescending(s => s.AsOfVersion)
                        .Select(s => (string?)s.Ddl)
                        .FirstOrDefault()
                    : null;
                if (previous is null && migratedBelow.Contains(type))
                {
                    unrecorded.Add(map.RelationName!);
                    continue;
                }

                if (previous != current)
                {
                    viewChanges.Add((type, folder, map.RelationName!, current, previous));
                }
            }
        }

        if (unrecorded.Count > 0)
        {
            return Fail(
                $"no recorded schema below {prefix} for {string.Join(", ", unrecorded)} — earlier versions migrate "
                + $"them but no snapshot describes them, so the diff would create them anew; run simpleorm shadow "
                + $"--to V{version - 1:0000} (sqlite) or snapshot on a database migrated to V{version - 1:0000}, then retry");
        }

        if (problems.Count > 0)
        {
            foreach (var problem in problems)
            {
                error.WriteLine("DDL-004 " + problem);
            }

            return 1;
        }

        if (removals.Count > 0 && !options.AllowRemove)
        {
            return Fail("DDL-003 destructive changes need --allow-remove: " + string.Join(", ", removals));
        }

        // The files this run would write, computed before anything is touched.
        var description = options.Name ?? "Auto";
        var planned = new List<(string Path, string Content)>();
        var stepRefs = new List<string>();
        if (changed.Count > 0 || viewChanges.Count > 0)
        {
            // New tables first, FK-referenced before referencing; then modified tables by name.
            var newSet = changed.Where(c => c.Diff.IsNew).Select(c => c.Type).ToHashSet();
            var ordered = TopologicalByForeignKey(changed.Where(c => c.Diff.IsNew).ToList(), newSet)
                .Concat(changed.Where(c => !c.Diff.IsNew));
            foreach (var (type, map, diff) in ordered)
            {
                planned.Add((
                    Path.Combine(options.OutDir, "Table", type.Name, $"{prefix}_{description}.cs"),
                    MigrationGenerator.EmitTableStep(
                        options.RootNamespace, type, map, options.Dialect, version, description, diff)));
                stepRefs.Add($"Table.{type.Name}.{prefix}_{description}");
            }

            foreach (var (type, folder, objectName, ddl, previousDdl) in viewChanges)
            {
                planned.Add((
                    Path.Combine(options.OutDir, folder, type.Name, $"{prefix}_{description}.cs"),
                    MigrationGenerator.EmitViewStep(
                        options.RootNamespace, type, folder, objectName, version, description, ddl, previousDdl)));
                stepRefs.Add($"{folder}.{type.Name}.{prefix}_{description}");
            }

            planned.Add((
                Path.Combine(options.OutDir, $"{prefix}.cs"),
                MigrationGenerator.EmitRoot(options.RootNamespace, version, stepRefs, options.DialectLabel)));
        }

        // A file that exists and is not the tool's own for this version is never overwritten.
        foreach (var (path, _) in planned)
        {
            if (File.Exists(path) && !replaceable.Contains(path, PathComparer))
            {
                return Fail($"refusing to overwrite {path}");
            }
        }

        if (planned.Count == 0 && !options.Amend)
        {
            output.WriteLine("no schema changes: the model matches the snapshots");
            return 0;
        }

        if (options.Amend && options.IsApplied is not null && await options.IsApplied(version).ConfigureAwait(false))
        {
            output.WriteLine(
                $"warning: {prefix} is recorded as applied in the database; amending changes its checksum, so migrate "
                + $"reports MIG-010 there — migrate down --to {version - 1} or recreate that database before re-applying");
        }

        // 6. Commit: every refusal has passed; the tree changes here and only here.
        foreach (var notice in notices)
        {
            output.WriteLine(notice);
        }

        foreach (var path in replaceable.Where(p => !planned.Any(w => PathComparer.Equals(w.Path, p))))
        {
            File.Delete(path);
            output.WriteLine("deleted " + path);
        }

        foreach (var (path, content) in planned)
        {
            Directory.CreateDirectory(Path.GetDirectoryName(path)!);
            File.WriteAllText(path, content);
            output.WriteLine("wrote " + path);
        }

        if (planned.Count == 0)
        {
            output.WriteLine($"{prefix} removed: the model matches the snapshot history below it, so the version has no content");
            return 0;
        }

        output.WriteLine(
            $"review the generated {prefix}, build, migrate, then refresh snapshots: simpleorm snapshot --out <MigrationsDir>");
        return 0;
    }

    /// <summary>
    /// Amend steps 2–3: the root, every <c>V000N_*.cs</c> step under the generator's
    /// layout, and any snapshot stamped at that version (it describes the draft).
    /// Refuses — naming the file — when the root or a step lacks the generator's
    /// header and <c>--force</c> was not given (hand-written content is not
    /// reproducible from a diff; with force it is replaced and every such file is
    /// noted), when the root was stamped for another dialect, or when the number
    /// of step files disagrees with the number of compiled steps: the layout
    /// diverged from the tool's, and the tool does not guess.
    /// </summary>
    private static string? LocateVersionFiles(
        DiffOptions options, long version, string prefix, List<string> replaceable, List<string> notices)
    {
        var rootFile = Path.Combine(options.OutDir, $"{prefix}.cs");
        if (!File.Exists(rootFile))
        {
            return $"amend: {rootFile} not found — {prefix} is the newest version in the assembly but has no root under --out";
        }

        var rootSource = File.ReadAllText(rootFile);
        var stamped = MigrationGenerator.GeneratedDialectLabel(rootSource);
        if (stamped is not null && !string.Equals(stamped, options.DialectLabel, StringComparison.OrdinalIgnoreCase))
        {
            return $"amend refuses: {prefix} was generated for dialect {stamped}; storage types are dialect-specific — run with --dialect {stamped}";
        }

        var objectDirs = KindFolders
            .Select(kind => Path.Combine(options.OutDir, kind))
            .Where(Directory.Exists)
            .SelectMany(Directory.GetDirectories)
            .ToArray();
        var stepFiles = objectDirs
            .SelectMany(dir => Directory.GetFiles(dir, $"{prefix}_*.cs"))
            .OrderBy(f => f, StringComparer.Ordinal)
            .ToArray();

        foreach (var file in new[] { rootFile }.Concat(stepFiles))
        {
            if (MigrationGenerator.IsGenerated(file == rootFile ? rootSource : File.ReadAllText(file)))
            {
                continue;
            }

            if (!options.Force)
            {
                return $"amend refuses: {file} is hand-written (no '{MigrationGenerator.GeneratedMarker}' header) — "
                    + "its raw SQL, hooks, data steps, and Down() are not reproducible from the diff; add --force to replace it anyway and re-add those by hand";
            }

            notices.Add($"replacing hand-written {file} (--force): re-add by hand any raw SQL, hooks, data steps, or Down() it carried that still apply");
        }

        var compiledSteps = options.Assembly.GetTypes()
            .Count(t => !t.IsAbstract && typeof(MigrationStep).IsAssignableFrom(t)
                && InNamespace(t, options.RootNamespace)
                && t.Name.StartsWith(prefix + "_", StringComparison.Ordinal));
        if (compiledSteps != stepFiles.Length)
        {
            return $"amend refuses: {prefix} compiles {compiledSteps} step(s) but {stepFiles.Length} {prefix}_*.cs file(s) exist under {options.OutDir} — the layout diverged from the generator's";
        }

        replaceable.Add(rootFile);
        replaceable.AddRange(stepFiles);
        replaceable.AddRange(objectDirs.SelectMany(dir => Directory.GetFiles(dir, $"{prefix}.schema.json")));
        return null;
    }

    /// <summary>
    /// The entity types that migration steps below <paramref name="version"/>
    /// touch (<c>TableMigration&lt;T&gt;</c>/<c>ViewMigration&lt;T&gt;</c> in the
    /// namespace): objects with a migration history, which therefore need a
    /// recorded schema to diff against.
    /// </summary>
    private static HashSet<Type> EntitiesMigratedBelow(DiffOptions options, long version)
    {
        var migrated = new HashSet<Type>();
        foreach (var type in options.Assembly.GetTypes()
            .Where(t => !t.IsAbstract && typeof(MigrationStep).IsAssignableFrom(t)
                && t.GetConstructor(Type.EmptyTypes) is not null
                && InNamespace(t, options.RootNamespace)))
        {
            var entity = EntityOf(type);
            if (entity is not null && ((MigrationStep)Activator.CreateInstance(type)!).Version < version)
            {
                migrated.Add(entity);
            }
        }

        return migrated;
    }

    private static Type? EntityOf(Type step)
    {
        for (var type = step.BaseType; type is not null; type = type.BaseType)
        {
            if (type.IsGenericType
                && (type.GetGenericTypeDefinition() == typeof(TableMigration<>)
                    || type.GetGenericTypeDefinition() == typeof(ViewMigration<>)))
            {
                return type.GetGenericArguments()[0];
            }
        }

        return null;
    }

    private static bool InNamespace(Type type, string rootNamespace)
        => (type.Namespace ?? string.Empty).StartsWith(rootNamespace, StringComparison.Ordinal);

    private static readonly StringComparer PathComparer = StringComparer.OrdinalIgnoreCase;

    private static List<(Type Type, EntityMap Map, MigrationGenerator.TableDiff Diff)> TopologicalByForeignKey(
        List<(Type Type, EntityMap Map, MigrationGenerator.TableDiff Diff)> newTables, HashSet<Type> newSet)
    {
        var ordered = new List<(Type, EntityMap, MigrationGenerator.TableDiff)>();
        var visited = new HashSet<Type>();

        void Visit((Type Type, EntityMap Map, MigrationGenerator.TableDiff Diff) node)
        {
            if (!visited.Add(node.Type))
            {
                return;
            }

            foreach (var target in node.Map.Properties
                .Where(p => p.ForeignKeyReferences is not null && newSet.Contains(p.ForeignKeyReferences))
                .Select(p => p.ForeignKeyReferences!))
            {
                var dependency = newTables.FirstOrDefault(c => c.Type == target);
                if (dependency.Type is not null)
                {
                    Visit(dependency);
                }
            }

            ordered.Add(node);
        }

        foreach (var node in newTables)
        {
            Visit(node);
        }

        return ordered;
    }
}
