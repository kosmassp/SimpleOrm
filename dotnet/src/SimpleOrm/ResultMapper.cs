using System.Data.Common;
using System.Reflection;

namespace SimpleOrm;

/// <summary>
/// Milestone 3 row mapper: one pipeline for all results (§7.11), reflection-based.
/// Entity types (any mapping attributes) map through their <see cref="EntityMap"/>
/// column names — so [Column] overrides apply to raw SQL results too. Other types
/// (DTOs, join rows) construct via the best-matching constructor or settable
/// properties, columns matched name-insensitively ignoring underscores.
/// Milestone 4 replaces the internals with compiled, cached mappers and the strict
/// MAP-001/002/003 behavior; milestone 8 makes them fast.
/// </summary>
internal sealed class ResultMapper
{
    private readonly EntityMapLoader _loader;

    public ResultMapper(EntityMapLoader loader) => _loader = loader;

    /// <summary>Builds a row-materializer for the reader's current column set.</summary>
    public Func<DbDataReader, T> CreatePlan<T>(DbDataReader reader, string queryName)
    {
        var type = typeof(T);
        if (IsScalar(type))
        {
            return r => (T)ValueConverter.FromDatabase(r.GetValue(0), type, queryName)!;
        }

        var columns = new string[reader.FieldCount];
        for (var i = 0; i < reader.FieldCount; i++)
        {
            columns[i] = reader.GetName(i);
        }

        return AttributeMapLoader.HasMappingAttributes(type)
            ? EntityPlan<T>(columns, queryName)
            : DtoPlan<T>(columns, queryName);
    }

    private Func<DbDataReader, T> EntityPlan<T>(string[] columns, string queryName)
    {
        var map = _loader.Load(typeof(T));
        var setters = new PropertyInfo[columns.Length];
        for (var i = 0; i < columns.Length; i++)
        {
            var property = map.Properties.FirstOrDefault(
                p => string.Equals(p.ColumnName, columns[i], StringComparison.OrdinalIgnoreCase));
            if (property is null)
            {
                throw new SimpleOrmException(
                    "MAP-001", queryName,
                    $"result column '{columns[i]}' has no mapped property on {typeof(T).Name}");
            }

            setters[i] = property.Property;
        }

        return reader =>
        {
            var instance = Activator.CreateInstance<T>();
            for (var i = 0; i < setters.Length; i++)
            {
                var context = $"{queryName} → {typeof(T).Name}.{setters[i].Name}";
                setters[i].SetValue(instance, ValueConverter.FromDatabase(reader.GetValue(i), setters[i].PropertyType, context));
            }

            return instance;
        };
    }

    private static Func<DbDataReader, T> DtoPlan<T>(string[] columns, string queryName)
    {
        // Prefer the constructor whose parameters all match columns (records).
        foreach (var constructor in typeof(T).GetConstructors().OrderByDescending(c => c.GetParameters().Length))
        {
            var parameters = constructor.GetParameters();
            if (parameters.Length == 0 || parameters.Length != columns.Length)
            {
                continue;
            }

            var ordinals = new int[parameters.Length];
            var matched = true;
            for (var i = 0; i < parameters.Length; i++)
            {
                ordinals[i] = Array.FindIndex(columns, c => NamesMatch(c, parameters[i].Name!));
                if (ordinals[i] < 0)
                {
                    matched = false;
                    break;
                }
            }

            if (!matched)
            {
                continue;
            }

            var parameterTypes = parameters.Select(p => p.ParameterType).ToArray();
            return reader =>
            {
                var values = new object?[parameterTypes.Length];
                for (var i = 0; i < parameterTypes.Length; i++)
                {
                    var context = $"{queryName} → {typeof(T).Name}({parameters[i].Name})";
                    values[i] = ValueConverter.FromDatabase(reader.GetValue(ordinals[i]), parameterTypes[i], context);
                }

                return (T)constructor.Invoke(values);
            };
        }

        // Fall back to settable properties.
        var properties = typeof(T).GetProperties(BindingFlags.Public | BindingFlags.Instance)
            .Where(p => p.SetMethod is not null)
            .ToArray();
        var setters = new PropertyInfo[columns.Length];
        for (var i = 0; i < columns.Length; i++)
        {
            var property = properties.FirstOrDefault(p => NamesMatch(columns[i], p.Name));
            setters[i] = property ?? throw new SimpleOrmException(
                "MAP-001", queryName,
                $"result column '{columns[i]}' matches no constructor parameter or settable property on {typeof(T).Name}");
        }

        return reader =>
        {
            var instance = Activator.CreateInstance<T>();
            for (var i = 0; i < setters.Length; i++)
            {
                var context = $"{queryName} → {typeof(T).Name}.{setters[i].Name}";
                setters[i].SetValue(instance, ValueConverter.FromDatabase(reader.GetValue(i), setters[i].PropertyType, context));
            }

            return instance;
        };
    }

    /// <summary>Case- and underscore-insensitive: <c>created_at</c> matches <c>CreatedAt</c>.</summary>
    private static bool NamesMatch(string column, string member)
        => string.Equals(
            column.Replace("_", string.Empty),
            member.Replace("_", string.Empty),
            StringComparison.OrdinalIgnoreCase);

    private static bool IsScalar(Type type)
    {
        var underlying = Nullable.GetUnderlyingType(type) ?? type;
        return underlying.IsPrimitive || underlying.IsEnum
            || underlying == typeof(string) || underlying == typeof(decimal) || underlying == typeof(Guid)
            || underlying == typeof(DateTime) || underlying == typeof(DateTimeOffset)
            || underlying == typeof(byte[]);
    }
}
