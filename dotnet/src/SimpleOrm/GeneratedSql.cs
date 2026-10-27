using System.Runtime.CompilerServices;

namespace SimpleOrm;

/// <summary>The four generated write renderings, as <see cref="Db.RenderObserver"/> reports them.</summary>
internal enum GeneratedWrite
{
    Insert,
    Update,
    UpdateOnly,
    Delete,
}

/// <summary>
/// The generated CRUD text of one entity map on one dialect, rendered once and
/// reused (ADR-0048): the INSERT, the full-row UPDATE, both DELETE forms, the
/// key read, the select-all, and every column-list UPDATE by its column set —
/// with the property lists they bind. A rendering is a pure function of the map
/// and the dialect (the dialect seam's contract), so the cache is keyed by the
/// two objects whose identity defines it: a <see cref="ConditionalWeakTable{TKey, TValue}"/>
/// on the <see cref="EntityMap"/> instance (the entry dies with the map), then
/// the <see cref="IDialect"/> instance by reference. Never a string key. Safe
/// across threads: maps and dialects may be shared by sessions.
/// </summary>
internal sealed class GeneratedSql
{
    /// <summary>A column-list UPDATE beyond this many distinct column sets per map renders uncached (a caller ordering its lists arbitrarily cannot grow the cache without bound).</summary>
    private const int MaxColumnSets = 256;

    private static readonly ConditionalWeakTable<EntityMap, PerMap> ByMap = new();

    private readonly EntityMap _map;
    private readonly IDialect _dialect;
    private readonly Dictionary<IReadOnlyList<PropertyMap>, string> _updateOnly = new(ColumnSetComparer.Instance);
    private string? _insert;
    private PropertyMap[]? _insertBound;
    private string? _update;
    private PropertyMap[]? _updateBound;
    private string? _delete;
    private string? _deleteVersioned;
    private KeyRead? _get;
    private string? _selectAll;

    private GeneratedSql(EntityMap map, IDialect dialect)
    {
        _map = map;
        _dialect = dialect;
    }

    public static GeneratedSql For(EntityMap map, IDialect dialect)
        => ByMap.GetValue(map, static _ => new PerMap()).For(map, dialect);

    public IDialect Dialect => _dialect;

    /// <summary>The generated INSERT (<see cref="IDialect.InsertSql"/>).</summary>
    public string Insert => _insert ??= _dialect.InsertSql(_map);

    /// <summary>What the INSERT binds: every non-generated property, in map order.</summary>
    public PropertyMap[] InsertBound => _insertBound ??= _map.Properties.Where(p => !p.IsGenerated).ToArray();

    /// <summary>The generated full-row UPDATE (<see cref="IDialect.UpdateSql"/>).</summary>
    public string Update => _update ??= _dialect.UpdateSql(_map);

    /// <summary>What the full-row UPDATE binds: the SET values, the key, and the version for the WHERE; generated non-key columns never.</summary>
    public PropertyMap[] UpdateBound => _updateBound ??= _map.Properties.Where(p => p.IsKey || p.IsVersion || !p.IsGenerated).ToArray();

    /// <summary>The generated DELETE (<see cref="IDialect.DeleteSql"/>), by key or version-checked.</summary>
    public string Delete(bool checkVersion)
        => checkVersion
            ? _deleteVersioned ??= _dialect.DeleteSql(_map, checkVersion: true)
            : _delete ??= _dialect.DeleteSql(_map, checkVersion: false);

    /// <summary>The key read: its SELECT — every mapped column by key — and its parameter names, <c>@k0..@kN</c> in key order.</summary>
    public KeyRead Get
    {
        get
        {
            if (_get is null)
            {
                var names = new string[_map.KeyProperties.Count];
                var predicates = new string[names.Length];
                for (var i = 0; i < names.Length; i++)
                {
                    names[i] = "@k" + i;
                    predicates[i] = _dialect.QuoteIdentifier(_map.KeyProperties[i].ColumnName) + " = " + names[i];
                }

                _get = new KeyRead(
                    "select " + string.Join(", ", _map.Properties.Select(p => _dialect.QuoteIdentifier(p.ColumnName)))
                    + " from " + _dialect.QuoteIdentifier(_map.RelationName!)
                    + " where " + string.Join(" and ", predicates),
                    names);
            }

            return _get;
        }
    }

    /// <summary>The generated select-all: every mapped column, ordered by the key when one exists.</summary>
    public string SelectAll
    {
        get
        {
            if (_selectAll is null)
            {
                var sql = "select " + string.Join(", ", _map.Properties.Select(p => _dialect.QuoteIdentifier(p.ColumnName)))
                    + " from " + _dialect.QuoteIdentifier(_map.RelationName!);
                if (_map.KeyProperties.Count > 0)
                {
                    sql += " order by " + string.Join(", ", _map.KeyProperties.Select(k => _dialect.QuoteIdentifier(k.ColumnName)));
                }

                _selectAll = sql;
            }

            return _selectAll;
        }
    }

