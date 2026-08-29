using System.Data.Common;
using System.Reflection;

namespace SimpleOrm;

/// <summary>
/// Join-mode eager loading (ADR-0022 add.1): one SELECT with LEFT JOINs, each
/// entity segment materialized through the one mapping pipeline via
/// <see cref="SegmentReader"/>, roots deduplicated by §7.4 identity, children
/// attached with the same semantics as the multi-query path. Strictness where
/// Hibernate famously is not: joins with <c>Limit</c>/<c>Offset</c> refuse
/// (<c>REL-005</c> — never in-memory paging), and joining more than one
/// collection navigation refuses (<c>REL-006</c> — never a silent Cartesian
/// product).
/// </summary>
public sealed partial class Db
{
    internal async Task<IReadOnlyList<TEntity>> EagerJoinLoadAsync<TEntity>(
        SelectAst root, IReadOnlyList<string> navigations, CancellationToken ct)
        where TEntity : class
    {
        var map = root.Map;
        var queryName = typeof(TEntity).Name + " criteria";
        if (map.KeyProperties.Count == 0)
        {
            throw new SimpleOrmException(
                "REL-003", queryName,
                "join-mode eager loading needs a keyed root (§7.4 identity deduplicates the joined rows) — load via MultiQuery");
        }

        var plans = new List<JoinPlan>();
        var joins = new List<SelectJoin>();
        var collections = 0;
        foreach (var navigation in navigations)
        {
            var relationship = ResolveNavigation<TEntity>(map, navigation);
            if (relationship.Kind is RelationshipKind.OneToMany or RelationshipKind.ManyToMany)
            {
                collections++;
            }

            if (collections > 1)
            {
                throw new SimpleOrmException(
                    "REL-006", queryName,
                    "join-mode eager loading joins at most one collection navigation: two would multiply into a Cartesian product — use FetchMode.MultiQuery or SubSelect for the rest");
            }

            plans.Add(BuildJoin(map, relationship, joins, queryName));
        }

        // To-one joins never multiply root rows (they join the full target key),
        // so paging with only to-one includes is sound; a collection include
        // multiplies, and in-memory paging is never acceptable (REL-005).
        if (collections > 0 && (root.Limit is not null || root.Offset is not null))
        {
            throw new SimpleOrmException(
                "REL-005", queryName,
                "join-mode eager loading of a collection navigation cannot page: the join multiplies root rows, so limit/offset would count children — use FetchMode.MultiQuery or SubSelect");
        }

        var joined = new SelectAst(map, root.Where, root.Orderings, root.Limit, root.Offset, joins: joins);
        using var command = _connection.CreateCommand();
        command.Transaction = _transaction;
        var binder = new CommandParameterBinder(command, _converter, queryName);
        command.CommandText = Options.Dialect.SelectSql(joined, binder.Add);

        var results = new List<TEntity>();
        var rootsByKey = new Dictionary<object?[], TEntity>(KeyTupleComparer.Instance);
        // With a single projected navigation the join cannot fan out, so raw
        // per-root row counting applies — REL-002 and duplicate collection rows
        // behave exactly as in the other modes. With several, identity-dedup
        // cancels the cross-navigation fan-out (a same-key source duplicate is
        // then indistinguishable from it; documented in spec/loading.md).
        var dedupFanOut = plans.Count > 1;
        var attachments = plans.Select(p => new Attachment(p, dedupFanOut)).ToArray();
        var markUnloaded = UnloadedNavigations.MarkerFor(typeof(TEntity), Maps);

        var reader = await command.ExecuteReaderAsync(ct).ConfigureAwait(false);
        try
        {
            var rootSegment = new SegmentReader(reader, 0, map.Properties.Count, "t_");
            var rootPlan = _mapper.CreatePlan<TEntity>(rootSegment, queryName);
            var cursor = map.Properties.Count;
            foreach (var attachment in attachments)
            {
                var target = attachment.Plan.Relationship.TargetType;
                var targetMap = Maps.Load(target);
                attachment.Segment = new SegmentReader(
                    reader, cursor, targetMap.Properties.Count, attachment.Plan.TargetAlias + "_");
                attachment.MaterializeChild = ChildPlan(target, attachment.Segment, queryName);
                attachment.KeyOrdinals = targetMap.KeyProperties
                    .Select(k => targetMap.Properties.ToList().FindIndex(p => ReferenceEquals(p, k)))
                    .ToArray();
                attachment.TargetMap = targetMap;
                // Children guard their own unloaded collections (REL-004) exactly
                // like every other materialized entity.
                attachment.MarkChildUnloaded = UnloadedNavigations.MarkerFor(target, Maps);
                cursor += targetMap.Properties.Count;
            }

            var row = 0;
            while (reader.Read())
            {
                var rootEntity = rootPlan(rootSegment);
                var rootKey = map.GetKeyValues(rootEntity);
                if (rootsByKey.TryGetValue(rootKey, out var existing))
                {
                    rootEntity = existing;
                }
                else
                {
                    markUnloaded?.Invoke(rootEntity);
                    rootsByKey[rootKey] = rootEntity;
                    results.Add(rootEntity);
                }

                foreach (var attachment in attachments)
                {
                    attachment.Absorb(rootEntity, rootKey);
                }

                if ((++row & 63) == 0)
                {
                    ct.ThrowIfCancellationRequested();
                }
            }
        }
        finally
        {
            reader.Dispose();
        }

        foreach (var attachment in attachments)
        {
            attachment.Finish(results, map);
        }

        return results;
    }

