using System.Collections.Concurrent;

namespace SimpleOrm;

/// <summary>
/// The single entry point for metadata (§7.2). Loader precedence per type:
/// an explicit <see cref="EntityMapBuilder{T}"/> registration, else the attribute
/// loader when any mapping attribute is present, else the convention loader.
/// Maps are cached per loader instance; a type that fails to load throws
/// <see cref="MappingException"/> with every violation.
/// </summary>
public sealed class EntityMapLoader
{
    private readonly MappingOptions _options;
    private readonly ConcurrentDictionary<Type, EntityMap> _cache = new();

    public EntityMapLoader(MappingOptions? options = null) => _options = options ?? MappingOptions.Default;

    public EntityMap Load<T>()
        where T : class
        => Load(typeof(T));

    public EntityMap Load(Type entityType) => _cache.GetOrAdd(entityType, LoadCore);

    /// <summary>Whether the type carries any mapping attributes (used by tooling such as export-metadata).</summary>
    public static bool HasMappingAttributes(Type entityType) => AttributeMapLoader.HasMappingAttributes(entityType);

    /// <summary>An <c>[Owned]</c> value type (ADR-0030): carries mapping attributes but is never an entity — assembly scans skip it.</summary>
    public static bool IsOwnedType(Type type) => AttributeMapLoader.IsOwnedType(type);

    /// <summary>A trigger class (ADR-0042): carries a <c>[Trigger(name, …)]</c> declaration, is never an entity — assembly scans skip it.</summary>
    public static bool IsTriggerType(Type type) => AttributeMapLoader.IsTriggerType(type);

    /// <summary>
    /// The trigger name a trigger class declares (ADR-0042) — what a migration step
    /// is keyed by even when no entity links the class any more (a removal step).
    /// Throws <see cref="MappingException"/> (<c>MAP-029</c>) for a malformed declaration.
    /// </summary>
    public static string TriggerNameOf(Type triggerType)
    {
        var errors = new List<MappingError>();
        var spec = AttributeMapLoader.ReadTriggerDeclaration(triggerType, triggerType.Name, errors);
        if (spec is null)
        {
            throw new MappingException(triggerType, errors);
        }

        return spec.Name;
    }

    /// <summary>
    /// The scan rule for triggers (ADR-0042, <c>MAP-029</c>): a trigger class linked
    /// by two entities, or two classes declaring one trigger name. One error each.
    /// </summary>
    public static IReadOnlyList<MappingError> TriggerConflicts(IEnumerable<Type> types)
        => AttributeMapLoader.TriggerConflicts(types);

    /// <summary>
    /// The scan rule of ADR-0041 (<c>MAP-028</c>): among the types an assembly scan
    /// considers, a concrete class without a relation source of its own that another
    /// scanned type extends is a base class, not an entity — the one case a lone load
    /// cannot see, because such a class legitimately maps to a convention-named table.
    /// Returns one error per base class, naming the types that extend it.
    /// </summary>
    public static IReadOnlyList<MappingError> BaseClassesUsedAsEntities(IEnumerable<Type> types)
        => AttributeMapLoader.BaseClassesUsedAsEntities(types);

    /// <summary>
    /// The entity types an assembly scan maps — concrete classes carrying mapping
    /// attributes that are not owned types — or a <see cref="MappingException"/> with
    /// every <c>MAP-028</c> when a base class is among them (ADR-0041). Tooling that
    /// enumerates entities (the CLI, the shadow rebuild) goes through this.
    /// </summary>
    public static IReadOnlyList<Type> ScannedEntityTypes(IEnumerable<Type> types)
    {
        var all = types.ToArray();
        var conflicts = AttributeMapLoader.TriggerConflicts(all);
        if (conflicts.Count > 0)
        {
            throw new MappingException(
                all.FirstOrDefault(t => t.Name == conflicts[0].Target) ?? all.First(t => t.IsDefined(typeof(TriggerAttribute), false)), conflicts);
        }

        return AttributeMapLoader.ScannedEntityTypes(all);
    }

    private EntityMap LoadCore(Type entityType)
    {
        if (AttributeMapLoader.IsTriggerType(entityType))
        {
            // ADR-0042: a trigger class declares a trigger; it is read through the entity that links it.
            throw new MappingException(entityType, [new MappingError(
                "MAP-029", entityType.Name, "is a trigger declaration, not an entity; it is read through the entity that links it")]);
        }

        if (entityType.IsAbstract)
        {
            // ADR-0041: a base class shares members only; identity comes from the
            // concrete class alone. Every path that loads a map arrives here.
            throw new MappingException(entityType, [new MappingError(
                "MAP-028", entityType.Name,
                "is abstract: a base class shares members only and is never an entity (ADR-0041); read, write, and query the concrete class")]);
        }

        if (_options.ExplicitMaps.TryGetValue(entityType, out var factory))
        {
            return factory(_options.NamingConvention);
        }

        return AttributeMapLoader.HasMappingAttributes(entityType)
            ? AttributeMapLoader.Load(entityType, _options.NamingConvention)
            : ConventionMapLoader.Load(entityType, _options.NamingConvention);
    }
}
