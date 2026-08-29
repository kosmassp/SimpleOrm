using System.Collections;
using System.Reflection;

namespace SimpleOrm;

/// <summary>
/// Explicit and batch relationship loading (Level 2 milestone 3, ADR-0021).
/// Nothing loads implicitly (§2, ADR-0019 add.1): a navigation stays empty/null
/// until one of these calls populates it, and touching an unloaded navigation
/// never fires SQL. Every call is a visible, bounded set of round trips — one
/// criteria query per navigation (two for many-to-many: link rows, then targets),
/// chunked only when a batch exceeds the parameter budget.
///
/// Key/FK tuples match by **structural equality of their values** — the §7.4
/// entity-identity rule — never by string tokens (ADR-0021 add.1: lossy
/// stringification silently loaded the wrong entity for DateTime and byte[]
/// keys). Collection results order by the target key, compared value-wise.
/// </summary>
public sealed partial class Db
{
    /// <summary>How many owners bind into one query before chunking (SQLite's parameter budget).</summary>
    private const int LoadChunkSize = 500;

    /// <summary>Loads one declared navigation of one entity (ADR-0021).</summary>
    public Task LoadAsync<TEntity>(TEntity entity, string navigation, CancellationToken ct)
        where TEntity : class
        => LoadEachAsync<TEntity>([entity], navigation, ct);

    /// <summary>
    /// The batch form (ADR-0019 M3): loads one declared navigation for every
    /// entity in the list with one query per chunk — never one per entity. Within
    /// one call, owners sharing a many-to-one target share the same loaded
    /// instance.
    /// </summary>
    public Task LoadEachAsync<TEntity>(IReadOnlyList<TEntity> entities, string navigation, CancellationToken ct)
        where TEntity : class
        => LoadEachAsync(entities, navigation, ownerSubquery: null, ct);

    /// <summary>
    /// With <paramref name="ownerSubquery"/> (ADR-0022 add.1, SubSelect mode) the
    /// owner set is expressed as <c>IN (select …)</c> over the root query instead
    /// of a client-side key list — one query per navigation, no chunking.
    /// </summary>
    internal async Task LoadEachAsync<TEntity>(
        IReadOnlyList<TEntity> entities, string navigation, SelectAst? ownerSubquery, CancellationToken ct)
        where TEntity : class
    {
        // The navigation validates even for an empty batch: a wrong name is a
        // bug regardless of how many entities happened to be in the list.
        var map = Maps.Load<TEntity>();
        var relationship = ResolveNavigation<TEntity>(map, navigation);
        if (entities.Count == 0)
        {
            return;
        }

        var property = map.EntityType.GetProperty(navigation)!;
        switch (relationship.Kind)
        {
            case RelationshipKind.ManyToOne:
                await LoadManyToOneAsync(map, relationship, property, entities, ownerSubquery, ct).ConfigureAwait(false);
                break;
            case RelationshipKind.OneToOne:
            case RelationshipKind.OneToMany:
                await LoadFromTargetForeignKeyAsync(map, relationship, property, entities, ownerSubquery, ct).ConfigureAwait(false);
                break;
            default:
                await LoadManyToManyAsync(map, relationship, property, entities, ownerSubquery, ct).ConfigureAwait(false);
                break;
        }
    }

