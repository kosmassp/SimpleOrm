using System.Reflection;
using SimpleOrm;
using SimpleOrm.Sqlite;
using SimpleOrm.SqlServer;

// simpleorm CLI (§7.24): migration is always an explicit act — the application
// never migrates at startup. Migrations and entities are code, so the CLI loads
// the application assembly and works from its types.

var boolFlags = new HashSet<string>(StringComparer.OrdinalIgnoreCase) { "force", "allow-delete", "allow-remove" };
var positional = new List<string>();
var options = new Dictionary<string, List<string>>(StringComparer.OrdinalIgnoreCase);
for (var i = 0; i < args.Length; i++)
{
    if (args[i].StartsWith("--", StringComparison.Ordinal))
    {
        var name = args[i][2..];
        if (!options.TryGetValue(name, out var values))
        {
            options[name] = values = [];
        }

        if (!boolFlags.Contains(name) && i + 1 < args.Length)
        {
            values.Add(args[++i]);
        }
    }
    else
    {
        positional.Add(args[i]);
    }
}

if (positional.Count == 0)
{
    return Usage();
}

try
{
    return positional[0].ToLowerInvariant() switch
    {
        "migrate" when positional.Count > 1 && positional[1] == "down" => await MigrateDownAsync(),
        "migrate" => await MigrateAsync(),
        "status" => await StatusAsync(),
        "baseline" => await BaselineAsync(),
        "export-metadata" => ExportMetadata(),
        "validate" => await ValidateAsync(),
        "snapshot" => await SnapshotAsync(),
        "diff" => DiffCommand(),
        "shadow" => await ShadowAsync(),
        _ => Usage(),
    };
}
catch (SchemaValidationException exception)
{
    Console.Error.WriteLine(exception.Message);
    return 1;
}
catch (SimpleOrmException exception)
{
    Console.Error.WriteLine(exception.Message);
    return 1;
}

int Usage()
{
    Console.WriteLine(
        """
        simpleorm — SQL-first micro-ORM CLI

        commands (all need --assembly <dll>; migrate/status/baseline/validate/snapshot also --db):
          migrate [--force [--allow-delete]]
                                      apply pending versions; a guarded view step refuses
                                      when the live view was changed outside the code
                                      (MIG-012) unless --force recreates it; --force then
                                      also syncs the live schema to the model (additive
                                      only; deletions need --allow-delete, DDL-003)
          migrate down --to <N> [--snapshots <MigrationsDir>] [--force]
                                      revert versions above N; rollbacks derive from the
                                      snapshots (embedded in the assembly, or --snapshots);
                                      Down() overrides; no snapshot is MIG-020
          status                      list (version, object) states
          baseline --version <N>      record versions <= N without running them
          export-metadata [--out dir] write each entity's EntityMap JSON
          validate                    SchemaGuard: full report or exit 0
          snapshot --out <MigrationsDir>
                                      write V000N.schema.json per object (tables by
                                      columns; views by DDL), versioned and timestamped
          diff --out <MigrationsDir> --namespace <ns>
               [--name <Description>] [--rename table.old=new]... [--allow-remove]
                                      generate the next migration version from the model vs
                                      the latest snapshots (tables by columns, views by
                                      DDL); no database needed; removals need
                                      --allow-remove (DDL-003); inexpressible changes are
                                      DDL-004 (write by hand)
          shadow --out <MigrationsDir> [--from V000N] [--to V000M]
                                      rebuild snapshots by replaying migrations in a
                                      throwaway database; --from trusts version N as
                                      correct (baseline from committed snapshots, no
                                      verification below N) and regenerates only (N, M]

        options:
          --assembly <path>   the application assembly containing migrations/entities
          --db <value>        connection string, or a bare path to a SQLite file
          --namespace <ns>    migration namespace filter (default: whole assembly)
          --dialect <name>    sqlite (default) or sqlserver (ADR-0024); shadow is
                              sqlite-only — other dialects override Down() instead
        """);
    return 2;
}

int Fail(string message)
{
    Console.Error.WriteLine(message);
    return 1;
}

string? Option(string name)
    => options.TryGetValue(name, out var values) && values.Count > 0 ? values[^1] : null;

