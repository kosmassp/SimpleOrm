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
        var names = EntityNames.For(typeof(TEntity));
        var queryName = names.Criteria;

        // Refusal precedence (spec/loading.md Clarifications, ADR-0033): REL-001
        // (the caller, before any mode), then REL-006/REL-005 — the request's
        // shape — then REL-003, the metadata's shape.
        var relationships = navigations.Select(n => ResolveNavigation<TEntity>(map, n)).ToArray();
        var collections = relationships.Count(r => r.Kind is RelationshipKind.OneToMany or RelationshipKind.ManyToMany);
        if (collections > 1)
        {
            throw new SimpleOrmException(
                "REL-006", queryName,
                "join-mode eager loading joins at most one collection navigation: two would multiply into a Cartesian product — use FetchMode.MultiQuery or SubSelect for the rest");
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

        if (map.KeyProperties.Count == 0)
        {
            throw new SimpleOrmException(
                "REL-003", queryName,
                "join-mode eager loading needs a keyed root (§7.4 identity deduplicates the joined rows) — load via MultiQuery");
        }

        var plans = new List<JoinPlan>();
        var joins = new List<SelectJoin>();
        foreach (var relationship in relationships)
        {
            plans.Add(BuildJoin(map, relationship, joins, queryName));
        }

        var joined = new SelectAst(map, root.Where, root.Orderings, root.Limit, root.Offset, joins: joins);
        using var command = NewCommand();
        command.Transaction = _transaction;
        var binder = new CommandParameterBinder(command, _converter, names);
        command.CommandText = Options.Dialect.SelectSql(joined, binder.Add);

        var results = new List<TEntity>();
        var rootsByKey = new Dictionary<JoinKey, TEntity>();
        // With a single projected navigation the join cannot fan out, so raw
        // per-root row counting applies — REL-002 and duplicate collection rows
        // behave exactly as in the other modes. With several, identity-dedup
        // cancels the cross-navigation fan-out (a same-key source duplicate is
        // then indistinguishable from it; documented in spec/loading.md).
        var dedupFanOut = plans.Count > 1;
        var attachments = plans.Select(p => new Attachment(p, dedupFanOut)).ToArray();
        var markUnloaded = UnloadedNavigations.MarkerFor(typeof(TEntity), Maps);

        LogStatement(names.Include, command);
        var reader = await command.ExecuteReaderAsync(ct).ConfigureAwait(false);
        try
        {
            var rootSegment = new SegmentReader(reader, 0, map.Properties.Count, "t_");
            var rootPlan = _mapper.CreatePlan<TEntity>(rootSegment, queryName);
            var rootKeys = JoinKeyReader.For(
                map, rootSegment,
                map.KeyProperties.Select(k => IndexOf(map.Properties, k)).ToArray());
            var cursor = map.Properties.Count;
            foreach (var attachment in attachments)
            {
                var target = attachment.Plan.Relationship.TargetType;
                var targetMap = Maps.Load(target);
                attachment.Segment = new SegmentReader(
                    reader, cursor, targetMap.Properties.Count, attachment.Plan.TargetAlias + "_");
                attachment.MaterializeChild = ChildPlan(target, attachment.Segment, queryName);
                attachment.KeyOrdinals = targetMap.KeyProperties
                    .Select(k => IndexOf(targetMap.Properties, k))
                    .ToArray();
                attachment.TargetMap = targetMap;
                attachment.ChildKeys = JoinKeyReader.For(targetMap, attachment.Segment, attachment.KeyOrdinals);
                attachment.RootKeys = rootKeys;
                // Children guard their own unloaded collections (REL-004) exactly
                // like every other materialized entity.
                attachment.MarkChildUnloaded = UnloadedNavigations.MarkerFor(target, Maps);
                attachment.TrackChild = TrackerFor(target);
                cursor += targetMap.Properties.Count;
            }

            var row = 0;
            while (reader.Read())
            {
                // A root repeats once per child row: with an integer key read from
                // the row (ADR-0048), a repeat is recognized before it is
                // materialized again; otherwise the materialized key decides.
                TEntity rootEntity;
                JoinKey rootKey;
                if (rootKeys.ReadsRow(rootSegment))
                {
                    rootKey = rootKeys.FromRow(rootSegment);
                    if (rootsByKey.TryGetValue(rootKey, out var known))
                    {
                        rootEntity = known;
                    }
                    else
                    {
                        rootEntity = rootPlan(rootSegment);
                        markUnloaded?.Invoke(rootEntity);
                        rootEntity = (TEntity)Track(map, rootEntity);
                        rootsByKey[rootKey] = rootEntity;
                        results.Add(rootEntity);
                    }
                }
                else
                {
                    rootEntity = rootPlan(rootSegment);
                    rootKey = rootKeys.FromEntity(rootEntity);
                    if (rootsByKey.TryGetValue(rootKey, out var existing))
                    {
                        rootEntity = existing;
                    }
                    else
                    {
                        markUnloaded?.Invoke(rootEntity);
                        rootEntity = (TEntity)Track(map, rootEntity);
                        rootsByKey[rootKey] = rootEntity;
                        results.Add(rootEntity);
                    }
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

        /// <summary>The session's identity map for the child type (Level 3 M1), or null when untracked.</summary>
        public Func<object, object>? TrackChild;

        /// <summary>How the child's identity key is read (ADR-0048): from the row for a single integer key, else from the materialized child.</summary>
        public JoinKeyReader ChildKeys = null!;

        /// <summary>The root's key reader, for <see cref="Finish{TEntity}"/>'s lookups by root instance.</summary>
        public JoinKeyReader RootKeys = null!;

        private readonly Dictionary<JoinKey, object> _children = new();
        private readonly Dictionary<JoinKey, List<object>> _byRoot = new();
        private readonly Dictionary<JoinKey, HashSet<JoinKey>> _seenByRoot = new();   // only with fan-out deduplication

        public void Absorb(object rootEntity, JoinKey rootKey)
        {
            // LEFT JOIN: an absent child is all-NULL key columns in the segment.
            var absent = false;
            foreach (var ordinal in KeyOrdinals)
            {
                if (Segment.IsDBNull(ordinal))
                {
                    absent = true;
                    break;
                }
            }

            if (absent)
            {
                if (!_byRoot.ContainsKey(rootKey))
                {
                    _byRoot[rootKey] = [];
                }

                return;
            }

            // A child repeated across rows (a to-one target, a fan-out) is the
            // shared instance per identity (§7.4); with an integer key read from
            // the row, a repeat is recognized before it is materialized again.
            object child;
            JoinKey childKey;
            if (ChildKeys.ReadsRow(Segment))
            {
                childKey = ChildKeys.FromRow(Segment);
                child = _children.TryGetValue(childKey, out var known) ? known : Adopt(MaterializeChild(Segment), childKey);
            }
            else
            {
                child = MaterializeChild(Segment);
                childKey = ChildKeys.FromEntity(child);
                child = _children.TryGetValue(childKey, out var existing) ? existing : Adopt(child, childKey);
            }

            if (!_byRoot.TryGetValue(rootKey, out var list))
            {
                _byRoot[rootKey] = list = [];
            }

            // With one navigation the join cannot fan out: every row is a real
            // source row, so raw counting keeps duplicate-key rows and lets
            // REL-002 fire exactly as in the other modes.
            if (!dedupFanOut)
            {
                list.Add(child);
                return;
            }

            if (!_seenByRoot.TryGetValue(rootKey, out var seen))
            {
                _seenByRoot[rootKey] = seen = [];
            }

            if (seen.Add(childKey))
            {
                list.Add(child);
            }
        }

        /// <summary>A child seen for the first time: guarded, tracked, remembered.</summary>
        private object Adopt(object child, JoinKey childKey)
        {
            MarkChildUnloaded?.Invoke(child);
            if (TrackChild is not null)
            {
                child = TrackChild(child);
            }

            _children[childKey] = child;
            return child;
        }

        public void Finish<TEntity>(IReadOnlyList<TEntity> roots, EntityMap map)
            where TEntity : class
        {
            var navigation = map.EntityType.GetProperty(Plan.Relationship.PropertyName)!;
            foreach (var root in roots)
            {
                _byRoot.TryGetValue(RootKeys.FromEntity(root), out var children);
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
                        // Always: the join orders roots, not each root's children.
                        SortByKey(children, TargetMap);
                        navigation.SetValue(root, TypedList(Plan.Relationship.TargetType, children));
                        break;
                }
            }
        }
    }

    private static int IndexOf(IReadOnlyList<PropertyMap> properties, PropertyMap property)
    {
        for (var i = 0; i < properties.Count; i++)
        {
            if (ReferenceEquals(properties[i], property))
            {
                return i;
            }
        }

        return -1;
    }

    /// <summary>
    /// A join-mode identity key (ADR-0048): a single integer key part as a
    /// <see cref="long"/>, read from the row without boxing; any other key as the
    /// boxed tuple under the §7.4 structural comparison. The two forms never
    /// compare equal to each other, and one reader produces one form per value
    /// (a null key part always takes the tuple form, from the materialized entity).
    /// </summary>
    internal readonly struct JoinKey : IEquatable<JoinKey>
    {
        private readonly long _value;
        private readonly object?[]? _tuple;

        public JoinKey(long value)
        {
            _value = value;
            _tuple = null;
        }

        public JoinKey(object?[] tuple)
        {
            _value = 0;
            _tuple = tuple;
        }

        public bool Equals(JoinKey other)
            => _tuple is null
                ? other._tuple is null && _value == other._value
                : other._tuple is not null && KeyTupleComparer.Instance.Equals(_tuple, other._tuple);

        public override bool Equals(object? obj) => obj is JoinKey other && Equals(other);

        public override int GetHashCode() => _tuple is null ? _value.GetHashCode() : KeyTupleComparer.Instance.GetHashCode(_tuple);
    }

    /// <summary>
    /// Reads one map's identity key in a join (ADR-0048). The typed form applies
    /// when the key is one integer part (byte, short, int, long) and the segment
    /// column at its ordinal — named as the key column — is an integer field: the
    /// row's value is then the key the materialized entity would carry, so equal
    /// keys are equal either way. Otherwise every key comes from the entity, as
    /// the tuple, exactly as before.
    /// </summary>
    internal sealed class JoinKeyReader
    {
        private readonly EntityMap _map;
        private readonly int _ordinal;
        private readonly Type? _fieldType;

        private JoinKeyReader(EntityMap map, int ordinal, Type? fieldType)
        {
            _map = map;
            _ordinal = ordinal;
            _fieldType = fieldType;
        }

        public static JoinKeyReader For(EntityMap map, DbDataReader segment, int[] keyOrdinals)
        {
            if (map.KeyProperties.Count == 1 && keyOrdinals.Length == 1 && keyOrdinals[0] >= 0
                && IntegerKeyType(map.KeyProperties[0]) is not null
                && string.Equals(segment.GetName(keyOrdinals[0]), map.KeyProperties[0].ColumnName, StringComparison.OrdinalIgnoreCase))
            {
                var fieldType = segment.GetFieldType(keyOrdinals[0]);
                if (fieldType == typeof(long) || fieldType == typeof(int) || fieldType == typeof(short) || fieldType == typeof(byte))
                {
                    return new JoinKeyReader(map, keyOrdinals[0], fieldType);
                }
            }

            return new JoinKeyReader(map, -1, null);
        }

        /// <summary>Whether this row's key reads from the row: the typed form, and a non-null key cell.</summary>
        public bool ReadsRow(DbDataReader segment) => _fieldType is not null && !segment.IsDBNull(_ordinal);

        public JoinKey FromRow(DbDataReader segment)
        {
            if (_fieldType == typeof(long))
            {
                return new JoinKey(segment.GetInt64(_ordinal));
            }

            if (_fieldType == typeof(int))
            {
                return new JoinKey(segment.GetInt32(_ordinal));
            }

            return _fieldType == typeof(short) ? new JoinKey(segment.GetInt16(_ordinal)) : new JoinKey(segment.GetByte(_ordinal));
        }

        public JoinKey FromEntity(object entity)
        {
            if (_fieldType is not null && _map.KeyProperties[0].GetValue(entity) is { } value)
            {
                return new JoinKey(ToInt64(value));
            }

            return new JoinKey(_map.GetKeyValues(entity));
        }
    }
}