    private async Task LoadManyToOneAsync<TEntity>(
        EntityMap map, RelationshipMap relationship, PropertyInfo navigation,
        IReadOnlyList<TEntity> entities, SelectAst? ownerSubquery, CancellationToken ct)
        where TEntity : class
    {
        var targetMap = Maps.Load(relationship.TargetType);
        var foreignKeys = relationship.ForeignKeyProperties
            .Select(name => map.Properties.First(p => p.PropertyName == name))
            .ToArray();
        RequireShape(
            targetMap.KeyProperties.Count == foreignKeys.Length, map, relationship,
            $"declares {foreignKeys.Length} foreign-key propert{(foreignKeys.Length == 1 ? "y" : "ies")} but '{relationship.TargetType.Name}' has a {targetMap.KeyProperties.Count}-part key");

        // Distinct FK tuples; an owner with any null FK part keeps a null navigation.
        var wanted = new Dictionary<object?[], object?[]>(KeyTupleComparer.Instance);
        foreach (var entity in entities)
        {
            var tuple = foreignKeys.Select(p => p.Property.GetValue(entity)).ToArray();
            if (tuple.All(v => v is not null))
            {
                wanted[tuple] = tuple;
            }
        }

        var loaded = new Dictionary<object?[], object>(KeyTupleComparer.Instance);
        foreach (var filter in OwnerFilters(
            targetMap.KeyProperties, wanted.Keys.ToArray(), ownerSubquery, foreignKeys))
        {
            var rows = await RowsAsync(
                relationship.TargetType, [filter], targetMap.KeyProperties, ct).ConfigureAwait(false);
            foreach (var row in rows)
            {
                loaded[targetMap.GetKeyValues(row)] = row;
            }
        }

        foreach (var entity in entities)
        {
            var tuple = foreignKeys.Select(p => p.Property.GetValue(entity)).ToArray();
            navigation.SetValue(
                entity,
                tuple.All(v => v is not null) && loaded.TryGetValue(tuple, out var target)
                    ? target
                    : null);
        }
    }

    private async Task LoadFromTargetForeignKeyAsync<TEntity>(
        EntityMap map, RelationshipMap relationship, PropertyInfo navigation,
        IReadOnlyList<TEntity> entities, SelectAst? ownerSubquery, CancellationToken ct)
        where TEntity : class
    {
        var targetMap = Maps.Load(relationship.TargetType);
        var targetForeignKeys = relationship.ForeignKeyProperties
            .Select(name => targetMap.Properties.FirstOrDefault(p => p.PropertyName == name))
            .ToArray();
        RequireShape(
            targetForeignKeys.All(p => p is not null), map, relationship,
            $"target foreign-key properties are not all mapped columns of '{relationship.TargetType.Name}'");
        RequireShape(
            map.KeyProperties.Count == targetForeignKeys.Length, map, relationship,
            $"declares {targetForeignKeys.Length} target foreign-key propert{(targetForeignKeys.Length == 1 ? "y" : "ies")} but this entity has a {map.KeyProperties.Count}-part key");

        var ownerKeys = entities.Select(e => map.GetKeyValues(e)).ToArray();
        var byOwner = new Dictionary<object?[], List<object>>(KeyTupleComparer.Instance);
        foreach (var filter in OwnerFilters(
            targetForeignKeys!, DistinctComplete(ownerKeys), ownerSubquery, map.KeyProperties.ToArray()))
        {
            var rows = await RowsAsync(
                relationship.TargetType, [filter], targetMap.KeyProperties, ct).ConfigureAwait(false);
            foreach (var row in rows)
            {
                var owner = targetForeignKeys.Select(p => p!.Property.GetValue(row)).ToArray();
                if (!byOwner.TryGetValue(owner, out var list))
                {
                    byOwner[owner] = list = [];
                }

                list.Add(row);
            }
        }

        for (var i = 0; i < entities.Count; i++)
        {
            List<object>? rows = null;
            if (ownerKeys[i].All(v => v is not null))
            {
                byOwner.TryGetValue(ownerKeys[i], out rows);
            }

            if (relationship.Kind == RelationshipKind.OneToOne)
            {
                if (rows is { Count: > 1 })
                {
                    throw new SimpleOrmException(
                        "REL-002", $"{map.EntityType.Name}.{relationship.PropertyName}",
                        $"one-to-one matched {rows.Count} rows for key ({string.Join(", ", ownerKeys[i])}); the target foreign key needs a unique index");
                }

                navigation.SetValue(entities[i], rows is { Count: 1 } ? rows[0] : null);
            }
            else
            {
                // Rows arrive SQL-ordered by target key and grouped stably.
                navigation.SetValue(entities[i], TypedList(relationship.TargetType, rows ?? []));
            }
        }
    }

