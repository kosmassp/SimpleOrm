using System.Diagnostics;
using System.Reflection;
using System.Runtime.Loader;
using SimpleOrm.Cli;
using Xunit;

namespace SimpleOrm.Tests;

/// <summary>
/// ADR-0049: the CLI loads <c>--assembly</c> into a load context of its own. The fixture
/// (tests/NewerDependencyApp) is built against Microsoft.Extensions.Options 10 while this
/// process, like the CLI, carries 9.x through Microsoft.Data.SqlClient and Npgsql.
/// </summary>
public sealed class CliLoadContextTests : IDisposable
{
    private static readonly Version NewerOptions = new(10, 0, 0, 0);

    private readonly string _temp = Path.Combine(Path.GetTempPath(), "simpleorm-alc-" + Guid.NewGuid().ToString("N"));

    public CliLoadContextTests() => Directory.CreateDirectory(_temp);

    public void Dispose()
    {
        try
        {
            Directory.Delete(_temp, recursive: true);
        }
        catch (IOException)
        {
            // A file the load context still maps (Windows); the temp folder is the OS's to clear.
        }
        catch (UnauthorizedAccessException)
        {
        }
    }

    [Fact]
    public void This_process_carries_an_older_options_than_the_fixture()
    {
        // The premise of every test here, as it is the CLI's: Options 9 is what the default context
        // binds. (Out of the CLI, the xunit host's own assembly resolver would find Options 10 in the
        // package folder and hide a collision, so the failing side is proved through the CLI process.)
        var options = DefaultContextOptionsVersion();
        Assert.NotNull(options);
        Assert.True(options < NewerOptions, $"this process must carry an older Options than the fixture, has {options}");
    }

    [Fact]
    public void The_target_loads_in_its_own_context_with_its_newer_package()
    {
        var assembly = TargetLoadContext.LoadTarget(FixturePath());

        Assert.IsType<TargetLoadContext>(AssemblyLoadContext.GetLoadContext(assembly));
        var setup = assembly.GetType("NewerDependencyApp.GadgetSettingsSetup", throwOnError: true)!;
        var options = setup.GetInterfaces().Single().Assembly;
        Assert.Equal("Microsoft.Extensions.Options", options.GetName().Name);
        Assert.Equal(NewerOptions, options.GetName().Version);
        Assert.Same(AssemblyLoadContext.GetLoadContext(assembly), AssemblyLoadContext.GetLoadContext(options));

        // A class library's output holds no package assemblies: this one came from the package folder.
        Assert.False(File.Exists(Path.Combine(Path.GetDirectoryName(FixturePath())!, "Microsoft.Extensions.Options.dll")));
        Assert.Contains("microsoft.extensions.options", options.Location.Replace('\\', '/').ToLowerInvariant());
    }

    [Fact]
    public void SimpleOrm_types_are_the_clis_so_entities_and_migrations_scan()
    {
        var assembly = TargetLoadContext.LoadTarget(FixturePath());

        Assert.Contains(assembly.GetReferencedAssemblies(), n => n.Name == "SimpleOrm");
        var entities = EntityMapLoader.ScannedEntityTypes(assembly.GetExportedTypes()).ToArray();
        var gadget = Assert.Single(entities);
        Assert.Equal("Gadget", gadget.Name);
        Assert.Equal("gadgets", new EntityMapLoader().Load(gadget).RelationName);

        var version = assembly.GetType("NewerDependencyApp.Migrations.V0001", throwOnError: true)!;
        Assert.Equal(typeof(MigrationVersion), version.BaseType);
        Assert.Same(typeof(Db).Assembly, version.BaseType!.Assembly);
    }

    [Fact]
    public void A_copy_next_to_the_application_wins_over_the_package_folder()
    {
        var packaged = TargetLoadContext.LoadTarget(FixturePath())
            .GetType("NewerDependencyApp.GadgetSettingsSetup", throwOnError: true)!
            .GetInterfaces().Single().Assembly.Location;

        CopyFixtureTo(_temp);
        File.Copy(packaged, Path.Combine(_temp, Path.GetFileName(packaged)));

        var options = TargetLoadContext.LoadTarget(Path.Combine(_temp, "NewerDependencyApp.dll"))
            .GetType("NewerDependencyApp.GadgetSettingsSetup", throwOnError: true)!
            .GetInterfaces().Single().Assembly;
        Assert.Equal(Path.Combine(_temp, "Microsoft.Extensions.Options.dll"), options.Location);
        Assert.Equal(NewerOptions, options.GetName().Version);
    }

    [Fact]
    public async Task A_dependency_only_the_cli_could_supply_refuses_coded_and_names_it()
    {
        // No deps.json and no package beside it: the CLI's own Options 9 is all there is — the
        // collision the pre-ADR-0049 CLI died of, unhandled. Now one coded line, exit 1.
        CopyFixtureTo(_temp);
        File.Delete(Path.Combine(_temp, "NewerDependencyApp.deps.json"));

        var (exit, output, errors) = await RunCli("export-metadata", "--assembly", Path.Combine(_temp, "NewerDependencyApp.dll"));
        Assert.Equal(1, exit);
        Assert.Equal(string.Empty, output);
        Assert.StartsWith("CLI --assembly: ", errors);
        Assert.Contains("Microsoft.Extensions.Options, Version=10.0.0.0", errors);
        Assert.DoesNotContain("   at ", errors);
    }

