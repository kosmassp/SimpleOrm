namespace SimpleOrm;

/// <summary>
/// Configuration for metadata loading: the naming convention (default snake_case)
/// and explicit <see cref="EntityMapBuilder{T}"/> registrations, which take
/// precedence over attributes (§7.2: explicit → attribute → convention).
/// </summary>
public sealed class MappingOptions
{
    /// <summary>Shared default options: snake_case, no explicit registrations.</summary>
    public static MappingOptions Default { get; } = new();

    internal Dictionary<Type, Func<INamingConvention, EntityMap>> ExplicitMaps { get; } = [];

    public INamingConvention NamingConvention { get; set; } = SnakeCaseNamingConvention.Instance;

    /// <summary>Registers a manual map for <typeparamref name="T"/>, overriding its attributes and conventions.</summary>
    public MappingOptions Register<T>(EntityMapBuilder<T> builder)
        where T : class
    {
        if (typeof(T).IsAbstract)
        {
            // ADR-0041: a base class is never an entity, so it cannot be registered as one.
            throw new MappingException(typeof(T), [new MappingError(
                "MAP-028", typeof(T).Name,
                "is abstract: a base class shares members only and is never an entity (ADR-0041); register the concrete class")]);
        }

        ExplicitMaps[typeof(T)] = builder.Build;
        return this;
    }
}
