using System.Reflection;
using SimpleOrm;
using SimpleOrm.Sqlite;

// simpleorm CLI (§7.24): migration is always an explicit act — the application
// never migrates at startup. Migrations and entities are code, so the CLI loads
// the application assembly and works from its types.

var positional = new List<string>();
var options = new Dictionary<string, string>(StringComparer.OrdinalIgnoreCase);
for (var i = 0; i < args.Length; i++)
{
    if (args[i].StartsWith("--", StringComparison.Ordinal) && i + 1 < args.Length)
    {
        options[args[i][2..]] = args[++i];
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

        commands (all need --assembly <dll> and, except export-metadata, --db <connection or file>):
          migrate                     apply pending versions
          migrate down --to <N>       revert versions above N
          status                      list (version, object) states
          baseline --version <N>      record versions <= N without running them
          export-metadata [--out dir] write each entity's EntityMap JSON
          validate                    SchemaGuard: full report or exit 0
          snapshot --out <MigrationsDir>
                                      write V000N.schema.json per table (versioned, timestamped)

        options:
          --assembly <path>   the application assembly containing migrations/entities
          --db <value>        connection string, or a bare path to a SQLite file
          --namespace <ns>    migration namespace filter (default: whole assembly)
        """);
    return 2;
}

int Fail(string message)
{
    Console.Error.WriteLine(message);
    return 1;
}

Assembly LoadAssembly()
{
    if (!options.TryGetValue("assembly", out var path))
    {
        throw new SimpleOrmException("CLI", "arguments", "--assembly <path> is required");
    }

    return Assembly.LoadFrom(Path.GetFullPath(path));
}

async Task<(Db Db, MigrationRunner Runner)> OpenAsync()
{
    if (!options.TryGetValue("db", out var db))
    {
        throw new SimpleOrmException("CLI", "arguments", "--db <connection string or file> is required");
    }

    var connectionString = db.Contains('=') ? db : $"Data Source={db}";
    var session = await Db.OpenAsync(
        connectionString, new DbOptions { Dialect = new SqliteDialect() }, CancellationToken.None);
    options.TryGetValue("namespace", out var migrationsNamespace);
    return (session, new MigrationRunner(session, LoadAssembly(), migrationsNamespace));
}

async Task<int> MigrateAsync()
{
    var (db, runner) = await OpenAsync();
    await using (db)
    {
        var applied = await runner.MigrateAsync(CancellationToken.None);
        Console.WriteLine(applied == 0 ? "nothing pending" : $"applied {applied} version(s)");
        return 0;
    }
}

async Task<int> MigrateDownAsync()
{
    if (!options.TryGetValue("to", out var to) || !long.TryParse(to, out var target))
    {
        return Fail("migrate down requires --to <version>");
    }

    var (db, runner) = await OpenAsync();
    await using (db)
    {
        var reverted = await runner.MigrateDownAsync(target, CancellationToken.None);
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
    if (!options.TryGetValue("version", out var raw) || !long.TryParse(raw, out var version))
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
        options.TryGetValue("namespace", out var migrationsNamespace);
        await SchemaGuard.ValidateAsync(db, LoadAssembly(), migrationsNamespace, CancellationToken.None);
        Console.WriteLine("valid");
        return 0;
    }
}

async Task<int> SnapshotAsync()
{
    if (!options.TryGetValue("out", out var outDir))
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
        foreach (var type in LoadAssembly().GetExportedTypes()
            .Where(t => t is { IsClass: true, IsAbstract: false } && EntityMapLoader.HasMappingAttributes(t)))
        {
            var map = db.Maps.Load(type);
            if (map.Kind != RelationKind.Table || !perObject.TryGetValue(map.RelationName!, out var version))
            {
                continue;   // tables only — views/statements/procedures self-reflect (ADR-0013 add.3)
            }

            var directory = Path.Combine(outDir, "Table", type.Name);
            Directory.CreateDirectory(directory);
            var file = Path.Combine(directory, $"V{version:0000}.schema.json");
            File.WriteAllText(file, SchemaSnapshot.Export(map, version, generatedAt) + "\n");
            Console.WriteLine("wrote " + file);
        }

        return 0;
    }
}

int ExportMetadata()
{
    var assembly = LoadAssembly();
    var loader = new EntityMapLoader();
    var convention = SnakeCaseNamingConvention.Instance;
    options.TryGetValue("out", out var outDir);
    if (outDir is not null)
    {
        Directory.CreateDirectory(outDir);
    }

    foreach (var type in assembly.GetExportedTypes()
        .Where(t => t is { IsClass: true, IsAbstract: false } && EntityMapLoader.HasMappingAttributes(t)))
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
