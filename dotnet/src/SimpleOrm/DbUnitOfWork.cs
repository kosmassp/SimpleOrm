using System.Collections;
using System.Reflection;

namespace SimpleOrm;

/// <summary>What one <see cref="Db.FlushAsync"/> wrote, by statement kind.</summary>
public readonly record struct FlushSummary(int Inserted, int Updated, int Deleted);

/// <summary>
/// The unit of work (Level 3 M2, ADR-0034 §7c): <see cref="Persist"/> and
/// <see cref="Remove"/> schedule, <see cref="FlushAsync"/> writes — inserts in
/// foreign-key order with generated keys flowing into dependents, then the dirty
/// tracked instances, then deletes children-first — inside the session's
/// transaction, or one the flush opens and commits itself. Cascades are
/// <b>declared</b> on navigations (<see cref="CascadeAttribute"/>), never
/// inferred, and never load: a remove cascading over an unloaded collection
/// refuses (<c>UOW-005</c>), a persist cascades only over initialized ones.
/// Every statement is the same generated CRUD the immediate operations use —
/// one implementation per concept (§7.14–16, ADR-0028) — so the identity map,
/// the snapshots, the version rules, and the identity gate apply unchanged.
/// The unit of work is part of tracking: an untracked session has no snapshots
/// to flush from (<c>UOW-003</c>).
/// </summary>
public sealed partial class Db
{
    private readonly List<object> _scheduledInserts = [];
    private readonly HashSet<object> _scheduledInsertSet = new(InstanceComparer.Instance);
    private readonly HashSet<object> _directInserts = new(InstanceComparer.Instance);
    private readonly List<object> _scheduledRemoves = [];
    private readonly HashSet<object> _scheduledRemoveSet = new(InstanceComparer.Instance);
    private readonly List<PendingReference> _pendingReferences = [];

    /// <summary>
    /// Schedules an insert for an instance the session does not track (a tracked
    /// instance is already managed: its changes flush as updates) and cascades
    /// through the navigations declared <see cref="CascadeOperations.Persist"/>,
    /// recording the key each cascaded child takes from its owner at flush.
    /// Persisting an instance scheduled for removal is <c>UOW-006</c>.
    /// </summary>
    public void Persist(object entity)
    {
        RequireUnitOfWork();
        // Plan the whole cascade first, then commit it to the schedule: a refusal
        // anywhere in the graph (UOW-006, CRUD-002/003) leaves the schedule untouched.
        var plan = new SchedulePlan();
        PersistCore(entity, new HashSet<object>(InstanceComparer.Instance), plan);
        foreach (var scheduled in plan.Inserts)
        {
            if (_scheduledInsertSet.Add(scheduled))
            {
                _scheduledInserts.Add(scheduled);
            }
        }

        _pendingReferences.AddRange(plan.References);
        if (!IsTracked(entity))
        {
            _directInserts.Add(entity);   // asked for by name: survives a cancelled owner
        }
    }

    /// <summary>
    /// Schedules a delete for a tracked instance (<c>UOW-001</c> otherwise — the
    /// session knows no row for it) and cascades through the navigations declared
    /// <see cref="CascadeOperations.Remove"/>: a loaded collection removes its
    /// members, a never-loaded one is <c>UOW-005</c>, a null singular navigation
    /// cascades nothing. Removing an instance scheduled for insert cancels the
    /// insert, and the inserts it cascaded.
    /// </summary>
    public void Remove(object entity)
    {
        RequireUnitOfWork();
        // Planned first, committed after: a UOW-005/UOW-001 deep in the cascade
        // schedules nothing.
        var plan = new SchedulePlan();
        RemoveCore(entity, new HashSet<object>(InstanceComparer.Instance), plan);
        foreach (var cancelled in plan.Cancellations)
        {
            _scheduledInsertSet.Remove(cancelled);
            _scheduledInserts.Remove(cancelled);
            _directInserts.Remove(cancelled);
            _pendingReferences.RemoveAll(r => ReferenceEquals(r.Child, cancelled) || ReferenceEquals(r.Parent, cancelled));
        }

        foreach (var scheduled in plan.Removes)
        {
            if (_scheduledRemoveSet.Add(scheduled))
            {
                _scheduledRemoves.Add(scheduled);
            }
        }
    }