    /// <summary>The joins for one navigation; many-to-many adds the (unprojected) link hop.</summary>
    private JoinPlan BuildJoin(EntityMap map, RelationshipMap relationship, List<SelectJoin> joins, string queryName)
    {
        var alias = "j" + joins.Count(j => j.Project);
        switch (relationship.Kind)
        {
            case RelationshipKind.ManyToOne:
            {
                var targetMap = Maps.Load(relationship.TargetType);
                RequireShape(
                    targetMap.KeyProperties.Count > 0
                    && targetMap.KeyProperties.Count == relationship.ForeignKeyProperties.Count
                    && relationship.ForeignKeyProperties.All(n => map.Properties.Any(p => p.PropertyName == n)),
                    map, relationship,
                    $"join-mode eager loading needs a keyed '{relationship.TargetType.Name}' and mapped foreign keys — or load via MultiQuery");
                joins.Add(new SelectJoin(
                    targetMap, alias, parentAlias: null,
                    relationship.ForeignKeyProperties
                        .Select((fk, i) => (fk, targetMap.KeyProperties[i].PropertyName))
                        .ToArray(),
                    project: true));
                return new JoinPlan(relationship, alias);
            }

            case RelationshipKind.OneToOne:
            case RelationshipKind.OneToMany:
            {
                var targetMap = Maps.Load(relationship.TargetType);
                RequireShape(
                    targetMap.KeyProperties.Count > 0
                    && relationship.ForeignKeyProperties.All(
                        n => targetMap.Properties.Any(p => p.PropertyName == n))
                    && map.KeyProperties.Count == relationship.ForeignKeyProperties.Count,
                    map, relationship,
                    $"join-mode eager loading needs a keyed '{relationship.TargetType.Name}' with mapped target foreign keys agreeing with this key — or load via MultiQuery");
                joins.Add(new SelectJoin(
                    targetMap, alias, parentAlias: null,
                    map.KeyProperties
                        .Select((k, i) => (k.PropertyName, relationship.ForeignKeyProperties[i]))
                        .ToArray(),
                    project: true));
                return new JoinPlan(relationship, alias);
            }

            default:
            {
                var linkMap = Maps.Load(relationship.LinkType!);
                var targetMap = Maps.Load(relationship.TargetType);
                RequireShape(
                    targetMap.KeyProperties.Count > 0
                    && relationship.LinkForeignKeysToOwner.All(n => linkMap.Properties.Any(p => p.PropertyName == n))
                    && relationship.LinkForeignKeysToTarget.All(n => linkMap.Properties.Any(p => p.PropertyName == n))
                    && map.KeyProperties.Count == relationship.LinkForeignKeysToOwner.Count
                    && targetMap.KeyProperties.Count == relationship.LinkForeignKeysToTarget.Count,
                    map, relationship,
                    $"link foreign keys disagree with the key shapes of '{map.EntityType.Name}'/'{relationship.TargetType.Name}', or the target is keyless — or load via MultiQuery");
                var linkAlias = "l" + joins.Count;
                joins.Add(new SelectJoin(
                    linkMap, linkAlias, parentAlias: null,
                    map.KeyProperties
                        .Select((k, i) => (k.PropertyName, relationship.LinkForeignKeysToOwner[i]))
                        .ToArray(),
                    project: false));
                joins.Add(new SelectJoin(
                    targetMap, alias, parentAlias: linkAlias,
                    relationship.LinkForeignKeysToTarget
                        .Select((fk, i) => (fk, targetMap.KeyProperties[i].PropertyName))
                        .ToArray(),
                    project: true));
                return new JoinPlan(relationship, alias);
            }
        }
    }