    private async Task LoadManyToManyAsync<TEntity>(
        EntityMap map, RelationshipMap relationship, PropertyInfo navigation,
        IReadOnlyList<TEntity> entities, SelectAst? ownerSubquery, CancellationToken ct)
        where TEntity : class
    {
        var linkMap = Maps.Load(relationship.LinkType!);
        var targetMap = Maps.Load(relationship.TargetType);
        var toOwner = relationship.LinkForeignKeysToOwner
            .Select(name => linkMap.Properties.FirstOrDefault(p => p.PropertyName == name))
            .ToArray();
        var toTarget = relationship.LinkForeignKeysToTarget
            .Select(name => linkMap.Properties.FirstOrDefault(p => p.PropertyName == name))
            .ToArray();
        RequireShape(
            toOwner.All(p => p is not null) && toTarget.All(p => p is not null), map, relationship,
            $"link foreign-key properties are not all mapped columns of '{relationship.LinkType!.Name}'");
        RequireShape(
            map.KeyProperties.Count == toOwner.Length && targetMap.KeyProperties.Count == toTarget.Length,
            map, relationship,
            $"link foreign-key counts disagree with the key shapes of '{map.EntityType.Name}'/'{relationship.TargetType.Name}'");

        // First visible query: the link rows for these owners.
        var ownerKeys = entities.Select(e => map.GetKeyValues(e)).ToArray();
        var targetKeysByOwner = new Dictionary<object?[], List<object?[]>>(KeyTupleComparer.Instance);
        foreach (var filter in OwnerFilters(
            toOwner!, DistinctComplete(ownerKeys), ownerSubquery, map.KeyProperties.ToArray()))
        {
            var links = await RowsAsync(
                relationship.LinkType!, [filter], linkMap.KeyProperties, ct).ConfigureAwait(false);
            foreach (var link in links)
            {
                var owner = toOwner.Select(p => p!.Property.GetValue(link)).ToArray();
                if (!targetKeysByOwner.TryGetValue(owner, out var keys))
                {
                    targetKeysByOwner[owner] = keys = [];
                }

                keys.Add(toTarget.Select(p => p!.Property.GetValue(link)).ToArray());
            }
        }

        // Second visible query: the targets those links reference. A link row
        // whose target row does not exist contributes nothing — the loaded
        // collection reflects existing rows, exactly as a join would (ADR-0021
        // add.1; referential integrity is the database's story, not loading's).
        var distinctTargetKeys = new Dictionary<object?[], object?[]>(KeyTupleComparer.Instance);
        foreach (var key in targetKeysByOwner.Values.SelectMany(k => k))
        {
            distinctTargetKeys[key] = key;
        }

        var targets = new Dictionary<object?[], object>(KeyTupleComparer.Instance);
        foreach (var chunk in Chunks(distinctTargetKeys.Keys.ToArray()))
        {
            var rows = await RowsAsync(
                relationship.TargetType,
                [KeyMembership(targetMap.KeyProperties, chunk)],
                targetMap.KeyProperties, ct).ConfigureAwait(false);
            foreach (var row in rows)
            {
                targets[targetMap.GetKeyValues(row)] = row;
            }
        }

        for (var i = 0; i < entities.Count; i++)
        {
            var matched = new List<object>();
            if (ownerKeys[i].All(v => v is not null)
                && targetKeysByOwner.TryGetValue(ownerKeys[i], out var keys))
            {
                var seen = new HashSet<object?[]>(KeyTupleComparer.Instance);
                foreach (var key in keys)
                {
                    if (targets.TryGetValue(key, out var target) && seen.Add(key))
                    {
                        matched.Add(target);
                    }
                }

                // Order by the target key, compared value-wise — never by a
                // string rendering (ADR-0021 add.1: "10" sorts before "2").
                matched.Sort((left, right) => CompareKeyTuples(
                    targetMap.GetKeyValues(left), targetMap.GetKeyValues(right)));
            }

            navigation.SetValue(entities[i], TypedList(relationship.TargetType, matched));
        }
    }

