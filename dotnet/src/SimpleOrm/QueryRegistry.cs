using System.Reflection;
using System.Runtime.CompilerServices;

namespace SimpleOrm;

/// <summary>
/// Factories for registry entries (§6). <see cref="Embedded"/> binds a .sql embedded
/// resource under <c>Sql/</c> in the calling assembly; <see cref="Inline"/> is the
/// explicit escape hatch. Both convert implicitly to <see cref="Query{TArgs, TResult}"/>
/// and <see cref="Command{TArgs}"/>, so the declaration site names the types once:
/// <c>public static readonly Query&lt;Args, Row&gt; X = Query.Embedded("Dir/X.sql");</c>
/// </summary>
public static class Query
{
    [MethodImpl(MethodImplOptions.NoInlining)]
    public static SqlSource Embedded(string path) => SqlSource.Embedded(path, Assembly.GetCallingAssembly());

    public static SqlSource Inline(string sql) => SqlSource.Inline(sql);
}

/// <summary>Where a registry entry's SQL comes from; resolves lazily and caches.</summary>
public sealed class SqlSource
{
    private readonly Lazy<string> _sql;
    private readonly string _description;

    private SqlSource(string description, Func<string> factory)
    {
        _description = description;
        _sql = new Lazy<string>(factory);
    }

    /// <summary>The resolved SQL text (<c>QRY-003</c> when an embedded resource is missing).</summary>
    public string Sql => _sql.Value;

    /// <summary>The query's name in error messages: the resource path, or a SQL prefix for inline sources.</summary>
    public string Description => _description;

    internal static SqlSource Embedded(string path, Assembly assembly)
        => new(path, () =>
        {
            var suffix = "." + path.Replace('/', '.').Replace('\\', '.');
            var resource = assembly.GetManifestResourceNames()
                .FirstOrDefault(n => n.EndsWith(suffix, StringComparison.OrdinalIgnoreCase));
            if (resource is null)
            {
                throw new SimpleOrmException(
                    "QRY-003", path, $"no embedded resource ending in '{suffix}' in {assembly.GetName().Name}");
            }

            using var stream = assembly.GetManifestResourceStream(resource)!;
            using var reader = new StreamReader(stream);
            return reader.ReadToEnd();
        });

    internal static SqlSource Inline(string sql)
    {
        var head = sql.Length <= 60 ? sql : sql.Substring(0, 60) + "…";
        return new SqlSource("inline: " + string.Join(" ", head.Split((char[]?)null, StringSplitOptions.RemoveEmptyEntries)), () => sql);
    }
}

/// <summary>A registered query: SQL bound to its args and result types.</summary>
public sealed class Query<TArgs, TResult>
{
    private Query(SqlSource source) => Source = source;

    public SqlSource Source { get; }

    public static implicit operator Query<TArgs, TResult>(SqlSource source) => new(source);
}

/// <summary>A registered command: SQL bound to its args type; returns affected rows.</summary>
public sealed class Command<TArgs>
{
    private Command(SqlSource source) => Source = source;

    public SqlSource Source { get; }

    public static implicit operator Command<TArgs>(SqlSource source) => new(source);
}
