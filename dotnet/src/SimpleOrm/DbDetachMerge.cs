namespace SimpleOrm;

/// <summary>
/// Detach and merge (Level 3 M3, spec/unit-of-work.md "Detach and merge") — the
/// simple rules, no deep graph merge. <see cref="Detach"/> makes the session
/// forget an instance: it is no longer the session's instance for its key, and
/// any insert or removal scheduled for it is cancelled; the instance keeps its
/// values. <see cref="MergeAsync{TEntity}"/> brings a detached copy's state back:
/// the session's instance for the key — the tracked one, or the row read now —
/// takes every mapped value of the copy (version included, so a stale copy still
/// fails its next update's version check), and that instance is returned.
/// Navigations are not merged. Both need a tracked session (<c>UOW-003</c>).
/// </summary>
public sealed partial class Db
{
    /// <summary>
    /// Forgets the instance: a tracked one is untracked, a scheduled one is
    /// unscheduled (an insert cancelled with what it cascaded, a removal dropped);
    /// an instance the session neither tracks nor has scheduled is <c>UOW-001</c>.
    /// </summary>
    public void Detach(object entity)
    {
        RequireUnitOfWork();
        var map = Maps.Load(entity.GetType());
        var known = false;
        if (_trackedByInstance.TryGetValue(entity, out var entry))
        {
            Untrack(map, map.GetKeyValues(entry.Entity));
            known = true;
        }

        if (_scheduledInsertSet.Contains(entity))
        {
            var plan = new SchedulePlan();
            RemoveCore(entity, new HashSet<object>(InstanceComparer.Instance), plan);
            foreach (var cancelled in plan.Cancellations)
            {
                _scheduledInsertSet.Remove(cancelled);
                _scheduledInserts.Remove(cancelled);
                _directInserts.Remove(cancelled);
                _pendingReferences.RemoveAll(r => ReferenceEquals(r.Child, cancelled) || ReferenceEquals(r.Parent, cancelled));
            }

            known = true;
        }

        if (_scheduledRemoveSet.Remove(entity))
        {
            _scheduledRemoves.Remove(entity);
            known = true;
        }

        if (!known)
        {
            throw new SimpleOrmException(
                "UOW-001", map.EntityType.Name,
                "the session neither tracks nor has scheduled this instance; there is nothing to detach");
        }
    }

    /// <summary>
    /// Copies a detached instance's mapped values onto the session's instance for
    /// its key and returns that instance: the tracked one when the key is known,
    /// else the row read now (one visible round trip; a missing row is
    /// <c>CRUD-001</c>, an instance without a complete key <c>CRUD-002</c> — new
    /// instances are persisted, not merged). The copy becomes dirty where it
    /// differed; the detached instance itself stays untracked. Merging the
    /// session's own instance is a no-op returning it.
    /// </summary>
    public async Task<TEntity> MergeAsync<TEntity>(TEntity detached, CancellationToken ct)
        where TEntity : class
    {
        RequireUnitOfWork();
        var map = RequireWritableKeyed<TEntity>();
        if (IsTracked(detached))
        {
            return detached;
        }

        var key = map.GetKeyValues(detached);
        // Unset: null, a database-generated key at its default, or an empty client GUID; a natural key's default is a key.
        var unset = key.Select((value, i) => value is null
            || (map.KeyProperties[i].IsGenerated && Equals(value, Activator.CreateInstance(map.KeyProperties[i].ClrType)))
            || (map.KeyStrategy == KeyStrategy.ClientGuid && Equals(value, Guid.Empty)))
            .Any(missing => missing);
        if (unset)
        {
            throw new SimpleOrmException(
                "CRUD-002", typeof(TEntity).Name,
                $"the instance's key ({FormatKey(key)}) is not set; a new instance is persisted, not merged");
        }

        TEntity target;
        if (TrackedOf(map.EntityType).TryGetValue(key, out var holder))
        {
            target = (TEntity)holder.Entity;
        }
        else
        {
            var fresh = await ReadByKeyAsync<TEntity>(map, key, ct).ConfigureAwait(false)
                ?? throw new SimpleOrmException(
                    "CRUD-001", typeof(TEntity).Name, $"no row with key ({FormatKey(key)}) to merge into");
            target = (TEntity)Track(map, fresh);
        }

        foreach (var property in map.Properties)
        {
            if (!property.IsKey)
            {
                property.SetValue(target, property.GetValue(detached));
            }
        }

        return target;
    }
}
