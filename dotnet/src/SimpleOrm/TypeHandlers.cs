using System.Text.Json;

namespace SimpleOrm;

/// <summary>
/// Converts between a CLR type and its database representation (§7.9): the
/// extension point for anything outside the fixed conversion table. No
/// reflection-based guessing — unregistered types fail with <c>MAP-030</c>.
/// </summary>
public interface ITypeHandler<T>
{
    /// <summary>Database value → CLR value. Never receives NULL.</summary>
    T Parse(object databaseValue);

    /// <summary>CLR value → database value (a type the provider stores natively).</summary>
    object Format(T value);
}

/// <summary>Per-options registry of <see cref="ITypeHandler{T}"/> instances.</summary>
public sealed class TypeHandlerRegistry
{
    private readonly Dictionary<Type, (Func<object, object?> Parse, Func<object, object> Format)> _handlers = [];

    public TypeHandlerRegistry Register<T>(ITypeHandler<T> handler)
    {
        _handlers[typeof(T)] = (
            value => handler.Parse(value),
            value => handler.Format((T)value)!);
        return this;
    }

    /// <summary>
    /// Registers <typeparamref name="T"/> as a JSON column (TEXT holding JSON, §7.10)
    /// via System.Text.Json. Default options: snake_case names, case-insensitive,
    /// numbers readable from strings (SQLite TEXT affinity).
    /// </summary>
    public TypeHandlerRegistry Json<T>(JsonSerializerOptions? options = null)
        => Register(new JsonTypeHandler<T>(options ?? JsonTypeHandler<T>.DefaultOptions));

    internal bool Contains(Type type) => _handlers.ContainsKey(type);

    internal bool TryParse(Type type, object databaseValue, out object? value)
    {
        if (_handlers.TryGetValue(type, out var handler))
        {
            value = handler.Parse(databaseValue);
            return true;
        }

        value = null;
        return false;
    }

    internal bool TryFormat(object value, out object databaseValue)
    {
        if (_handlers.TryGetValue(value.GetType(), out var handler))
        {
            databaseValue = handler.Format(value);
            return true;
        }

        databaseValue = value;
        return false;
    }
}

/// <summary>The built-in JSON column handler (§7.10).</summary>
internal sealed class JsonTypeHandler<T>(JsonSerializerOptions options) : ITypeHandler<T>
{
    public static readonly JsonSerializerOptions DefaultOptions = new()
    {
        PropertyNamingPolicy = JsonNamingPolicy.SnakeCaseLower,
        PropertyNameCaseInsensitive = true,
        NumberHandling = System.Text.Json.Serialization.JsonNumberHandling.AllowReadingFromString,
    };

    public T Parse(object databaseValue)
        => JsonSerializer.Deserialize<T>((string)databaseValue, options)!;

    public object Format(T value)
        => JsonSerializer.Serialize(value, options);
}