// --dialect (ADR-0024): sqlite stays the default; unknown names refuse loudly.
IDialect DialectFor() => (Option("dialect") ?? "sqlite").ToLowerInvariant() switch
{
    "sqlite" => (IDialect)new SqliteDialect(),
    "sqlserver" => new SqlServerDialect(),
    var name => throw new SimpleOrmException(
        "CLI", "arguments", $"unknown --dialect '{name}' (sqlite, sqlserver)"),
};

bool Flag(string name) => options.ContainsKey(name);

Assembly LoadAssembly()
{
    if (Option("assembly") is not { } path)
    {
        throw new SimpleOrmException("CLI", "arguments", "--assembly <path> is required");
    }

    return Assembly.LoadFrom(Path.GetFullPath(path));
}

IEnumerable<Type> MappedTypes(Assembly assembly)
    => assembly.GetExportedTypes()
        .Where(t => t is { IsClass: true, IsAbstract: false } && EntityMapLoader.HasMappingAttributes(t));

async Task<(Db Db, MigrationRunner Runner)> OpenAsync()
{
    if (Option("db") is not { } db)
    {
        throw new SimpleOrmException("CLI", "arguments", "--db <connection string or file> is required");
    }

    var connectionString = db.Contains('=') ? db : $"Data Source={db}";
    var session = await Db.OpenAsync(
        connectionString, new DbOptions { Dialect = DialectFor() }, CancellationToken.None);

    // Rollbacks derive from the snapshots (ADR-0018): embedded in the assembly by
    // default, or read from the source Migrations dir via --snapshots.
    var snapshots = Option("snapshots") is { } snapshotDir ? SnapshotSet.FromDirectory(snapshotDir) : null;
    return (session, new MigrationRunner(session, LoadAssembly(), Option("namespace"), snapshots));
}

async Task<int> MigrateAsync()
{
    var (db, runner) = await OpenAsync();
    await using (db)
    {
        var applied = await runner.MigrateAsync(
            allowViewDrift: Flag("force"), notify: m => Console.WriteLine("MIG-012 " + m), CancellationToken.None);
        Console.WriteLine(applied == 0 ? "nothing pending" : $"applied {applied} version(s)");
        return Flag("force") ? await ForceSyncAsync(db) : 0;
    }
}

async Task<int> ForceSyncAsync(Db db)
{
    var plan = await SchemaSync.PlanAsync(db, MappedTypes(LoadAssembly()), CancellationToken.None);
    await SchemaSync.ApplyAsync(db, plan.Additive, CancellationToken.None);
    foreach (var sql in plan.Additive)
    {
        Console.WriteLine("sync: " + sql);
    }

    if (plan.Deletions.Count > 0)
    {
        if (Flag("allow-delete"))
        {
            await SchemaSync.ApplyAsync(db, plan.Deletions, CancellationToken.None);
            foreach (var sql in plan.Deletions)
            {
                Console.WriteLine("sync (destructive): " + sql);
            }
        }
        else
        {
            foreach (var sql in plan.Deletions)
            {
                Console.WriteLine("DDL-003 skipped (needs --allow-delete): " + sql);
            }
        }
    }

    foreach (var message in plan.Unsupported)
    {
        Console.Error.WriteLine("DDL-004 " + message);
    }

    if (plan.IsEmpty)
    {
        Console.WriteLine("schema already matches the model");
    }

    return plan.Unsupported.Count > 0 ? 1 : 0;
}

async Task<int> MigrateDownAsync()
{
    if (Option("to") is not { } to || !long.TryParse(to, out var target))
    {
        return Fail("migrate down requires --to <version>");
    }

    var (db, runner) = await OpenAsync();
    await using (db)
    {
        var reverted = await runner.MigrateDownAsync(
            target, allowViewDrift: Flag("force"), notify: m => Console.WriteLine("MIG-012 " + m), CancellationToken.None);
        Console.WriteLine($"reverted {reverted} version(s); now at <= V{target:0000}");
        return 0;
    }
}

async Task<int> StatusAsync()
{
    var (db, runner) = await OpenAsync();
    await using (db)
    {
        foreach (var entry in await runner.StatusAsync(CancellationToken.None))
        {
            Console.WriteLine(entry);
        }

        return 0;
    }
}