    /// <summary>
    /// Writes the unit of work: scheduled inserts in dependency order (a child
    /// after the parent its navigation or cascade points at; types in foreign-key
    /// order, instances in schedule order), every dirty tracked instance as a
    /// column-list update, then scheduled deletes children-first. Runs inside the
    /// session's transaction when one is active, else in one the flush opens and
    /// commits; a failure leaves the schedule in place and, when the flush owned
    /// the transaction, nothing applied. A cycle among the scheduled instances is
    /// <c>UOW-004</c>, before any SQL.
    /// </summary>
    public async Task<FlushSummary> FlushAsync(CancellationToken ct)
    {
        RequireUnitOfWork();
        // A scheduled insert written immediately in the meantime is tracked now, a
        // scheduled removal deleted immediately is not: both are done, not work.
        var inserts = OrderInserts().Where(e => !IsTracked(e)).ToList();
        var removes = OrderRemoves().Where(IsTracked).ToList();
        var updates = DirtyTrackedInstances();
        if (inserts.Count == 0 && updates.Count == 0 && removes.Count == 0)
        {
            ClearSchedule();
            return default;   // nothing to write: no statement, no transaction
        }

        var ownsTransaction = _transaction is null;
        var scope = ownsTransaction ? await BeginAsync(ct).ConfigureAwait(false) : null;
        var undo = new List<Action>();
        try
        {
            foreach (var entity in inserts)
            {
                var map = Maps.Load(entity.GetType());
                ApplyReferences(map, entity);
                var keyBefore = map.KeyProperties.Select(k => k.GetValue(entity)).ToArray();
                await InvokeWriteAsync(nameof(InsertAsync), map, entity, ct).ConfigureAwait(false);
                undo.Add(() =>
                {
                    Untrack(map, map.GetKeyValues(entity));
                    for (var i = 0; i < keyBefore.Length; i++)
                    {
                        map.KeyProperties[i].SetValue(entity, keyBefore[i]);
                    }
                });
            }

            foreach (var entity in updates)
            {
                var map = Maps.Load(entity.GetType());
                var entry = _trackedByInstance[entity];
                var snapshotBefore = (object?[])entry.Snapshot.Clone();
                var versionBefore = map.VersionProperty?.GetValue(entity);
                await InvokeWriteAsync(nameof(UpdateAsync), map, entity, ct).ConfigureAwait(false);
                undo.Add(() =>
                {
                    entry.Snapshot = snapshotBefore;
                    map.VersionProperty?.SetValue(entity, versionBefore);
                });
            }

            foreach (var entity in removes)
            {
                var map = Maps.Load(entity.GetType());
                var snapshotBefore = _trackedByInstance[entity].Snapshot;
                await InvokeWriteAsync(nameof(DeleteAsync), map, entity, ct).ConfigureAwait(false);
                undo.Add(() => Register(map, map.GetKeyValues(entity), entity, snapshotBefore));
            }

            if (scope is not null)
            {
                await scope.CommitAsync(ct).ConfigureAwait(false);
            }

            ClearSchedule();
            return new FlushSummary(inserts.Count, updates.Count, removes.Count);
        }
        catch
        {
            // The in-memory effects of the statements that ran are undone — keys,
            // snapshots, versions, tracking — so the schedule can flush again as
            // it was; the database is rolled back below when the flush owns the
            // transaction, and is the caller's to roll back otherwise.
            for (var i = undo.Count - 1; i >= 0; i--)
            {
                undo[i]();
            }

            throw;
        }
        finally
        {
            if (scope is not null)
            {
                await scope.DisposeAsync().ConfigureAwait(false);   // rolls back unless committed
            }
        }
    }

    /// <summary>Whether anything is scheduled for insert or removal (dirty tracked instances flush regardless).</summary>
    public bool HasPendingWork => _scheduledInserts.Count > 0 || _scheduledRemoves.Count > 0;

    // --- scheduling ------------------------------------------------------------------

