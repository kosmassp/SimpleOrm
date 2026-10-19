using System.Data;
using System.Data.Common;
using System.Globalization;

namespace SimpleOrm;

/// <summary>
/// The fixed conversion table of §7.9 plus the handler registry — the only two ways
/// a value crosses the database boundary; no reflection-based guessing. Handlers
/// win over the fixed table. Failures carry codes: <c>MAP-030</c> (no rule),
/// <c>MAP-031</c> (rule failed for the value), <c>VAL-020</c> (the UTC rule:
/// stored datetimes must carry a UTC/offset marker; bound DateTimes must not be
/// Kind.Unspecified).
/// </summary>
internal sealed class TypeConverter(TypeHandlerRegistry handlers, bool bindsTemporalsNatively = false)
{
    public bool HasHandler(Type type) => handlers.Contains(type);

    public object? FromDatabase(object? value, Type targetType, string context)
    {
        if (value is null or DBNull)
        {
            if (!targetType.IsValueType || Nullable.GetUnderlyingType(targetType) is not null)
            {
                return null;
            }

            throw new SimpleOrmException(
                "MAP-031", context, $"NULL cannot convert to non-nullable {targetType.Name}");
        }

        var target = Nullable.GetUnderlyingType(targetType) ?? targetType;
        if (handlers.TryParse(target, value, out var handled))
        {
            return handled;
        }

        // Provider-native temporal values (ADR-0024): SQL Server returns DateTime
        // and DateTimeOffset instances where SQLite returns TEXT. The §7.9 UTC rule
        // applies to both shapes — a datetime that carries no UTC/offset marker
        // (Kind=Unspecified; SQL Server's datetime/datetime2) refuses, exactly like
        // markerless TEXT; datetimeoffset is the marked storage. An ITypeHandler
        // that declares the column's actual kind is the per-application escape.
        switch (value)
        {
            case DateTime { Kind: DateTimeKind.Unspecified }
                when target == typeof(DateTime) || target == typeof(DateTimeOffset):
                throw new SimpleOrmException(
                    "VAL-020", context,
                    "stored datetime carries no UTC/offset marker (Kind=Unspecified); store datetimeoffset, or register an ITypeHandler declaring the column's kind");
            case DateTime dateTime when target == typeof(DateTime):
                return dateTime.Kind == DateTimeKind.Utc ? dateTime : dateTime.ToUniversalTime();
            case DateTime dateTime when target == typeof(DateTimeOffset):
                return new DateTimeOffset(dateTime.ToUniversalTime());
            case DateTimeOffset offset when target == typeof(DateTime):
                return offset.UtcDateTime;
#if NET
            case DateTime date when target == typeof(DateOnly):
                return DateOnly.FromDateTime(date);   // date columns carry no kind by design
            case TimeSpan time when target == typeof(TimeOnly):
                return TimeOnly.FromTimeSpan(time);
#endif
        }

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
                    : Enum.ToObject(target, Convert.ToInt64(value, CultureInfo.InvariantCulture));
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
                return ParseUtc(dateText, context).UtcDateTime;
            }

            if (target == typeof(DateTimeOffset) && value is string offsetText)
            {
                return ParseUtc(offsetText, context);
            }

            if (target == typeof(Guid))
            {
                return value switch
                {
                    string guidText => Guid.Parse(guidText),
                    byte[] bytes => new Guid(bytes),
                    _ => throw NoRule(value, target, context),
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
            throw new SimpleOrmException(
                "MAP-031", context,
                $"cannot convert '{value}' ({value.GetType().Name}) to {target.Name}: {exception.Message}");
        }

        throw NoRule(value, target, context);
    }

    /// <summary>
    /// CLR value → database value (§7.9 storage conventions). Handlers win; unknown
    /// types are <c>MAP-030</c>. <paramref name="enumAsInt"/> reflects the mapped
    /// column's [EnumAsInt] flag (parameter binding always stores enum names).
    /// </summary>
    public object ToDatabase(object? value, string context, bool enumAsInt = false)
    {
        if (value is null)
        {
            return DBNull.Value;
        }

        if (handlers.TryFormat(value, out var formatted))
        {
            return formatted;
        }

        switch (value)
        {
            case Enum enumValue:
                return enumAsInt
                    ? Convert.ToInt64(enumValue, CultureInfo.InvariantCulture)
                    : enumValue.ToString();
            // Temporals bind as ISO-8601 strings (§7.9, the SQLite TEXT convention)
            // or, on a dialect whose provider wants native CLR values (ADR-0025
            // Postgres), UTC-normalized as-is; the Kind=Unspecified refusal is the
            // same rule either way.
            case DateTime dateTime:
                var utc = dateTime.Kind switch
                {
                    DateTimeKind.Utc => dateTime,
                    DateTimeKind.Local => dateTime.ToUniversalTime(),
                    _ => throw new SimpleOrmException(
                        "VAL-020", context,
                        "DateTime with Kind=Unspecified cannot be stored; use Kind=Utc (Local is converted)"),
                };
                return bindsTemporalsNatively ? utc : utc.ToString("o", CultureInfo.InvariantCulture);
            case DateTimeOffset offset:
                return bindsTemporalsNatively
                    ? offset.ToUniversalTime()   // providers require offset zero for timestamptz
                    : offset.ToString("o", CultureInfo.InvariantCulture);
#if NET
            case DateOnly date:
                return bindsTemporalsNatively ? date : date.ToString("O", CultureInfo.InvariantCulture);
            case TimeOnly time:
                return bindsTemporalsNatively ? time : time.ToString("O", CultureInfo.InvariantCulture);
#endif
            case bool or byte or short or int or long or float or double or decimal or string or byte[] or Guid or char:
                return value;
            default:
                throw new SimpleOrmException(
                    "MAP-030", context,
                    $"no conversion or handler stores a {value.GetType().Name}; register an ITypeHandler (§7.9)");
        }
    }

