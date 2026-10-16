namespace SimpleOrm;

/// <summary>
/// The identity map and snapshot change tracking (Level 3 M1, ADR-0034 add.1).
/// A <see cref="TrackingMode.Tracked"/> session keeps one instance per (entity
/// type, key) — the §7.4 identity, compared value-wise — and a snapshot of each
/// instance's mapped values as read. Every read path that materializes an entity
/// consults the map: a known key returns the tracked instance and the row's
/// values are <b>not</b> applied (the tracked instance wins, dirty or not;
/// <see cref="RefreshAsync{TEntity}"/> overwrites on request). Writes keep the
/// map honest: an insert or full-row update registers/re-snapshots the instance,
/// a column-list update moves the snapshot forward for the written columns only
/// (an unwritten change stays dirty), a delete forgets the key, and a write from
/// an instance that is not the session's instance for its key refuses
/// (<c>UOW-002</c>) instead of racing it. <see cref="UpdateAsync{TEntity}"/> on a
/// tracked instance writes only the dirty columns through the ADR-0028
/// column-list path, and nothing at all when the instance is clean.
///
/// The snapshot holds each value in its <b>database-bound form</b> (the type
/// converter's output, ADR-0035): "dirty" means "the database would receive a
/// different value", so a handler-typed or JSON member compares by what would be
/// written and an in-place mutation of such a member counts as a change. Byte
/// arrays compare by content. Untracked sessions (the default) never enter this
/// file's logic: every read is a fresh instance, exactly as at Levels 0–2.
/// </summary>
public sealed partial class Db
{
    private readonly Dictionary<Type, Dictionary<object?[], TrackedEntry>> _trackedByKey = new();
    private readonly Dictionary<object, TrackedEntry> _trackedByInstance = new(InstanceComparer.Instance);

    /// <summary>Whether this session keeps an identity map (<see cref="DbOptions.Tracking"/>).</summary>
    public bool IsTracking => Options.Tracking == TrackingMode.Tracked;

    /// <summary>Whether the session holds this instance as the one for its key.</summary>
    public bool IsTracked(object entity) => _trackedByInstance.ContainsKey(entity);

    /// <summary>
    /// The mapped properties whose current value differs from the snapshot taken
    /// when the instance was read or last written — property names in metadata
    /// order, dotted for owned members (ADR-0030). An instance the session does
    /// not track is <c>UOW-001</c> (an untracked session, or an instance that
    /// never came through this session).
    /// </summary>
    public IReadOnlyList<string> ChangesOf<TEntity>(TEntity entity)
        where TEntity : class
    {
        var entry = RequireTracked(entity);
        var map = Maps.Load<TEntity>();
        return DirtyProperties(map, entity, entry.Snapshot).Select(p => p.PropertyName).ToArray();
    }

    /// <summary>Whether <see cref="ChangesOf{TEntity}"/> is non-empty.</summary>
    public bool IsDirty<TEntity>(TEntity entity)
        where TEntity : class
        => ChangesOf(entity).Count > 0;

    /// <summary>
    /// Re-reads the instance's row by key and applies every mapped value onto the
    /// instance — the explicit way to overwrite local changes (ADR-0034 add.1
    /// ruling 2). The snapshot resets when the session tracks the instance; an
    /// instance the session does not know is refreshed but not registered.
    /// Works in both tracking modes; a missing row is <c>CRUD-001</c>; an
    /// instance that is not the session's instance for its key is <c>UOW-002</c>.
    /// </summary>
    public async Task RefreshAsync<TEntity>(TEntity entity, CancellationToken ct)
        where TEntity : class
    {
        var map = Maps.Load<TEntity>();
        if (map.Kind is RelationKind.Statement or RelationKind.Procedure)
        {
            throw new SimpleOrmException(
                "QRY-005", typeof(TEntity).Name, $"is {map.Kind}-backed; key reads need a named relation");
        }

        if (map.KeyProperties.Count == 0)
        {
            throw new SimpleOrmException("CRUD-002", typeof(TEntity).Name, "the entity defines no key");
        }

        var key = map.GetKeyValues(entity);
        if (key.Any(v => v is null))
        {
            throw new SimpleOrmException(
                "CRUD-002", typeof(TEntity).Name, $"the instance's key ({FormatKey(key)}) has a null part");
        }

        var entry = CheckIdentity(map, entity);
        var fresh = await ReadByKeyAsync<TEntity>(map, key, ct).ConfigureAwait(false)
            ?? throw new SimpleOrmException(
                "CRUD-001", typeof(TEntity).Name, $"no row with key ({FormatKey(key)})");
        foreach (var property in map.Properties)
        {
            property.SetValue(entity, property.GetValue(fresh));
        }

        if (entry is not null)
        {
            entry.Snapshot = Snapshot(map, entity);
        }
    }

