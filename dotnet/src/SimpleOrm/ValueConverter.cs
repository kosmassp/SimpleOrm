using System.Globalization;

namespace SimpleOrm;

/// <summary>
/// Milestone 3 interim conversions for reading SQLite values into CLR types.
/// Milestone 4 replaces this with the fixed conversion table + ITypeHandler
/// registry and the VAL/MAP error codes; until then failures throw with context.
/// </summary>
internal static class ValueConverter
{
    public static object? FromDatabase(object? value, Type targetType, string context)
    {
        if (value is null or DBNull)
        {
            if (!targetType.IsValueType || Nullable.GetUnderlyingType(targetType) is not null)
            {
                return null;
            }

            throw new InvalidOperationException($"{context}: NULL cannot convert to non-nullable {targetType.Name}");
        }

        var target = Nullable.GetUnderlyingType(targetType) ?? targetType;
        if (target.IsInstanceOfType(value))
        {
            return value;
        }

        try
        {
            if (target.IsEnum)
            {
                return value is string name
                    ? Enum.Parse(target, name, ignoreCase: true)
                    : Enum.ToObject(target, value);
            }

            if (target == typeof(int))
            {
                return Convert.ToInt32(value, CultureInfo.InvariantCulture);
            }

            if (target == typeof(long))
            {
                return Convert.ToInt64(value, CultureInfo.InvariantCulture);
            }

            if (target == typeof(short))
            {
                return Convert.ToInt16(value, CultureInfo.InvariantCulture);
            }

            if (target == typeof(decimal))
            {
                return Convert.ToDecimal(value, CultureInfo.InvariantCulture);
            }

            if (target == typeof(double))
            {
                return Convert.ToDouble(value, CultureInfo.InvariantCulture);
            }

            if (target == typeof(float))
            {
                return Convert.ToSingle(value, CultureInfo.InvariantCulture);
            }

            if (target == typeof(bool))
            {
                return Convert.ToBoolean(value, CultureInfo.InvariantCulture);
            }

            if (target == typeof(string))
            {
                return Convert.ToString(value, CultureInfo.InvariantCulture);
            }

            if (target == typeof(DateTime) && value is string dateText)
            {
                return DateTime.Parse(dateText, CultureInfo.InvariantCulture, DateTimeStyles.RoundtripKind);
            }

            if (target == typeof(DateTimeOffset) && value is string offsetText)
            {
                return DateTimeOffset.Parse(offsetText, CultureInfo.InvariantCulture, DateTimeStyles.RoundtripKind);
            }

            if (target == typeof(Guid))
            {
                return value switch
                {
                    string guidText => Guid.Parse(guidText),
                    byte[] bytes => new Guid(bytes),
                    _ => throw Unconvertible(value, target, context),
                };
            }

#if NET
            if (target == typeof(DateOnly) && value is string dateOnlyText)
            {
                return DateOnly.Parse(dateOnlyText, CultureInfo.InvariantCulture);
            }

            if (target == typeof(TimeOnly) && value is string timeOnlyText)
            {
                return TimeOnly.Parse(timeOnlyText, CultureInfo.InvariantCulture);
            }
#endif
        }
        catch (Exception exception) when (exception is FormatException or OverflowException or ArgumentException)
        {
            throw new InvalidOperationException(
                $"{context}: cannot convert '{value}' ({value.GetType().Name}) to {target.Name}", exception);
        }

        throw Unconvertible(value, target, context);
    }

    /// <summary>Converts a CLR value for binding as a parameter (§7.9 storage conventions).</summary>
    public static object ToDatabase(object? value)
    {
        switch (value)
        {
            case null:
                return DBNull.Value;
            case Enum enumValue:
                return enumValue.ToString();
            case DateTime dateTime:
                var utc = dateTime.Kind == DateTimeKind.Local ? dateTime.ToUniversalTime() : dateTime;
                return utc.ToString("o", CultureInfo.InvariantCulture);
            case DateTimeOffset offset:
                return offset.ToString("o", CultureInfo.InvariantCulture);
#if NET
            case DateOnly date:
                return date.ToString("O", CultureInfo.InvariantCulture);
            case TimeOnly time:
                return time.ToString("O", CultureInfo.InvariantCulture);
#endif
            default:
                return value;
        }
    }

    private static InvalidOperationException Unconvertible(object value, Type target, string context)
        => new($"{context}: no conversion from {value.GetType().Name} to {target.Name} (custom types need an ITypeHandler, milestone 4)");
}
