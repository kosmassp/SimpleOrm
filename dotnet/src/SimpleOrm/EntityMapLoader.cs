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

    private EntityMap LoadCore(Type entityType)
    {
        if (_options.ExplicitMaps.TryGetValue(entityType, out var factory))
        {
            return factory(_options.NamingConvention);
        }

        return AttributeMapLoader.HasMappingAttributes(entityType)
            ? AttributeMapLoader.Load(entityType, _options.NamingConvention)
            : ConventionMapLoader.Load(entityType, _options.NamingConvention);
    }
}
