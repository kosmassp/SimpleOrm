using System.Reflection;
using System.Runtime.Loader;
using System.Text.Json;
using SimpleOrm.Postgres;
using SimpleOrm.Sqlite;
using SimpleOrm.SqlServer;

namespace SimpleOrm.Cli;

/// <summary>
/// The application assembly <c>--assembly</c> names, loaded into a load context of its own
/// (ADR-0049) so its dependencies resolve from its own <c>*.deps.json</c> instead of colliding
/// with the CLI's: an application built against a newer package than the CLI carries (say
/// Microsoft.Extensions.Options 10 against the CLI's 9) loads without a
/// <see cref="FileLoadException"/>.
/// <para>
/// Each dependency the application asks for is decided once, in this order: SimpleOrm's own
/// assemblies are always the CLI's, so attributes, <see cref="EntityMap"/>, migrations, and
/// dialect types are one type on both sides; an assembly the CLI already carries at the same
/// or a newer version is the CLI's too (framework and driver types stay shared, as they were
/// under <c>Assembly.LoadFrom</c>); anything else is the application's own copy — from its
/// output folder or, for a package a class library's build does not copy there, from the
/// NuGet package folder its <c>deps.json</c> names.
/// </para>
/// </summary>
public sealed class TargetLoadContext : AssemblyLoadContext
{
    // SimpleOrm's assemblies, exactly as the CLI carries them: the application must see these
    // types, never copies. Named by type, not by prefix, so an application called
    // SimpleOrm.Something is still the application's.
    private static readonly Dictionary<string, Assembly> SharedSimpleOrm =
        new[] { typeof(Db), typeof(SqliteDialect), typeof(SqlServerDialect), typeof(PostgresDialect) }
            .Select(t => t.Assembly)
            .Distinct()
            .ToDictionary(a => a.GetName().Name!, StringComparer.OrdinalIgnoreCase);

    private static readonly Lazy<Dictionary<string, string>> CliAssemblies = new(TrustedPlatformAssemblies);

    private readonly AssemblyDependencyResolver? _resolver;
    private readonly Dictionary<string, List<string>> _packageAssets;

    private TargetLoadContext(string path)
        : base("simpleorm-target:" + Path.GetFileName(path))
    {
        try
        {
            _resolver = new AssemblyDependencyResolver(path);
        }
        catch (InvalidOperationException)
        {
            // No host to resolve with (a single-file CLI build): the deps.json reader below still serves.
            _resolver = null;
        }

        _packageAssets = PackageAssets(path);
    }

    /// <summary>
    /// Loads <paramref name="path"/> in a new context and loads its types eagerly, so a dependency
    /// that cannot be found or loaded refuses here, coded, rather than as a stack trace from the
    /// first command that scans the assembly.
    /// </summary>
    public static Assembly LoadTarget(string path)
    {
        var fullPath = Path.GetFullPath(path);
        if (!File.Exists(fullPath))
        {
            throw new SimpleOrmException("CLI", "--assembly", $"{fullPath}: no such file");
        }

        Assembly assembly;
        try
        {
            assembly = new TargetLoadContext(fullPath).LoadFromAssemblyPath(fullPath);
        }
        catch (BadImageFormatException exception)
        {
            throw new SimpleOrmException("CLI", "--assembly", $"{fullPath}: not a .NET assembly ({exception.Message})");
        }

        try
        {
            _ = assembly.GetTypes();
        }
        catch (ReflectionTypeLoadException exception)
        {
            var cause = exception.LoaderExceptions.FirstOrDefault(e => e is FileLoadException or FileNotFoundException)
                ?? exception.LoaderExceptions.FirstOrDefault(e => e is not null)
                ?? exception;
            throw new SimpleOrmException("CLI", "--assembly", $"{fullPath}: a dependency does not load: {cause.Message.Trim()}");
        }
        catch (Exception exception) when (exception is FileLoadException or FileNotFoundException)
        {
            throw new SimpleOrmException("CLI", "--assembly", $"{fullPath}: a dependency does not load: {exception.Message.Trim()}");
        }

        return assembly;
    }

    protected override Assembly? Load(AssemblyName assemblyName)
    {
        var name = assemblyName.Name;
        if (name is null)
        {
            return null;
        }

        if (SharedSimpleOrm.TryGetValue(name, out var simpleOrm))
        {
            return simpleOrm;
        }

        // The CLI's copy when it is at least as new as the application asks: one type on both sides.
        if (CliAssemblies.Value.TryGetValue(name, out var cliPath) && CliCopySatisfies(cliPath, assemblyName.Version))
        {
            return null;
        }

        var ownPath = _resolver?.ResolveAssemblyToPath(assemblyName) ?? PackageAssetPath(name);
        return ownPath is null ? null : LoadFromAssemblyPath(ownPath);
    }