    // --- the read side -----------------------------------------------------------------

    /// <summary>
    /// The session's instance for a freshly materialized entity: the tracked one
    /// when the key is known (the row's values are discarded — ruling 2), else this
    /// instance, registered with its snapshot. Untracked sessions, keyless maps,
    /// and null key parts return the instance unchanged.
    /// </summary>
    internal object Track(EntityMap map, object entity)
    {
        if (!IsTracking || map.KeyProperties.Count == 0)
        {
            return entity;
        }

        var key = map.GetKeyValues(entity);
        if (key.Any(v => v is null))
        {
            return entity;
        }

        var byKey = TrackedOf(map.EntityType);
        if (byKey.TryGetValue(key, out var existing))
        {
            return existing.Entity;
        }

        Register(map, key, entity);
        return entity;
    }

    /// <summary>The tracker for a result type, or null when there is nothing to track (untracked session, plain result types, keyless maps).</summary>
    internal Func<object, object>? TrackerFor(Type type)
    {
        if (!IsTracking || !EntityMapLoader.HasMappingAttributes(type))
        {
            return null;
        }

        var map = Maps.Load(type);
        return map.KeyProperties.Count == 0 ? null : entity => Track(map, entity);
    }

    // --- the write side ----------------------------------------------------------------

    /// <summary>
    /// Before a write (and a refresh): the instance must be the session's instance
    /// for its key. Returns the entry when this instance is tracked, null when the
    /// session does not know it; throws <c>UOW-002</c> when another instance holds
    /// the key, or when a tracked instance's key was changed.
    /// </summary>
    private TrackedEntry? CheckIdentity(EntityMap map, object entity)
    {
        if (!IsTracking || map.KeyProperties.Count == 0)
        {
            return null;
        }

        var key = map.GetKeyValues(entity);
        var tracked = _trackedByInstance.TryGetValue(entity, out var own) ? own : null;
        if (key.All(v => v is not null) && TrackedOf(map.EntityType).TryGetValue(key, out var holder))
        {
            if (!ReferenceEquals(holder.Entity, entity))
            {
                throw new SimpleOrmException(
                    "UOW-002", map.EntityType.Name,
                    $"another instance is the session's instance for key ({FormatKey(key)}); write through that instance, or open an untracked session");
            }

            return holder;
        }

        if (tracked is not null)
        {
            throw new SimpleOrmException(
                "UOW-002", map.EntityType.Name,
                $"the tracked instance's key changed to ({FormatKey(key)}); a key is identity and never updates — delete and insert instead");
        }

        return null;
    }

    /// <summary>After an insert or a full-row update: the instance is the session's instance for its key, with a fresh snapshot of every column.</summary>
    private void TrackWritten(EntityMap map, object entity)
    {
        if (!IsTracking || map.KeyProperties.Count == 0)
        {
            return;
        }

        var key = map.GetKeyValues(entity);
        if (key.Any(v => v is null))
        {
            return;
        }

        if (_trackedByInstance.TryGetValue(entity, out var entry))
        {
            entry.Snapshot = Snapshot(map, entity);
            return;
        }

        Register(map, key, entity);
    }

    /// <summary>
    /// After a column-list update of a tracked instance: the snapshot moves
    /// forward for the written columns and the version only — an unwritten change
    /// stays dirty, so the snapshot never claims a column the database did not
    /// receive. An instance the session does not know is not registered: the
    /// session saw only part of the row.
    /// </summary>
    private void TrackWrittenColumns(TrackedEntry? entry, EntityMap map, IEnumerable<PropertyMap> written)
    {
        if (entry is null)
        {
            return;
        }

        var moved = new HashSet<PropertyMap>(written);
        if (map.VersionProperty is { } version)
        {
            moved.Add(version);
        }

        for (var i = 0; i < map.Properties.Count; i++)
        {
            if (moved.Contains(map.Properties[i]))
            {
                entry.Snapshot[i] = BoundValue(map, map.Properties[i], entry.Entity);
            }
        }
    }

