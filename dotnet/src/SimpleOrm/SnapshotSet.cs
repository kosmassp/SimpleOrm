using System.Reflection;
using System.Text.Json;

namespace SimpleOrm;

/// <summary>
/// Every committed schema snapshot of a migrations assembly, indexed by
/// (object, version) — the history the runner derives rollbacks from (ADR-0018).
/// Snapshots load from the assembly's embedded resources (embed
/// <c>Migrations/**/*.schema.json</c>), or from a directory for dev workflows.
/// </summary>
public sealed class SnapshotSet
{
    private readonly Dictionary<string, SortedList<long, Entry>> _byObject =
        new(StringComparer.OrdinalIgnoreCase);

    internal sealed class Entry(TableSchema? table, string? ddl)
    {
        /// <summary>The table shape — null for a DDL-shaped (view/MV/procedure) snapshot.</summary>
        public TableSchema? Table { get; } = table;

        /// <summary>The normalized defining DDL — null for a table snapshot.</summary>
        public string? Ddl { get; } = ddl;
    }

    public static SnapshotSet FromAssembly(Assembly assembly)
    {
        var set = new SnapshotSet();
        foreach (var name in assembly.GetManifestResourceNames()
            .Where(n => n.EndsWith(".schema.json", StringComparison.OrdinalIgnoreCase)))
        {
            using var stream = assembly.GetManifestResourceStream(name)!;
            using var reader = new StreamReader(stream);
            set.Add(reader.ReadToEnd());
        }

        return set;
    }

    public static SnapshotSet FromDirectory(string directory)
    {
        var set = new SnapshotSet();
        if (Directory.Exists(directory))
        {
            foreach (var file in Directory.GetFiles(directory, "*.schema.json", SearchOption.AllDirectories))
            {
                set.Add(File.ReadAllText(file));
            }
        }

        return set;
    }

    public int Count => _byObject.Values.Sum(v => v.Count);

    private void Add(string json)
    {
        string objectName;
        long version;
        bool isDdl;
        using (var document = JsonDocument.Parse(json))
        {
            objectName = document.RootElement.GetProperty("object").GetString()!;
            version = document.RootElement.GetProperty("asOfVersion").GetInt64();
            isDdl = document.RootElement.TryGetProperty("ddl", out _);
        }

        var entry = isDdl
            ? new Entry(table: null, SchemaSnapshot.ParseDdl(json).Ddl)
            : new Entry(SchemaSnapshot.Parse(json).Schema, ddl: null);
        if (!_byObject.TryGetValue(objectName, out var versions))
        {
            _byObject[objectName] = versions = [];
        }

        versions[version] = entry;
    }

    /// <summary>The object's snapshot at exactly this version, or null when the version didn't touch it.</summary>
    internal Entry? At(string objectName, long version)
        => _byObject.TryGetValue(objectName, out var versions) && versions.TryGetValue(version, out var entry)
            ? entry
            : null;

    /// <summary>The object's latest snapshot strictly before the version — null means the version created it.</summary>
    internal Entry? LatestBefore(string objectName, long version)
    {
        if (!_byObject.TryGetValue(objectName, out var versions))
        {
            return null;
        }

        Entry? latest = null;
        foreach (var pair in versions)
        {
            if (pair.Key >= version)
            {
                break;
            }

            latest = pair.Value;
        }

        return latest;
    }
}