    protected override IntPtr LoadUnmanagedDll(string unmanagedDllName)
        => _resolver?.ResolveUnmanagedDllToPath(unmanagedDllName) is { } path
            ? LoadUnmanagedDllFromPath(path)
            : IntPtr.Zero;

    private static bool CliCopySatisfies(string cliPath, Version? requested)
    {
        if (requested is null)
        {
            return true;
        }

        try
        {
            return AssemblyName.GetAssemblyName(cliPath).Version is { } carried && carried >= requested;
        }
        catch (Exception exception) when (exception is IOException or BadImageFormatException)
        {
            return false;
        }
    }

    private static Dictionary<string, string> TrustedPlatformAssemblies()
    {
        var map = new Dictionary<string, string>(StringComparer.OrdinalIgnoreCase);
        if (AppContext.GetData("TRUSTED_PLATFORM_ASSEMBLIES") is string list)
        {
            foreach (var path in list.Split(Path.PathSeparator, StringSplitOptions.RemoveEmptyEntries))
            {
                map.TryAdd(Path.GetFileNameWithoutExtension(path), path);
            }
        }

        return map;
    }

    private string? PackageAssetPath(string name)
        => _packageAssets.TryGetValue(name, out var candidates) ? candidates.FirstOrDefault(File.Exists) : null;

    // The runtime assets of every package library in the application's deps.json, by assembly
    // name, as candidate paths: next to the application first, then under each NuGet package
    // root. A class library's build output (the usual --assembly) holds its project references
    // but not its packages; the resolver only probes the output folder, so this finds the rest
    // where restore put them.
    private static Dictionary<string, List<string>> PackageAssets(string path)
    {
        var assets = new Dictionary<string, List<string>>(StringComparer.OrdinalIgnoreCase);
        var directory = Path.GetDirectoryName(path)!;
        var depsFile = Path.Combine(directory, Path.GetFileNameWithoutExtension(path) + ".deps.json");
        if (!File.Exists(depsFile))
        {
            return assets;
        }

        JsonDocument document;
        try
        {
            document = JsonDocument.Parse(File.ReadAllText(depsFile));
        }
        catch (JsonException)
        {
            return assets;
        }

        using (document)
        {
            var root = document.RootElement;
            if (!root.TryGetProperty("runtimeTarget", out var runtimeTarget)
                || !runtimeTarget.TryGetProperty("name", out var targetName)
                || !root.TryGetProperty("targets", out var targets)
                || !targets.TryGetProperty(targetName.GetString() ?? string.Empty, out var target)
                || !root.TryGetProperty("libraries", out var libraries))
            {
                return assets;
            }

            var packageRoots = PackageRoots();
            foreach (var library in target.EnumerateObject())
            {
                if (!library.Value.TryGetProperty("runtime", out var runtime)
                    || !libraries.TryGetProperty(library.Name, out var description)
                    || !description.TryGetProperty("type", out var type)
                    || type.GetString() != "package")
                {
                    continue;
                }

                var packagePath = description.TryGetProperty("path", out var declared) && declared.GetString() is { } p
                    ? p
                    : library.Name.ToLowerInvariant();
                foreach (var asset in runtime.EnumerateObject())
                {
                    var relative = asset.Name.Replace('/', Path.DirectorySeparatorChar);
                    var candidates = new List<string> { Path.Combine(directory, Path.GetFileName(relative)) };
                    candidates.AddRange(packageRoots.Select(r => Path.Combine(r, packagePath, relative)));
                    assets.TryAdd(Path.GetFileNameWithoutExtension(relative), candidates);
                }
            }
        }

        return assets;
    }

    // NUGET_PACKAGES when set (restore honours it too), else the per-user default folder.
    private static List<string> PackageRoots()
    {
        var roots = new List<string>();
        if (Environment.GetEnvironmentVariable("NUGET_PACKAGES") is { Length: > 0 } configured)
        {
            roots.Add(configured);
        }

        var home = Environment.GetFolderPath(Environment.SpecialFolder.UserProfile);
        if (home.Length > 0)
        {
            roots.Add(Path.Combine(home, ".nuget", "packages"));
        }

        return roots;
    }
}