    private void PersistCore(object entity, HashSet<object> visited, SchedulePlan plan)
    {
        if (!visited.Add(entity))
        {
            return;
        }

        var map = RequireWritableKeyed(entity.GetType());
        if (_scheduledRemoveSet.Contains(entity))
        {
            throw new SimpleOrmException(
                "UOW-006", map.EntityType.Name,
                "the instance is scheduled for removal; a removed instance cannot be persisted in the same unit of work");
        }

        if (!IsTracked(entity) && !_scheduledInsertSet.Contains(entity))
        {
            plan.Inserts.Add(entity);
        }

        foreach (var relationship in map.Relationships)
        {
            if (!relationship.Cascade.HasFlag(CascadeOperations.Persist))
            {
                continue;
            }

            var value = NavigationValue(map, relationship, entity);
            if (value is null || value is IUnloadedSentinel)
            {
                continue;   // nothing initialized to persist; cascading never loads
            }

            switch (relationship.Kind)
            {
                case RelationshipKind.ManyToOne:
                    PersistCore(value, visited, plan);
                    break;
                case RelationshipKind.OneToOne:
                    PersistCore(value, visited, plan);
                    plan.References.Add(new PendingReference(value, entity, relationship.ForeignKeyProperties));
                    break;
                case RelationshipKind.OneToMany:
                    foreach (var child in (IEnumerable)value)
                    {
                        PersistCore(child, visited, plan);
                        plan.References.Add(new PendingReference(child, entity, relationship.ForeignKeyProperties));
                    }

                    break;
            }
        }
    }

    private void RemoveCore(object entity, HashSet<object> visited, SchedulePlan plan)
    {
        if (!visited.Add(entity))
        {
            return;
        }

        var map = RequireWritableKeyed(entity.GetType());
        if (_scheduledInsertSet.Contains(entity))
        {
            // A cancelled insert: nothing to delete; the inserts its persist cascade
            // scheduled go with it — except a child persisted directly by name, or
            // one another scheduled owner still points at.
            plan.Cancellations.Add(entity);
            foreach (var relationship in map.Relationships.Where(r => r.Cascade.HasFlag(CascadeOperations.Persist)))
            {
                foreach (var child in NavigationMembers(map, relationship, entity))
                {
                    if (_scheduledInsertSet.Contains(child) && !_directInserts.Contains(child)
                        && !_pendingReferences.Any(r => ReferenceEquals(r.Child, child)
                            && !ReferenceEquals(r.Parent, entity) && !plan.Cancellations.Contains(r.Parent)))
                    {
                        RemoveCore(child, visited, plan);
                    }
                }
            }

            return;
        }

        if (!IsTracked(entity))
        {
            throw new SimpleOrmException(
                "UOW-001", map.EntityType.Name,
                "the session does not track this instance, so it knows no row to remove; read it through this session first, or delete by key");
        }

        plan.Removes.Add(entity);
        foreach (var relationship in map.Relationships)
        {
            if (!relationship.Cascade.HasFlag(CascadeOperations.Remove))
            {
                continue;
            }

            var value = NavigationValue(map, relationship, entity);
            if (value is IUnloadedSentinel)
            {
                throw new SimpleOrmException(
                    "UOW-005", $"{map.EntityType.Name}.{relationship.PropertyName}",
                    "the remove cascades over a collection that was never loaded — load it (LoadAsync/Include) or remove the children explicitly; nothing loads implicitly");
            }

            foreach (var child in NavigationMembers(map, relationship, entity))
            {
                RemoveCore(child, visited, plan);
            }
        }
    }

    /// <summary>What one Persist/Remove call would schedule, committed only when the whole cascade was accepted.</summary>
    private sealed class SchedulePlan
    {
        public List<object> Inserts { get; } = [];

        public List<PendingReference> References { get; } = [];

        public List<object> Removes { get; } = [];

        public List<object> Cancellations { get; } = [];
    }

    private void ClearSchedule()
    {
        _scheduledInserts.Clear();
        _scheduledInsertSet.Clear();
        _directInserts.Clear();
        _scheduledRemoves.Clear();
        _scheduledRemoveSet.Clear();
        _pendingReferences.Clear();
    }

    // --- ordering --------------------------------------------------------------------

