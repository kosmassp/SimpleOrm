using System.Reflection;
using SimpleOrm;
using SimpleOrm.Cli;
using SimpleOrm.Postgres;
using SimpleOrm.Sqlite;
using SimpleOrm.SqlServer;

// simpleorm CLI (§7.24): migration is always an explicit act — the application
// never migrates at startup. Migrations and entities are code, so the CLI loads
// the application assembly and works from its types.

var boolFlags = new HashSet<string>(StringComparer.OrdinalIgnoreCase) { "force", "allow-delete", "allow-remove", "amend" };
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
        "diff" => await DiffAsync(),
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
               [--amend [--force] [--db <value>]]
                                      generate the next migration version from the model vs
                                      the latest snapshots (tables by columns, views by
                                      DDL); no database needed; removals need
                                      --allow-remove (DDL-003); inexpressible changes are
                                      DDL-004 (write by hand); a migrated object with no
                                      snapshot refuses (run shadow/snapshot first).
                                      --amend regenerates the newest version in place
                                      instead (baseline: the snapshots below it); a
                                      hand-written version is replaced only with --force
                                      (its raw SQL/hooks/data are not reproducible — re-add
                                      by hand); with --db, an applied draft gets a MIG-010
                                      heads-up (migrate down first, or recreate)
          shadow --out <MigrationsDir> [--from V000N] [--to V000M]
                                      rebuild snapshots by replaying migrations in a
                                      throwaway database; --from trusts version N as
                                      correct (baseline from committed snapshots, no
                                      verification below N) and regenerates only (N, M]

        options:
          --assembly <path>   the application assembly containing migrations/entities
          --db <value>        connection string, or a bare path to a SQLite file
          --namespace <ns>    migration namespace filter (default: whole assembly)
          --dialect <name>    sqlite (default), sqlserver, or postgres
                              (ADR-0024/0025); shadow is sqlite-only — other
                              dialects override Down() instead
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

// --dialect (ADR-0024/0025): sqlite stays the default; unknown names refuse loudly.
IDialect DialectFor() => (Option("dialect") ?? "sqlite").ToLowerInvariant() switch
{
    "sqlite" => (IDialect)new SqliteDialect(),
    "sqlserver" => new SqlServerDialect(),
    "postgres" => new PostgresDialect(),
    var name => throw new SimpleOrmException(
        "CLI", "arguments", $"unknown --dialect '{name}' (sqlite, sqlserver, postgres)"),
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

async Task<int> DiffAsync()
{
    if (Option("out") is not { } outDir)
    {
        return Fail("diff requires --out <MigrationsDir>");
    }

    if (Option("namespace") is not { } rootNamespace)
    {
        return Fail("diff requires --namespace <Migrations root namespace> (used for the emitted code)");
    }

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

    var assembly = LoadAssembly();
    return await DiffCommand.ExecuteAsync(
        new DiffOptions
        {
            Assembly = assembly,
            EntityTypes = MappedTypes(assembly).ToArray(),
            OutDir = outDir,
            RootNamespace = rootNamespace,
            Dialect = DialectFor(),
            DialectLabel = (Option("dialect") ?? "sqlite").ToLowerInvariant(),
            Name = Option("name"),
            Renames = renames.ToDictionary(
                p => p.Key, p => (IReadOnlyDictionary<string, string>)p.Value, StringComparer.OrdinalIgnoreCase),
            AllowRemove = Flag("allow-remove"),
            Amend = Flag("amend"),
            Force = Flag("force"),
            // --db is optional when amending: an applied draft gets a MIG-010 heads-up.
            IsApplied = Flag("amend") && Option("db") is not null ? IsAppliedAsync : null,
        },
        Console.Out,
        Console.Error);
}

async Task<bool> IsAppliedAsync(long version)
{
    var (db, runner) = await OpenAsync();
    await using (db)
    {
        return (await runner.StatusAsync(CancellationToken.None))
            .Any(e => e.Version == version && e.State is MigrationState.Applied or MigrationState.Drifted);
    }
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
        var json = EntityMapJson.Export(loader.Load(type), loader);
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