    internal RelationshipMap ResolveNavigation<TEntity>(EntityMap map, string navigation)
        => map.Relationships.FirstOrDefault(
            r => string.Equals(r.PropertyName, navigation, StringComparison.Ordinal))
            ?? throw new SimpleOrmException(
                "REL-001", typeof(TEntity).Name,
                $"'{navigation}' is not a declared navigation "
                + $"(declared: {(map.Relationships.Count == 0 ? "none" : string.Join(", ", map.Relationships.Select(r => r.PropertyName)))})");

    private Func<DbDataReader, object> ChildPlan(Type entityType, SegmentReader segment, string queryName)
    {
        var method = typeof(Db).GetMethod(nameof(TypedChildPlan), BindingFlags.NonPublic | BindingFlags.Instance)!
            .MakeGenericMethod(entityType);
        try
        {
            return (Func<DbDataReader, object>)method.Invoke(this, [segment, queryName])!;
        }
        catch (TargetInvocationException exception) when (exception.InnerException is not null)
        {
            throw exception.InnerException;   // plan-build errors keep their codes (MAP-003 etc.)
        }
    }

    private Func<DbDataReader, object> TypedChildPlan<T>(SegmentReader segment, string queryName)
    {
        var plan = _mapper.CreatePlan<T>(segment, queryName);
        return r => plan(r)!;
    }

    private sealed class JoinPlan(RelationshipMap relationship, string targetAlias)
    {
        public RelationshipMap Relationship { get; } = relationship;

        public string TargetAlias { get; } = targetAlias;
    }

    /// <summary>Per-navigation absorption state: shared child instances, per-root lists, one-to-one duplicate detection.</summary>
    private sealed class Attachment(Db.JoinPlan plan, bool dedupFanOut)
    {
        public JoinPlan Plan { get; } = plan;

        public SegmentReader Segment = null!;

        public Func<DbDataReader, object> MaterializeChild = null!;

        public int[] KeyOrdinals = null!;

        public EntityMap TargetMap = null!;

        public Action<object>? MarkChildUnloaded;

        private readonly Dictionary<object?[], object> _children = new(KeyTupleComparer.Instance);
        private readonly Dictionary<object?[], List<object>> _byRoot = new(KeyTupleComparer.Instance);
        private readonly Dictionary<object?[], HashSet<object?[]>> _seenByRoot = new(KeyTupleComparer.Instance);

        public void Absorb(object rootEntity, object?[] rootKey)
        {
            // LEFT JOIN: an absent child is all-NULL key columns in the segment.
            if (KeyOrdinals.Any(Segment.IsDBNull))
            {
                if (!_byRoot.ContainsKey(rootKey))
                {
                    _byRoot[rootKey] = [];
                    _seenByRoot[rootKey] = new HashSet<object?[]>(KeyTupleComparer.Instance);
                }

                return;
            }

            var child = MaterializeChild(Segment);
            var childKey = TargetMap.GetKeyValues(child);
            if (_children.TryGetValue(childKey, out var existing))
            {
                child = existing;   // shared instance per identity (§7.4)
            }
            else
            {
                MarkChildUnloaded?.Invoke(child);
                _children[childKey] = child;
            }

            if (!_byRoot.TryGetValue(rootKey, out var list))
            {
                _byRoot[rootKey] = list = [];
                _seenByRoot[rootKey] = new HashSet<object?[]>(KeyTupleComparer.Instance);
            }

            // With one navigation the join cannot fan out: every row is a real
            // source row, so raw counting keeps duplicate-key rows and lets
            // REL-002 fire exactly as in the other modes.
            if (!dedupFanOut || _seenByRoot[rootKey].Add(childKey))
            {
                list.Add(child);
            }
        }

        public void Finish<TEntity>(IReadOnlyList<TEntity> roots, EntityMap map)
            where TEntity : class
        {
            var navigation = map.EntityType.GetProperty(Plan.Relationship.PropertyName)!;
            foreach (var root in roots)
            {
                _byRoot.TryGetValue(map.GetKeyValues(root), out var children);
                children ??= [];
                switch (Plan.Relationship.Kind)
                {
                    case RelationshipKind.ManyToOne:
                    case RelationshipKind.OneToOne:
                        if (Plan.Relationship.Kind == RelationshipKind.OneToOne && children.Count > 1)
                        {
                            throw new SimpleOrmException(
                                "REL-002", $"{map.EntityType.Name}.{Plan.Relationship.PropertyName}",
                                $"one-to-one matched {children.Count} rows; the target foreign key needs a unique index");
                        }

                        navigation.SetValue(root, children.Count > 0 ? children[0] : null);
                        break;
                    default:
                        children.Sort((left, right) => CompareKeyTuples(
                            TargetMap.GetKeyValues(left), TargetMap.GetKeyValues(right)));
                        navigation.SetValue(root, TypedList(Plan.Relationship.TargetType, children));
                        break;
                }
            }
        }
    }
}