    /// <summary>Inserts: a child after every scheduled parent it references; ties by type dependency rank, then schedule order.</summary>
    private List<object> OrderInserts()
    {
        var dependencies = new Dictionary<object, List<object>>(InstanceComparer.Instance);
        foreach (var entity in _scheduledInserts)
        {
            var parents = new List<object>();
            var map = Maps.Load(entity.GetType());
            foreach (var relationship in map.Relationships.Where(r => r.Kind == RelationshipKind.ManyToOne))
            {
                var target = NavigationValue(map, relationship, entity);
                if (target is not null && _scheduledInsertSet.Contains(target))
                {
                    parents.Add(target);
                }
            }

            foreach (var reference in _pendingReferences.Where(r => ReferenceEquals(r.Child, entity)))
            {
                if (_scheduledInsertSet.Contains(reference.Parent))
                {
                    parents.Add(reference.Parent);
                }
            }

            dependencies[entity] = parents;
        }

        var ranks = TypeRanks(_scheduledInserts.Select(e => e.GetType()));
        return TopologicalOrder(_scheduledInserts, dependencies, e => ranks[e.GetType()], "insert");
    }

    /// <summary>Deletes: a parent after every scheduled child that references it (children first); ties by reversed type rank, then schedule order.</summary>
    private List<object> OrderRemoves()
    {
        var dependencies = new Dictionary<object, List<object>>(InstanceComparer.Instance);
        foreach (var entity in _scheduledRemoves)
        {
            dependencies[entity] = [];
        }

        foreach (var child in _scheduledRemoves)
        {
            var map = Maps.Load(child.GetType());
            foreach (var relationship in map.Relationships.Where(r => r.Kind == RelationshipKind.ManyToOne))
            {
                var parent = NavigationValue(map, relationship, child);
                if (parent is not null && _scheduledRemoveSet.Contains(parent))
                {
                    dependencies[parent].Add(child);   // the parent waits for the child
                }
            }

            foreach (var relationship in map.Relationships.Where(
                r => r.Kind is RelationshipKind.OneToMany or RelationshipKind.OneToOne))
            {
                foreach (var member in NavigationMembers(map, relationship, child))
                {
                    if (_scheduledRemoveSet.Contains(member))
                    {
                        dependencies[child].Add(member);   // this owner waits for its members
                    }
                }
            }
        }

        var ranks = TypeRanks(_scheduledRemoves.Select(e => e.GetType()));
        return TopologicalOrder(_scheduledRemoves, dependencies, e => -ranks[e.GetType()], "delete");
    }

    /// <summary>
    /// Type dependency ranks from the metadata: a type ranks after every type its
    /// foreign keys reference (its many-to-one targets, and the owners whose
    /// one-to-many/one-to-one point at it). Types in a dependency cycle
    /// (self-reference, mutual) share one rank — Tarjan's strongly connected
    /// components, emitted dependencies-first — and the schedule decides inside
    /// it; instance edges decide the rest.
    /// </summary>
    private Dictionary<Type, int> TypeRanks(IEnumerable<Type> types)
    {
        // The graph spans every type reachable through relationships from the
        // flush's own, so two scheduled types related only through a third
        // (not scheduled this time) still rank in dependency order.
        var involved = new List<Type>();
        var pending = new Queue<Type>(types.Distinct());
        var dependencies = new Dictionary<Type, HashSet<Type>>();
        while (pending.Count > 0)
        {
            var type = pending.Dequeue();
            if (dependencies.ContainsKey(type))
            {
                continue;
            }

            involved.Add(type);
            dependencies[type] = [];
            foreach (var relationship in Maps.Load(type).Relationships)
            {
                if (relationship.Kind is RelationshipKind.ManyToMany)
                {
                    continue;
                }

                pending.Enqueue(relationship.TargetType);
            }
        }

        foreach (var type in involved)
        {
            foreach (var relationship in Maps.Load(type).Relationships)
            {
                switch (relationship.Kind)
                {
                    case RelationshipKind.ManyToOne:
                        dependencies[type].Add(relationship.TargetType);               // type depends on target
                        break;
                    case RelationshipKind.OneToMany or RelationshipKind.OneToOne:
                        dependencies[relationship.TargetType].Add(type);               // target depends on this owner
                        break;
                }
            }
        }

        // Tarjan: components complete in dependencies-first order, so the first
        // component found gets rank 0.
        var ranks = new Dictionary<Type, int>();
        var index = new Dictionary<Type, int>();
        var lowLink = new Dictionary<Type, int>();
        var stack = new Stack<Type>();
        var onStack = new HashSet<Type>();
        var next = 0;
        var componentRank = 0;
        void StrongConnect(Type type)
        {
            index[type] = lowLink[type] = next++;
            stack.Push(type);
            onStack.Add(type);
            foreach (var dependency in dependencies[type])
            {
                if (!index.ContainsKey(dependency))
                {
                    StrongConnect(dependency);
                    lowLink[type] = Math.Min(lowLink[type], lowLink[dependency]);
                }
                else if (onStack.Contains(dependency))
                {
                    lowLink[type] = Math.Min(lowLink[type], index[dependency]);
                }
            }

            if (lowLink[type] != index[type])
            {
                return;
            }

            Type member;
            do
            {
                member = stack.Pop();
                onStack.Remove(member);
                ranks[member] = componentRank;
            }
            while (!ReferenceEquals(member, type));
            componentRank++;
        }

        foreach (var type in involved)
        {
            if (!index.ContainsKey(type))
            {
                StrongConnect(type);
            }
        }

        return ranks;
    }