    // --- shared plumbing ------------------------------------------------------------

    /// <summary>Membership over one or many key parts: single-part uses IN; composite ORs per-tuple ANDed equalities.</summary>
    private static Criteria KeyMembership(IReadOnlyList<PropertyMap?> parts, IReadOnlyList<object?[]> tuples)
        => parts.Count == 1
            ? Criteria.In(parts[0]!.PropertyName, tuples.Select(t => t[0]!))
            : Criteria.Or(tuples
                .Select(t => Criteria.And(parts.Select((p, i) => Criteria.Eq(p!.PropertyName, t[i])).ToArray()))
                .ToArray());

    /// <summary>
    /// The owner-set filters for one related-side query: chunked key-list
    /// membership normally; a single <c>IN (select …)</c> over the root query in
    /// SubSelect mode (ADR-0022 add.1) — the subquery projects
    /// <paramref name="subqueryProjection"/> (the root's FK or key properties)
    /// and keeps the root's where/orderings/paging.
    /// </summary>
    private static IEnumerable<Criteria> OwnerFilters(
        IReadOnlyList<PropertyMap?> parts,
        IReadOnlyList<object?[]> tuples,
        SelectAst? ownerSubquery,
        IReadOnlyList<PropertyMap> subqueryProjection)
    {
        if (ownerSubquery is not null)
        {
            // Orderings matter to a subquery only when paging trims it; without
            // limit/offset they are dead weight (and dialect-divergent noise).
            var paged = ownerSubquery.Limit is not null || ownerSubquery.Offset is not null;
            yield return Criteria.InSelect(
                parts.Select(p => p!.PropertyName).ToArray(),
                new SelectAst(
                    ownerSubquery.Map, ownerSubquery.Where,
                    paged ? ownerSubquery.Orderings : [],
                    ownerSubquery.Limit, ownerSubquery.Offset, projection: subqueryProjection));
            yield break;
        }

        foreach (var chunk in Chunks(tuples))
        {
            yield return KeyMembership(parts, chunk);
        }
    }

    /// <summary>Loads rows of a runtime entity type through the criteria pipeline, ordered by the given key for determinism.</summary>
    private async Task<IReadOnlyList<object>> RowsAsync(
        Type entityType, IReadOnlyList<Criteria> where, IReadOnlyList<PropertyMap> orderBy, CancellationToken ct)
    {
        var method = typeof(Db).GetMethod(nameof(TypedRowsAsync), BindingFlags.NonPublic | BindingFlags.Instance)!
            .MakeGenericMethod(entityType);
        try
        {
            return await ((Task<IReadOnlyList<object>>)method.Invoke(this, [where, orderBy, ct])!).ConfigureAwait(false);
        }
        catch (TargetInvocationException exception) when (exception.InnerException is not null)
        {
            throw exception.InnerException;
        }
    }

    private async Task<IReadOnlyList<object>> TypedRowsAsync<T>(
        IReadOnlyList<Criteria> where, IReadOnlyList<PropertyMap> orderBy, CancellationToken ct)
        where T : class
    {
        var query = Query<T>().Where([.. where]);
        foreach (var key in orderBy)
        {
            query = query.OrderBy(key.PropertyName);
        }

        var rows = await query.ToListAsync(ct).ConfigureAwait(false);
        return [.. rows];
    }