async Task<int> BaselineAsync()
{
    if (Option("version") is not { } raw || !long.TryParse(raw, out var version))
    {
        return Fail("baseline requires --version <N>");
    }

    var (db, runner) = await OpenAsync();
    await using (db)
    {
        await runner.BaselineAsync(version, CancellationToken.None);
        Console.WriteLine($"baselined at V{version:0000}");
        return 0;
    }
}

async Task<int> ValidateAsync()
{
    var (db, _) = await OpenAsync();
    await using (db)
    {
        await SchemaGuard.ValidateAsync(db, LoadAssembly(), Option("namespace"), CancellationToken.None);
        Console.WriteLine("valid");
        return 0;
    }
}

async Task<int> SnapshotAsync()
{
    if (Option("out") is not { } outDir)
    {
        return Fail("snapshot requires --out <MigrationsDir>");
    }

    var (db, runner) = await OpenAsync();
    await using (db)
    {
        // Last version touching each object, from the code-side plan (any state).
        var perObject = (await runner.StatusAsync(CancellationToken.None))
            .GroupBy(e => e.ObjectName)
            .ToDictionary(g => g.Key, g => g.Max(e => e.Version));

        var generatedAt = DateTimeOffset.UtcNow;
        foreach (var type in MappedTypes(LoadAssembly()))
        {
            var map = db.Maps.Load(type);
            if (map.RelationName is null || !perObject.TryGetValue(map.RelationName, out var version))
            {
                continue;
            }

            // Tables snapshot by columns; views by DDL (ADR-0017 add.1). Kinds the
            // dialect cannot create (MV/procedure here) have no history to record.
            var (folder, content) = SnapshotContent(db.Options.Dialect, type, map, version, generatedAt);
            if (content is null)
            {
                continue;
            }

            var directory = Path.Combine(outDir, folder, type.Name);
            Directory.CreateDirectory(directory);
            var file = Path.Combine(directory, $"V{version:0000}.schema.json");
            File.WriteAllText(file, content + "\n");
            Console.WriteLine("wrote " + file);
        }

        return 0;
    }
}

static (string Folder, string? Content) SnapshotContent(
    IDialect dialect, Type type, EntityMap map, long version, DateTimeOffset generatedAt)
{
    switch (map.Kind)
    {
        case RelationKind.Table:
            return ("Table", SchemaSnapshot.Export(map, dialect, version, generatedAt));
        case RelationKind.View:
            return ("View", SchemaSnapshot.ExportDdl(
                map.RelationName!, "view", dialect.CreateViewSql(map), version, generatedAt));
        case RelationKind.MaterializedView when dialect.SupportsMaterializedViews:
            return ("MaterializedView", SchemaSnapshot.ExportDdl(
                map.RelationName!, "materialized_view", dialect.CreateViewSql(map), version, generatedAt));
        default:
            return (string.Empty, null);
    }
}