    /// <summary>
    /// Binds one value onto a parameter: the <see cref="ToDatabase"/> form, and for a null the
    /// provider type its non-null value would have carried (<see cref="NullDbType"/>) — a
    /// typed null, as the ports bind (ADR-0040 add.2). Without it SqlClient declares an
    /// untyped NULL as <c>nvarchar</c>, which SQL Server refuses into <c>varbinary</c>.
    /// </summary>
    public void BindValue(DbParameter parameter, object? value, string context, bool enumAsInt, Type clrType)
    {
        var bound = ToDatabase(value, context, enumAsInt);
        parameter.Value = bound;
        if (bound is DBNull && NullDbType(clrType) is { } dbType)
        {
            parameter.DbType = dbType;
        }
    }

    /// <summary>
    /// The provider type a null parameter declares for a property of the given CLR type: the
    /// <see cref="DbType"/> of what <see cref="ToDatabase"/> would have produced for a value
    /// (<see cref="ArrayElementStorageType"/>). Null when unknowable — a handler-registered type.
    /// </summary>
    public DbType? NullDbType(Type clrType)
    {
        var storage = ArrayElementStorageType(clrType);
        if (storage == typeof(byte[])) return DbType.Binary;
        if (storage == typeof(string)) return DbType.String;
        if (storage == typeof(bool)) return DbType.Boolean;
        if (storage == typeof(byte)) return DbType.Byte;
        if (storage == typeof(short)) return DbType.Int16;
        if (storage == typeof(int)) return DbType.Int32;
        if (storage == typeof(long)) return DbType.Int64;
        if (storage == typeof(float)) return DbType.Single;
        if (storage == typeof(double)) return DbType.Double;
        if (storage == typeof(decimal)) return DbType.Decimal;
        if (storage == typeof(Guid)) return DbType.Guid;
        if (storage == typeof(DateTime)) return DbType.DateTime;
        if (storage == typeof(DateTimeOffset)) return DbType.DateTimeOffset;
#if NET
        if (storage == typeof(DateOnly)) return DbType.Date;
        if (storage == typeof(TimeOnly)) return DbType.Time;
#endif
        return null;
    }

    /// <summary>
    /// The CLR type <see cref="ToDatabase"/> produces for an element of the given
    /// declared type — what an array parameter's element type must be (ADR-0025:
    /// an empty collection still binds as a <b>typed</b> empty array, because
    /// <c>bigint = any(text[])</c> refuses even with no elements). Handler types
    /// are unknowable statically and fall back to <see cref="object"/>.
    /// </summary>
    public Type ArrayElementStorageType(Type elementType)
    {
        var type = Nullable.GetUnderlyingType(elementType) ?? elementType;
        if (handlers.Contains(type))
        {
            return typeof(object);
        }

        if (type.IsEnum)
        {
            return typeof(string);   // parameter binding always stores enum names (§7.9)
        }

        if (type == typeof(DateTime) || type == typeof(DateTimeOffset))
        {
            return bindsTemporalsNatively ? type : typeof(string);
        }

#if NET
        if (type == typeof(DateOnly) || type == typeof(TimeOnly))
        {
            return bindsTemporalsNatively ? type : typeof(string);
        }
#endif

        return type;
    }

    /// <summary>The §7.9 date rule: a stored datetime must carry a UTC marker ('Z') or an explicit offset.</summary>
    private static DateTimeOffset ParseUtc(string text, string context)
    {
        var trimmed = text.TrimEnd();
        var hasMarker = trimmed.EndsWith("Z", StringComparison.OrdinalIgnoreCase)
            || System.Text.RegularExpressions.Regex.IsMatch(trimmed, @"[+-]\d{2}:\d{2}$");
        if (!hasMarker)
        {
            throw new SimpleOrmException(
                "VAL-020", context,
                $"stored datetime '{text}' has no UTC/offset marker; the convention is ISO-8601 UTC with a trailing Z");
        }

        return DateTimeOffset.Parse(trimmed, CultureInfo.InvariantCulture, DateTimeStyles.RoundtripKind).ToUniversalTime();
    }

    private static SimpleOrmException NoRule(object value, Type target, string context)
        => new(
            "MAP-030", context,
            $"no conversion or handler from {value.GetType().Name} to {target.Name}; register an ITypeHandler (§7.9)");
}
