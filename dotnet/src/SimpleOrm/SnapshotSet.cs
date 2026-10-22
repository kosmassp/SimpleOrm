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

    internal sealed class Entry(TableSchema? table, string? ddl, TriggerSnapshot? trigger = null, string dialect = SchemaSnapshot.DefaultDialect)
    {
        /// <summary>The dialect the document is stamped for (format v3, ADR-0043); unstamped files are SQLite's.</summary>
        public string Dialect { get; } = dialect;

        /// <summary>The table shape — null for a DDL-shaped (view/MV/procedure) or a trigger snapshot.</summary>
        public TableSchema? Table { get; } = table;

        /// <summary>The normalized defining DDL — null for a table or a trigger snapshot.</summary>
        public string? Ddl { get; } = ddl;

        /// <summary>The trigger declaration (ADR-0042) — null for the other kinds.</summary>
        public TriggerSnapshot? Trigger { get; } = trigger;
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
        bool isTrigger;
        string dialect;
        using (var document = JsonDocument.Parse(json))
        {
            objectName = document.RootElement.GetProperty("object").GetString()!;
            version = document.RootElement.GetProperty("asOfVersion").GetInt64();
            isDdl = document.RootElement.TryGetProperty("ddl", out _);
            isTrigger = document.RootElement.TryGetProperty("kind", out var kind) && kind.GetString() == "trigger";
            dialect = SchemaSnapshot.DialectOf(document.RootElement);
        }

        var entry = isTrigger
            ? new Entry(table: null, ddl: null, SchemaSnapshot.ParseTrigger(json).Snapshot, dialect)
            : isDdl
                ? new Entry(table: null, SchemaSnapshot.ParseDdl(json).Ddl, dialect: dialect)
                : new Entry(SchemaSnapshot.Parse(json).Schema, ddl: null, dialect: dialect);
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