int DiffCommand()
{
    if (Option("out") is not { } outDir)
    {
        return Fail("diff requires --out <MigrationsDir>");
    }

    if (Option("namespace") is not { } rootNamespace)
    {
        return Fail("diff requires --namespace <Migrations root namespace> (used for the emitted code)");
    }

    var assembly = LoadAssembly();
    var dialect = DialectFor();
    var loader = new EntityMapLoader();

    // Renames are declared, never inferred: --rename <table>.<old>=<new>, repeatable.
    var renames = new Dictionary<string, Dictionary<string, string>>(StringComparer.OrdinalIgnoreCase);
    foreach (var raw in options.TryGetValue("rename", out var declared) ? declared : [])
    {
        var dot = raw.IndexOf('.');
        var eq = raw.IndexOf('=');
        if (dot <= 0 || eq <= dot + 1 || eq == raw.Length - 1)
        {
            return Fail($"--rename '{raw}': expected <table>.<oldColumn>=<newColumn>");
        }

        if (!renames.TryGetValue(raw[..dot], out var perTable))
        {
            renames[raw[..dot]] = perTable = new Dictionary<string, string>(StringComparer.OrdinalIgnoreCase);
        }

        perTable[raw[(dot + 1)..eq]] = raw[(eq + 1)..];
    }

    var nextVersion = 1L + assembly.GetTypes()
        .Where(t => !t.IsAbstract && typeof(MigrationVersion).IsAssignableFrom(t)
            && t.GetConstructor(Type.EmptyTypes) is not null
            && (t.Namespace ?? string.Empty).StartsWith(rootNamespace, StringComparison.Ordinal))
        .Select(t => ((MigrationVersion)Activator.CreateInstance(t)!).Version)
        .DefaultIfEmpty(0)
        .Max();

    var changed = new List<(Type Type, EntityMap Map, MigrationGenerator.TableDiff Diff)>();
    var viewChanges = new List<(Type Type, string Folder, string ObjectName, string Ddl, string? PreviousDdl)>();
    var problems = new List<string>();
    var removals = new List<string>();
    foreach (var type in MappedTypes(assembly).OrderBy(t => t.Name, StringComparer.Ordinal))
    {
        var map = loader.Load(type);
        if (map.Kind == RelationKind.Table)
        {
            var snapshotDir = Path.Combine(outDir, "Table", type.Name);
            var latest = Directory.Exists(snapshotDir)
                ? Directory.GetFiles(snapshotDir, "V*.schema.json")
                    .Select(f => SchemaSnapshot.Parse(File.ReadAllText(f)))
                    .OrderByDescending(s => s.AsOfVersion)
                    .Select(s => s.Schema)
                    .FirstOrDefault()
                : null;

            renames.TryGetValue(map.RelationName!, out var tableRenames);
            var diff = MigrationGenerator.Diff(map, dialect, latest, tableRenames ?? new Dictionary<string, string>());
            problems.AddRange(diff.Unsupported.Select(m => $"{map.RelationName}: {m}"));
            removals.AddRange(diff.Removed.Select(c => $"{map.RelationName}.{c.Name}")
                .Concat(diff.RemovedIndexNames.Select(n => $"index {n}")));
            if (diff.HasChanges)
            {
                changed.Add((type, map, diff));
            }
        }
        else if (map.Kind == RelationKind.View
            || (map.Kind == RelationKind.MaterializedView && dialect.SupportsMaterializedViews))
        {
            // Views diff by DDL (ADR-0017 add.1): the definition is the schema.
            var folder = map.Kind == RelationKind.View ? "View" : "MaterializedView";
            var current = SchemaSnapshot.NormalizeDdl(dialect.CreateViewSql(map));
            var snapshotDir = Path.Combine(outDir, folder, type.Name);
            var previous = Directory.Exists(snapshotDir)
                ? Directory.GetFiles(snapshotDir, "V*.schema.json")
                    .Select(f => SchemaSnapshot.ParseDdl(File.ReadAllText(f)))
                    .OrderByDescending(s => s.AsOfVersion)
                    .Select(s => (string?)s.Ddl)
                    .FirstOrDefault()
                : null;
            if (previous != current)
            {
                viewChanges.Add((type, folder, map.RelationName!, current, previous));
            }
        }
    }

    if (problems.Count > 0)
    {
        foreach (var problem in problems)
        {
            Console.Error.WriteLine("DDL-004 " + problem);
        }

        return 1;
    }

    if (removals.Count > 0 && !Flag("allow-remove"))
    {
        return Fail("DDL-003 destructive changes need --allow-remove: " + string.Join(", ", removals));
    }

    if (changed.Count == 0 && viewChanges.Count == 0)
    {
        Console.WriteLine("no schema changes: the model matches the snapshots");
        return 0;
    }

    // New tables first, FK-referenced before referencing; then modified tables by name.
    var newSet = changed.Where(c => c.Diff.IsNew).Select(c => c.Type).ToHashSet();
    var ordered = TopologicalByForeignKey(changed.Where(c => c.Diff.IsNew).ToList(), newSet)
        .Concat(changed.Where(c => !c.Diff.IsNew))
        .ToList();

    var description = Option("name") ?? "Auto";
    var written = new List<string>();
    var stepRefs = new List<string>();
    foreach (var (type, map, diff) in ordered)
    {
        var directory = Path.Combine(outDir, "Table", type.Name);
        Directory.CreateDirectory(directory);
        var file = Path.Combine(directory, $"V{nextVersion:0000}_{description}.cs");
        if (File.Exists(file))
        {
            return Fail($"refusing to overwrite {file}");
        }

        File.WriteAllText(file, MigrationGenerator.EmitTableStep(
            rootNamespace, type, map, dialect, nextVersion, description, diff));
        written.Add(file);
        stepRefs.Add($"Table.{type.Name}.V{nextVersion:0000}_{description}");
    }

    // Views compose after tables (§7.22 ordering) — literal DDL with the
    // expected-previous-definition guard (MIG-012 on outside drift).
    foreach (var (type, folder, objectName, ddl, previousDdl) in viewChanges)
    {
        var directory = Path.Combine(outDir, folder, type.Name);
        Directory.CreateDirectory(directory);
        var file = Path.Combine(directory, $"V{nextVersion:0000}_{description}.cs");
        if (File.Exists(file))
        {
            return Fail($"refusing to overwrite {file}");
        }

        File.WriteAllText(file, MigrationGenerator.EmitViewStep(
            rootNamespace, type, folder, objectName, nextVersion, description, ddl, previousDdl));
        written.Add(file);
        stepRefs.Add($"{folder}.{type.Name}.V{nextVersion:0000}_{description}");
    }

    var rootFile = Path.Combine(outDir, $"V{nextVersion:0000}.cs");
    if (File.Exists(rootFile))
    {
        return Fail($"refusing to overwrite {rootFile}");
    }

    File.WriteAllText(rootFile, MigrationGenerator.EmitRoot(rootNamespace, nextVersion, stepRefs));
    written.Add(rootFile);

    foreach (var file in written)
    {
        Console.WriteLine("wrote " + file);
    }

    Console.WriteLine(
        $"review the generated V{nextVersion:0000}, build, migrate, then refresh snapshots: simpleorm snapshot --out <MigrationsDir>");
    return 0;
}