    /// <summary>Kahn's algorithm with a deterministic ready pick: lowest (rank, schedule index) first; a leftover is a cycle (<c>UOW-004</c>).</summary>
    private static List<object> TopologicalOrder(
        List<object> scheduled, Dictionary<object, List<object>> dependencies, Func<object, int> rank, string operation)
    {
        var index = new Dictionary<object, int>(InstanceComparer.Instance);
        for (var i = 0; i < scheduled.Count; i++)
        {
            index[scheduled[i]] = i;
        }

        var remaining = new Dictionary<object, int>(InstanceComparer.Instance);
        var dependents = new Dictionary<object, List<object>>(InstanceComparer.Instance);
        foreach (var entity in scheduled)
        {
            var parents = dependencies[entity].Where(p => !ReferenceEquals(p, entity)).Distinct(InstanceComparer.Instance).ToList();
            remaining[entity] = parents.Count;
            foreach (var parent in parents)
            {
                if (!dependents.TryGetValue(parent, out var list))
                {
                    dependents[parent] = list = [];
                }

                list.Add(entity);
            }
        }

        var ordered = new List<object>(scheduled.Count);
        var ready = new SortedSet<(int Rank, int Index)>();
        foreach (var entity in scheduled.Where(e => remaining[e] == 0))
        {
            ready.Add((rank(entity), index[entity]));
        }

        while (ready.Count > 0)
        {
            var next = ready.Min;
            ready.Remove(next);
            var entity = scheduled[next.Index];
            ordered.Add(entity);
            if (!dependents.TryGetValue(entity, out var children))
            {
                continue;
            }

            foreach (var child in children)
            {
                if (--remaining[child] == 0)
                {
                    ready.Add((rank(child), index[child]));
                }
            }
        }

        if (ordered.Count != scheduled.Count)
        {
            var stuck = scheduled.First(e => remaining[e] > 0);
            throw new SimpleOrmException(
                "UOW-004", stuck.GetType().Name,
                $"the scheduled {operation}s reference each other in a cycle through their navigations; break it and flush in two steps");
        }

        return ordered;
    }

    // --- execution -------------------------------------------------------------------

    /// <summary>Before an insert: the keys flow in — from every non-null many-to-one navigation, and from the owner a cascade recorded.</summary>
    private void ApplyReferences(EntityMap map, object entity)
    {
        foreach (var relationship in map.Relationships.Where(r => r.Kind == RelationshipKind.ManyToOne))
        {
            var target = NavigationValue(map, relationship, entity);
            if (target is null)
            {
                continue;
            }

            CopyKey(Maps.Load(relationship.TargetType), target, map, relationship.ForeignKeyProperties, entity);
        }

        foreach (var reference in _pendingReferences.Where(r => ReferenceEquals(r.Child, entity)))
        {
            CopyKey(Maps.Load(reference.Parent.GetType()), reference.Parent, map, reference.ChildForeignKeys, entity);
        }
    }

