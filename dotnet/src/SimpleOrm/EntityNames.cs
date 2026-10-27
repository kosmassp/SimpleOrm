using System.Runtime.CompilerServices;

namespace SimpleOrm;

/// <summary>
/// The names and error labels the session derives from an entity type's name —
/// the mapping plan's query name, the statement log's line name, the converter's
/// label of a key part or a criteria parameter — built once per type instead of
/// concatenated on every call (ADR-0048). The strings are exactly the ones the
/// call sites built before. Keyed by the <see cref="Type"/> instance itself
/// (a <see cref="ConditionalWeakTable{TKey, TValue}"/>, so a collectible type
/// is not held); safe across threads.
/// </summary>
internal sealed class EntityNames
{
    private static readonly ConditionalWeakTable<Type, EntityNames> ByType = new();

    private readonly string _name;
    private string[] _keyLabels = [];
    private string[] _criteriaLabels = [];

    private EntityNames(Type type)
    {
        _name = type.Name;
        Criteria = _name + " criteria";
        CriteriaLog = _name + ".criteria";
        Get = _name + " get";
        GetLog = _name + ".get";
        SelectAll = _name + " select-all";
        SelectAllLog = _name + ".selectAll";
        Include = _name + ".include";
        Insert = _name + ".insert";
        Update = _name + ".update";
        UpdateOnly = _name + ".updateOnly";
        Delete = _name + ".delete";
    }

    public static EntityNames For(Type type) => ByType.GetValue(type, static t => new EntityNames(t));

    /// <summary>A criteria query's name (mapping plan, errors): <c>User criteria</c>.</summary>
    public string Criteria { get; }

    public string CriteriaLog { get; }

    public string Get { get; }

    public string GetLog { get; }

    public string SelectAll { get; }

    public string SelectAllLog { get; }

    public string Include { get; }

    public string Insert { get; }

    public string Update { get; }

    public string UpdateOnly { get; }

    public string Delete { get; }

    /// <summary>A generated update's log name by its operation (<c>update</c>, <c>updateOnly</c>, <c>delete</c> — a soft delete); anything else concatenated as before.</summary>
    public string WriteLog(string operation) => operation switch
    {
        "update" => Update,
        "updateOnly" => UpdateOnly,
        "delete" => Delete,
        "insert" => Insert,
        _ => _name + "." + operation,
    };

    /// <summary>The converter's label of key part <paramref name="index"/>: <c>User key[0]</c>.</summary>
    public string KeyLabel(int index) => Label(ref _keyLabels, index, static (name, i) => $"{name} key[{i}]");

    /// <summary>The converter's label of criteria parameter <paramref name="index"/>: <c>User criteria @c0</c>.</summary>
    public string CriteriaLabel(int index) => Label(ref _criteriaLabels, index, static (name, i) => name + " criteria " + CriteriaParameterNames.Of(i));

    private string Label(ref string[] labels, int index, Func<string, int, string> build)
    {
        var current = Volatile.Read(ref labels);
        if (index < current.Length)
        {
            return current[index];
        }

        if (index >= 1024)
        {
            return build(_name, index);   // an unusually long list: built per call, never cached
        }

        lock (this)
        {
            current = labels;
            if (index >= current.Length)
            {
                var grown = new string[Math.Max(index + 1, Math.Min(1024, current.Length * 2))];
                Array.Copy(current, grown, current.Length);
                for (var i = current.Length; i < grown.Length; i++)
                {
                    grown[i] = build(_name, i);
                }

                Volatile.Write(ref labels, grown);
                current = grown;
            }

            return current[index];
        }
    }
}

/// <summary>The criteria renderer's parameter names, <c>@c0..@cN</c>, built once.</summary>
internal static class CriteriaParameterNames
{
    private static readonly string[] Cached = Enumerable.Range(0, 1024).Select(i => "@c" + i).ToArray();

    public static string Of(int index) => index < Cached.Length ? Cached[index] : "@c" + index;
}