static List<(Type Type, EntityMap Map, MigrationGenerator.TableDiff Diff)> TopologicalByForeignKey(
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

async Task<int> ShadowAsync()
{
    if (Option("dialect") is { } dialectName && !string.Equals(dialectName, "sqlite", StringComparison.OrdinalIgnoreCase))
    {
        return Fail($"shadow replays into a throwaway SQLite database and supports only --dialect sqlite (ADR-0024); '{dialectName}' rollbacks need Down() overrides until snapshot tooling learns that dialect");
    }

    if (Option("out") is not { } outDir)
    {
        return Fail("shadow requires --out <MigrationsDir>");
    }

    long from = 0;
    if (Option("from") is { } fromRaw && !TryParseVersion(fromRaw, out from))
    {
        return Fail($"--from '{fromRaw}': expected V000N or a number");
    }

    var to = long.MaxValue;
    if (Option("to") is { } toRaw && !TryParseVersion(toRaw, out to))
    {
        return Fail($"--to '{toRaw}': expected V000M or a number");
    }

    var result = await SqliteShadow.RebuildSnapshotsAsync(
        LoadAssembly(), Option("namespace"), outDir, from, to, CancellationToken.None);
    foreach (var note in result.Notes)
    {
        Console.WriteLine(note);
    }

    foreach (var file in result.WrittenFiles)
    {
        Console.WriteLine("wrote " + file);
    }

    Console.WriteLine(result.WrittenFiles.Count == 0 ? "nothing to regenerate" : $"regenerated {result.WrittenFiles.Count} snapshot(s)");
    return 0;
}

static bool TryParseVersion(string raw, out long version)
    => long.TryParse(
        raw.StartsWith('V') || raw.StartsWith('v') ? raw[1..] : raw,
        out version);

int ExportMetadata()
{
    var assembly = LoadAssembly();
    var loader = new EntityMapLoader();
    var convention = SnakeCaseNamingConvention.Instance;
    var outDir = Option("out");
    if (outDir is not null)
    {
        Directory.CreateDirectory(outDir);
    }

    foreach (var type in MappedTypes(assembly))
    {
        var json = EntityMapJson.Export(loader.Load(type));
        if (outDir is null)
        {
            Console.WriteLine(json);
        }
        else
        {
            var file = Path.Combine(outDir, convention.TableName(type.Name) + ".json");
            File.WriteAllText(file, json + "\n");
            Console.WriteLine("wrote " + file);
        }
    }

    return 0;
}