    private static void CopyKey(EntityMap parentMap, object parent, EntityMap childMap, IReadOnlyList<string> foreignKeys, object child)
    {
        var key = parentMap.GetKeyValues(parent);
        for (var i = 0; i < foreignKeys.Count && i < key.Length; i++)
        {
            var property = childMap.Properties.First(p => p.PropertyName == foreignKeys[i]);
            property.SetValue(child, ConvertKeyPart(key[i], property.ClrType));
        }
    }

    private static object? ConvertKeyPart(object? value, Type target)
    {
        if (value is null || target.IsInstanceOfType(value))
        {
            return value;
        }

        var underlying = Nullable.GetUnderlyingType(target) ?? target;
        return Convert.ChangeType(value, underlying, System.Globalization.CultureInfo.InvariantCulture);
    }

    /// <summary>Every tracked instance with a writable change, not scheduled for removal: types in dependency order, then registration order. A dirty read-only instance is not skipped — its update refuses (<c>CRUD-003</c>), never silently.</summary>
    private List<object> DirtyTrackedInstances()
    {
        var ranks = TypeRanks(_trackedByKey.Keys);
        var dirty = new List<(int Rank, long Sequence, object Entity)>();
        foreach (var byKey in _trackedByKey)
        {
            var map = Maps.Load(byKey.Key);
            foreach (var entry in byKey.Value.Values)
            {
                if (_scheduledRemoveSet.Contains(entry.Entity))
                {
                    continue;
                }

                if (DirtyProperties(map, entry.Entity, entry.Snapshot).Any(p => !p.IsKey && !p.IsVersion && !p.IsGenerated))
                {
                    dirty.Add((ranks[byKey.Key], entry.Sequence, entry.Entity));
                }
            }
        }

        return dirty.OrderBy(d => d.Rank).ThenBy(d => d.Sequence).Select(d => d.Entity).ToList();
    }

    private async Task InvokeWriteAsync(string method, EntityMap map, object entity, CancellationToken ct)
    {
        try
        {
            await ((Task)typeof(Db).GetMethods()
                .Single(m => m.Name == method && m.GetGenericArguments().Length == 1 && m.GetParameters().Length == 2)
                .MakeGenericMethod(map.EntityType)
                .Invoke(this, [entity, ct])!).ConfigureAwait(false);
        }
        catch (TargetInvocationException exception) when (exception.InnerException is not null)
        {
            throw exception.InnerException;
        }
    }

    // --- helpers ---------------------------------------------------------------------

    private void RequireUnitOfWork()
    {
        if (!IsTracking)
        {
            throw new SimpleOrmException(
                "UOW-003", "session",
                "the unit of work needs a tracked session (its snapshots decide what to write); open with DbOptions.Tracking = TrackingMode.Tracked");
        }
    }

    private EntityMap RequireWritableKeyed(Type entityType)
    {
        var map = Maps.Load(entityType);
        if (map.Kind != RelationKind.Table)
        {
            throw new SimpleOrmException(
                "CRUD-003", entityType.Name, $"is {map.Kind}-backed and read-only; writes need a table");
        }

        if (map.KeyProperties.Count == 0)
        {
            throw new SimpleOrmException("CRUD-002", entityType.Name, "the entity defines no key");
        }

        return map;
    }

    private static object? NavigationValue(EntityMap map, RelationshipMap relationship, object entity)
        => map.EntityType.GetProperty(relationship.PropertyName)!.GetValue(entity);

    /// <summary>The initialized members of a navigation: the single target, or a loaded collection's items; null and the unloaded sentinel yield nothing.</summary>
    private static IEnumerable<object> NavigationMembers(EntityMap map, RelationshipMap relationship, object entity)
    {
        var value = NavigationValue(map, relationship, entity);
        if (value is null || value is IUnloadedSentinel)
        {
            yield break;
        }

        if (relationship.Kind is RelationshipKind.OneToMany or RelationshipKind.ManyToMany)
        {
            foreach (var member in (IEnumerable)value)
            {
                yield return member;
            }
        }
        else
        {
            yield return value;
        }
    }

    /// <summary>A key a cascaded child takes from its owner at flush: the child's FK properties, in the owner's key order.</summary>
    private sealed record PendingReference(object Child, object Parent, IReadOnlyList<string> ChildForeignKeys);
}