    /// <summary>Distinct complete tuples: owners with a null key part are excluded from querying (their navigations stay empty/null).</summary>
    private static IReadOnlyList<object?[]> DistinctComplete(IReadOnlyList<object?[]> tuples)
    {
        var distinct = new Dictionary<object?[], object?[]>(KeyTupleComparer.Instance);
        foreach (var tuple in tuples)
        {
            if (tuple.All(v => v is not null))
            {
                distinct[tuple] = tuple;
            }
        }

        return distinct.Keys.ToArray();
    }

    private static IEnumerable<IReadOnlyList<object?[]>> Chunks(IReadOnlyList<object?[]> tuples)
    {
        for (var start = 0; start < tuples.Count; start += LoadChunkSize)
        {
            yield return tuples.Skip(start).Take(LoadChunkSize).ToArray();
        }
    }

    private static IList TypedList(Type elementType, IReadOnlyList<object> rows)
    {
        var list = (IList)Activator.CreateInstance(typeof(List<>).MakeGenericType(elementType))!;
        foreach (var row in rows)
        {
            list.Add(row);
        }

        return list;
    }

    /// <summary>
    /// Value-wise key ordering (parts are the same CLR type within one target and
    /// implement IComparable). Strings compare **ordinally** — matching SQLite's
    /// BINARY collation, so client-side ordering agrees with SQL ORDER BY —
    /// and byte[] compares lexicographically.
    /// </summary>
    private static int CompareKeyTuples(IReadOnlyList<object?> left, IReadOnlyList<object?> right)
    {
        for (var i = 0; i < left.Count; i++)
        {
            var comparison = (left[i], right[i]) switch
            {
                (byte[] lb, byte[] rb) => CompareBytes(lb, rb),
                (string ls, string rs) => string.CompareOrdinal(ls, rs),
                var (l, r) => Comparer<object?>.Default.Compare(l, r),
            };
            if (comparison != 0)
            {
                return comparison;
            }
        }

        return 0;
    }

    private static int CompareBytes(byte[] left, byte[] right)
    {
        var shared = Math.Min(left.Length, right.Length);
        for (var i = 0; i < shared; i++)
        {
            var comparison = left[i].CompareTo(right[i]);
            if (comparison != 0)
            {
                return comparison;
            }
        }

        return left.Length.CompareTo(right.Length);
    }

    private static void RequireShape(bool valid, EntityMap map, RelationshipMap relationship, string message)
    {
        if (!valid)
        {
            throw new SimpleOrmException(
                "REL-003", $"{map.EntityType.Name}.{relationship.PropertyName}", message);
        }
    }

    /// <summary>
    /// Structural equality for key/FK tuples — the §7.4 identity rule
    /// (<see cref="EntityMap.KeysEqual"/>) applied to raw tuples: element-wise
    /// <see cref="object.Equals(object)"/>, never a string rendering.
    /// </summary>
    private sealed class KeyTupleComparer : IEqualityComparer<object?[]>
    {
        public static readonly KeyTupleComparer Instance = new();

        public bool Equals(object?[]? x, object?[]? y)
        {
            if (ReferenceEquals(x, y))
            {
                return true;
            }

            if (x is null || y is null || x.Length != y.Length)
            {
                return false;
            }

            for (var i = 0; i < x.Length; i++)
            {
                // byte[] keys compare by content — object.Equals would be
                // reference equality and every blob key would miss its match.
                var equal = x[i] is byte[] xb && y[i] is byte[] yb
                    ? xb.Length == yb.Length && xb.SequenceEqual(yb)
                    : object.Equals(x[i], y[i]);
                if (!equal)
                {
                    return false;
                }
            }

            return true;
        }

        public int GetHashCode(object?[] tuple)
        {
            unchecked
            {
                var hash = 17;
                foreach (var value in tuple)
                {
                    var valueHash = value switch
                    {
                        null => 0,
                        byte[] bytes => bytes.Aggregate(19, (h, b) => (h * 31) + b),
                        _ => value.GetHashCode(),
                    };
                    hash = (hash * 31) + valueHash;
                }

                return hash;
            }
        }
    }
}