    /// <summary>
    /// Whether the engine's <c>ORDER BY</c> on this map's key already is the
    /// value order spec/loading.md requires of a loaded collection — then the
    /// post-fetch sort of rows that arrive ordered by the key is the identity and
    /// loading skips it (ADR-0048). The rule: every key part is an integer or
    /// decimal CLR type (byte, short, int, long, decimal) and the dialect stores it
    /// as a number — its <see cref="IDialect.StorageType"/> is an integer or exact
    /// numeric type (<c>integer</c>, <c>bigint</c>, <c>int</c>, <c>smallint</c>,
    /// <c>tinyint</c>, <c>numeric</c>, <c>decimal</c>, with or without a size), so
    /// <c>ORDER BY</c> compares numbers exactly as the value comparison does.
    /// SQLite stores a decimal as <c>TEXT</c> (<c>'10'</c> before <c>'2'</c>): a
    /// decimal key keeps the sort there and skips it on PostgreSQL and SQL Server.
    /// Strings (collations are not ordinal), GUIDs, temporals, booleans, enums, and
    /// byte arrays always keep the sort.
    /// </summary>
    public bool KeyOrderIsValueOrder => _keyOrderIsValueOrder ??= ComputeKeyOrderIsValueOrder();

    private bool? _keyOrderIsValueOrder;

    private bool ComputeKeyOrderIsValueOrder()
    {
        if (_map.KeyProperties.Count == 0)
        {
            return false;
        }

        foreach (var key in _map.KeyProperties)
        {
            var type = Nullable.GetUnderlyingType(key.ClrType) ?? key.ClrType;
            if (type != typeof(long) && type != typeof(int) && type != typeof(short) && type != typeof(byte) && type != typeof(decimal))
            {
                return false;
            }

            string storage;
            try
            {
                storage = _dialect.StorageType(_map, key).Trim().ToLowerInvariant();
            }
            catch (Exception exception) when (exception is not OutOfMemoryException)
            {
                return false;   // a type the dialect cannot spell: keep the sort
            }

            var paren = storage.IndexOf('(');
            var name = paren < 0 ? storage : storage.Substring(0, paren).TrimEnd();
            if (name is not ("integer" or "bigint" or "int" or "smallint" or "tinyint" or "numeric" or "decimal"))
            {
                return false;
            }
        }

        return true;
    }

    /// <summary>The column-list UPDATE (<see cref="IDialect.UpdateOnlySql"/>) for exactly this ordered set of properties.</summary>
    public string UpdateOnly(IReadOnlyList<PropertyMap> set)
    {
        lock (_updateOnly)
        {
            if (_updateOnly.TryGetValue(set, out var cached))
            {
                return cached;
            }
        }

        var sql = _dialect.UpdateOnlySql(_map, set);
        lock (_updateOnly)
        {
            if (_updateOnly.Count < MaxColumnSets && !_updateOnly.ContainsKey(set))
            {
                _updateOnly.Add(set.ToArray(), sql);   // a copy: the caller's list is not the cache's
            }
        }

        return sql;
    }

    /// <summary>The key read's text and parameter names, published together.</summary>
    internal sealed class KeyRead(string sql, string[] parameterNames)
    {
        public string Sql { get; } = sql;

        public string[] ParameterNames { get; } = parameterNames;
    }

    /// <summary>The per-map entries, one per dialect instance (almost always exactly one).</summary>
    private sealed class PerMap
    {
        private GeneratedSql[] _entries = [];

        public GeneratedSql For(EntityMap map, IDialect dialect)
        {
            foreach (var entry in Volatile.Read(ref _entries))
            {
                if (ReferenceEquals(entry._dialect, dialect))
                {
                    return entry;
                }
            }

            lock (this)
            {
                foreach (var entry in _entries)
                {
                    if (ReferenceEquals(entry._dialect, dialect))
                    {
                        return entry;
                    }
                }

                var created = new GeneratedSql(map, dialect);
                Volatile.Write(ref _entries, [.. _entries, created]);
                return created;
            }
        }
    }

    /// <summary>A column set is its properties in order, compared by instance — the map's own <see cref="PropertyMap"/>s.</summary>
    private sealed class ColumnSetComparer : IEqualityComparer<IReadOnlyList<PropertyMap>>
    {
        public static readonly ColumnSetComparer Instance = new();

        public bool Equals(IReadOnlyList<PropertyMap>? x, IReadOnlyList<PropertyMap>? y)
        {
            if (ReferenceEquals(x, y))
            {
                return true;
            }

            if (x is null || y is null || x.Count != y.Count)
            {
                return false;
            }

            for (var i = 0; i < x.Count; i++)
            {
                if (!ReferenceEquals(x[i], y[i]))
                {
                    return false;
                }
            }

            return true;
        }

        public int GetHashCode(IReadOnlyList<PropertyMap> set)
        {
            unchecked
            {
                var hash = 17;
                for (var i = 0; i < set.Count; i++)
                {
                    hash = (hash * 31) + RuntimeHelpers.GetHashCode(set[i]);
                }

                return hash;
            }
        }
    }
}