    [Fact]
    public void A_missing_assembly_refuses_coded()
    {
        var exception = Assert.Throws<SimpleOrmException>(
            () => TargetLoadContext.LoadTarget(Path.Combine(_temp, "Nowhere.dll")));
        Assert.Equal("CLI", exception.Code);
        Assert.Contains("no such file", exception.Message);
    }

    [Fact]
    public async Task Every_assembly_command_of_the_built_cli_runs_against_the_newer_application()
    {
        var db = Path.Combine(_temp, "app.db");
        var migrations = Path.Combine(_temp, "Migrations");
        var shadow = Path.Combine(_temp, "Shadow");
        var assembly = FixturePath();
        const string ns = "NewerDependencyApp.Migrations";

        Assert.Contains("applied 1 version(s)", await Cli("migrate", "--assembly", assembly, "--namespace", ns, "--db", db));
        Assert.Contains("Applied", await Cli("status", "--assembly", assembly, "--namespace", ns, "--db", db));
        Assert.Contains("valid", await Cli("validate", "--assembly", assembly, "--namespace", ns, "--db", db));
        Assert.Contains("Gadget", await Cli("snapshot", "--assembly", assembly, "--namespace", ns, "--db", db, "--out", migrations));
        Assert.Contains("no schema changes", await Cli("diff", "--assembly", assembly, "--namespace", ns, "--out", migrations));
        Assert.Contains("Gadget", await Cli("shadow", "--assembly", assembly, "--namespace", ns, "--out", shadow));
        Assert.Contains("\"gadgets\"", await Cli("export-metadata", "--assembly", assembly));

        Assert.Equal(
            File.ReadAllText(Path.Combine(migrations, "Table", "Gadget", "V0001.schema.json")).Split('\n').Where(l => !l.Contains("generatedAt")),
            File.ReadAllText(Path.Combine(shadow, "Table", "Gadget", "V0001.schema.json")).Split('\n').Where(l => !l.Contains("generatedAt")));
    }

    private static async Task<string> Cli(params string[] arguments)
    {
        var (exit, output, errors) = await RunCli(arguments);
        Assert.True(exit == 0, $"simpleorm {string.Join(' ', arguments)} exited {exit}:\n{output}\n{errors}");
        Assert.Equal(string.Empty, errors);
        return output;
    }

    private static async Task<(int Exit, string Output, string Errors)> RunCli(params string[] arguments)
    {
        var start = new ProcessStartInfo(DotnetHost())
        {
            RedirectStandardOutput = true,
            RedirectStandardError = true,
            UseShellExecute = false,
        };
        start.ArgumentList.Add(CliPath());
        foreach (var argument in arguments)
        {
            start.ArgumentList.Add(argument);
        }

        using var process = Process.Start(start)!;
        var stdout = process.StandardOutput.ReadToEndAsync();
        var stderr = process.StandardError.ReadToEndAsync();
        await process.WaitForExitAsync();
        return (process.ExitCode, await stdout, await stderr);
    }

    private void CopyFixtureTo(string directory)
    {
        foreach (var file in Directory.GetFiles(Path.GetDirectoryName(FixturePath())!))
        {
            File.Copy(file, Path.Combine(directory, Path.GetFileName(file)));
        }
    }

    private static Version? DefaultContextOptionsVersion()
        => AppContext.GetData("TRUSTED_PLATFORM_ASSEMBLIES") is string list
            ? list.Split(Path.PathSeparator)
                .Where(p => Path.GetFileNameWithoutExtension(p) == "Microsoft.Extensions.Options")
                .Select(p => AssemblyName.GetAssemblyName(p).Version)
                .FirstOrDefault()
            : null;

    // The test's own bin folder is .../tests/SimpleOrm.Tests/bin/<Configuration>/net10.0/: the
    // CLI and the fixture are built beside it, in the same configuration.
    private static string Configuration
        => new DirectoryInfo(AppContext.BaseDirectory.TrimEnd(Path.DirectorySeparatorChar)).Parent!.Name;

    private static string DotnetRoot()
    {
        var directory = new DirectoryInfo(AppContext.BaseDirectory);
        while (directory is not null && !File.Exists(Path.Combine(directory.FullName, "SimpleOrm.sln")))
        {
            directory = directory.Parent;
        }

        return directory?.FullName ?? throw new InvalidOperationException("SimpleOrm.sln not found above " + AppContext.BaseDirectory);
    }

    private static string FixturePath()
        => Path.Combine(DotnetRoot(), "tests", "NewerDependencyApp", "bin", Configuration, "net10.0", "NewerDependencyApp.dll");

    private static string CliPath()
        => Path.Combine(DotnetRoot(), "src", "SimpleOrm.Cli", "bin", Configuration, "net10.0", "SimpleOrm.Cli.dll");

    // The host running the tests runs the CLI too (the SDK sets DOTNET_HOST_PATH under dotnet test).
    private static string DotnetHost()
        => Environment.GetEnvironmentVariable("DOTNET_HOST_PATH") is { Length: > 0 } host
            ? host
            : Environment.ProcessPath is { } self && Path.GetFileNameWithoutExtension(self) == "dotnet" ? self : "dotnet";
}