    /// <summary>After a successful delete: the key is forgotten; the deleted instance is no longer tracked.</summary>
    private void Untrack(EntityMap map, object?[] key)
    {
        if (!IsTracking || !TrackedOf(map.EntityType).TryGetValue(key, out var entry))
        {
            return;
        }

        TrackedOf(map.EntityType).Remove(key);
        _trackedByInstance.Remove(entry.Entity);
    }

    // --- snapshots ---------------------------------------------------------------------

    private void Register(EntityMap map, object?[] key, object entity)
    {
        var byKey = TrackedOf(map.EntityType);
        if (byKey.TryGetValue(key, out var stale))
        {
            // A key reused after an external delete: the instance that wrote the
            // row is the session's instance now; the stale holder is forgotten.
            _trackedByInstance.Remove(stale.Entity);
        }

        var entry = new TrackedEntry(entity, Snapshot(map, entity));
        byKey[key] = entry;
        _trackedByInstance[entity] = entry;
    }

    private Dictionary<object?[], TrackedEntry> TrackedOf(Type entityType)
    {
        if (!_trackedByKey.TryGetValue(entityType, out var byKey))
        {
            _trackedByKey[entityType] = byKey = new Dictionary<object?[], TrackedEntry>(KeyTupleComparer.Instance);
        }

        return byKey;
    }

    private TrackedEntry RequireTracked(object entity)
    {
        if (!IsTracking)
        {
            throw new SimpleOrmException(
                "UOW-001", entity.GetType().Name,
                "the session is untracked; open it with DbOptions.Tracking = TrackingMode.Tracked to ask about changes");
        }

        return _trackedByInstance.TryGetValue(entity, out var entry)
            ? entry
            : throw new SimpleOrmException(
                "UOW-001", entity.GetType().Name,
                "the session does not track this instance: it was not read through, inserted by, or updated by this session");
    }

    /// <summary>The mapped values in database-bound form — the comparison baseline.</summary>
    private object?[] Snapshot(EntityMap map, object entity)
    {
        var values = new object?[map.Properties.Count];
        for (var i = 0; i < values.Length; i++)
        {
            values[i] = BoundValue(map, map.Properties[i], entity);
        }

        return values;
    }

    /// <summary>One value as the database would receive it: the converter's output, byte arrays copied so in-place edits still show.</summary>
    private object? BoundValue(EntityMap map, PropertyMap property, object entity)
    {
        var bound = _converter.ToDatabase(
            property.GetValue(entity), $"{map.EntityType.Name}.{property.PropertyName}", property.EnumAsInt);
        return bound is DBNull ? null : bound is byte[] bytes ? bytes.ToArray() : bound;
    }

    private List<PropertyMap> DirtyProperties(EntityMap map, object entity, object?[] snapshot)
    {
        var dirty = new List<PropertyMap>();
        for (var i = 0; i < map.Properties.Count; i++)
        {
            if (!ValueEquals(BoundValue(map, map.Properties[i], entity), snapshot[i]))
            {
                dirty.Add(map.Properties[i]);
            }
        }

        return dirty;
    }

    /// <summary>Equality of two database-bound values: nulls, byte arrays by content, everything else by <see cref="object.Equals(object)"/>.</summary>
    private static bool ValueEquals(object? left, object? right)
        => (left, right) switch
        {
            (null, null) => true,
            (null, _) or (_, null) => false,
            (byte[] l, byte[] r) => l.AsSpan().SequenceEqual(r),
            _ => left.Equals(right),
        };

    private sealed class TrackedEntry(object entity, object?[] snapshot)
    {
        public object Entity { get; } = entity;

        public object?[] Snapshot { get; set; } = snapshot;
    }

    /// <summary>Reference identity for the by-instance index (netstandard2.0 has no <c>ReferenceEqualityComparer</c>).</summary>
    private sealed class InstanceComparer : IEqualityComparer<object>
    {
        public static readonly InstanceComparer Instance = new();

        public new bool Equals(object? x, object? y) => ReferenceEquals(x, y);

        public int GetHashCode(object obj) => System.Runtime.CompilerServices.RuntimeHelpers.GetHashCode(obj);
    }
}
