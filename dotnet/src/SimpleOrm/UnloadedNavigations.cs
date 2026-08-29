using System.Collections;
using System.Collections.Concurrent;
using System.Linq.Expressions;
using System.Reflection;

namespace SimpleOrm;

/// <summary>
/// The unloaded-collection sentinel (ADR-0021 add.2, owner directive): an entity
/// **read from the database** gets its collection navigations set to a list that
/// throws <c>REL-004</c> on any access — the foreign keys prove related rows may
/// exist, so reading a navigation nobody loaded is a bug, not an empty result.
/// Loading (explicit, batch, or eager) replaces the sentinel with the real list.
/// Entities constructed by user code keep their own initializers (a new entity
/// genuinely has nothing). Singular navigations stay null until loaded — a plain
/// property cannot throw on read without proxies, which §2 forbids; after
/// loading, null means a null FK or a dead link.
/// </summary>
internal static class UnloadedNavigations
{
    private static readonly ConcurrentDictionary<Type, Action<object>?> Markers = new();

    /// <summary>The marker that flags a freshly materialized entity's collection navigations, or null when the type has none.</summary>
    public static Action<object>? MarkerFor(Type type, EntityMapLoader maps)
        => Markers.GetOrAdd(type, t => Build(t, maps));

    private static Action<object>? Build(Type type, EntityMapLoader maps)
    {
        if (!AttributeMapLoader.HasMappingAttributes(type))
        {
            return null;   // plain result records never carry navigations
        }

        var map = maps.Load(type);
        var assignments = new List<Expression>();
        var parameter = Expression.Parameter(typeof(object), "entity");
        var typed = Expression.Variable(type, "typedEntity");
        foreach (var relationship in map.Relationships.Where(
            r => r.Kind is RelationshipKind.OneToMany or RelationshipKind.ManyToMany))
        {
            var property = type.GetProperty(relationship.PropertyName)!;
            var sentinel = CreateSentinel(relationship.TargetType, type.Name, relationship.PropertyName);
            if (!property.PropertyType.IsInstanceOfType(sentinel))
            {
                continue;   // a concrete-list-typed navigation cannot hold the sentinel; it keeps its initializer
            }

            assignments.Add(Expression.Call(
                typed,
                property.GetSetMethod(nonPublic: true)!,
                Expression.Constant(sentinel, property.PropertyType)));
        }

        if (assignments.Count == 0)
        {
            return null;
        }

        var body = Expression.Block(
            [typed],
            new Expression[] { Expression.Assign(typed, Expression.Convert(parameter, type)) }
                .Concat(assignments));
        return Expression.Lambda<Action<object>>(body, parameter).Compile();
    }

    private static object CreateSentinel(Type elementType, string entityName, string propertyName)
        => Activator.CreateInstance(
            typeof(UnloadedList<>).MakeGenericType(elementType), entityName, propertyName)!;
}

/// <summary>Throws <c>REL-004</c> on any read; one shared instance per (entity type, navigation).</summary>
internal sealed class UnloadedList<T>(string entityName, string propertyName) : IReadOnlyList<T>
{
    public int Count => throw Unloaded();

    public T this[int index] => throw Unloaded();

    public IEnumerator<T> GetEnumerator() => throw Unloaded();

    IEnumerator IEnumerable.GetEnumerator() => throw Unloaded();

    private SimpleOrmException Unloaded()
        => new(
            "REL-004", $"{entityName}.{propertyName}",
            "this navigation was not loaded — Include it in the query or call LoadAsync/LoadEachAsync; navigations never load implicitly");
}
