using System.Collections.Concurrent;
using System.Data.Common;
using System.Linq.Expressions;
using System.Reflection;

namespace SimpleOrm;

/// <summary>
/// The one row-mapping pipeline (§7.11): mapper delegates are built per
/// (result type, column set) with expression trees, cached, and shared by raw SQL,
/// statement entities, and generated reads. Reflection happens only at build time.
///
/// Strictness (§7.7/§7.8): entity results must match their EntityMap exactly —
/// an unknown result column is <c>MAP-001</c>, a mapped property without a column
/// is <c>MAP-002</c>. DTOs construct via the §7.8 algorithm: the constructor whose
/// parameters all match columns wins (ties are <c>MAP-003</c>), leftovers go to
/// settable properties, a <c>required</c> member without a column is <c>MAP-002</c>.
/// </summary>
internal sealed class ResultMapper(EntityMapLoader loader, TypeConverter converter)
{
    private readonly ConcurrentDictionary<string, Delegate> _plans = new();

    public Func<DbDataReader, T> CreatePlan<T>(DbDataReader reader, string queryName)
    {
        var columns = new string[reader.FieldCount];
        for (var i = 0; i < reader.FieldCount; i++)
        {
            columns[i] = reader.GetName(i);
        }

        var key = typeof(T).FullName + "|" + string.Join(",", columns);
        return (Func<DbDataReader, T>)_plans.GetOrAdd(key, _ => BuildPlan<T>(columns, queryName));
    }

    private Func<DbDataReader, T> BuildPlan<T>(string[] columns, string queryName)
    {
        if (IsScalar(typeof(T)))
        {
            return CompileScalar<T>(queryName);
        }

        return AttributeMapLoader.HasMappingAttributes(typeof(T))
            ? Compile<T>(EntityBindings<T>(columns, queryName), queryName)
            : Compile<T>(DtoBindings<T>(columns, queryName), queryName);
    }

    private Func<DbDataReader, T> CompileScalar<T>(string queryName)
    {
        var reader = Expression.Parameter(typeof(DbDataReader), "reader");
        var body = ReadCell(reader, new Binding { Ordinal = 0, TargetType = typeof(T), TargetName = "scalar" }, typeof(T), queryName);
        return Expression.Lambda<Func<DbDataReader, T>>(body, reader).Compile();
    }

    /// <summary>One column bound to either a constructor parameter or a member.</summary>
    private sealed class Binding
    {
        public int Ordinal;
        public Type TargetType = typeof(object);
        public string TargetName = string.Empty;
        public PropertyInfo? Property;          // null when consumed by the constructor
    }

    private sealed class Plan
    {
        public ConstructorInfo Constructor = null!;
        public List<Binding> ConstructorBindings = [];
        public List<Binding> MemberBindings = [];
    }

    private Plan EntityBindings<T>(string[] columns, string queryName)
    {
        var map = loader.Load(typeof(T));
        var byColumn = new PropertyMap[columns.Length];
        for (var i = 0; i < columns.Length; i++)
        {
            byColumn[i] = map.Properties.FirstOrDefault(
                    p => string.Equals(p.ColumnName, columns[i], StringComparison.OrdinalIgnoreCase))
                ?? throw new SimpleOrmException(
                    "MAP-001", queryName, $"result column '{columns[i]}' has no mapped property on {typeof(T).Name}");
        }

        var missing = map.Properties
            .Where(p => !byColumn.Contains(p))
            .Select(p => p.ColumnName)
            .ToArray();
        if (missing.Length > 0)
        {
            throw new SimpleOrmException(
                "MAP-002", queryName,
                $"{typeof(T).Name} expects column(s) {string.Join(", ", missing)} which the result does not contain");
        }

        return ResolveConstructor<T>(
            columns.Select((_, i) => new Binding
            {
                Ordinal = i,
                TargetName = byColumn[i].PropertyName,
                TargetType = byColumn[i].ClrType,
                Property = byColumn[i].Property,
            }).ToList(),
            requiredCheck: false,
            queryName);
    }

    private static Plan DtoBindings<T>(string[] columns, string queryName)
    {
        var settable = typeof(T).GetProperties(BindingFlags.Public | BindingFlags.Instance)
            .Where(p => p.SetMethod is not null)
            .ToArray();

        var bindings = new List<Binding>(columns.Length);
        for (var i = 0; i < columns.Length; i++)
        {
            var property = settable.FirstOrDefault(p => NamesMatch(columns[i], p.Name));
            bindings.Add(new Binding
            {
                Ordinal = i,
                TargetName = property?.Name ?? columns[i],
                TargetType = property?.PropertyType ?? typeof(object),
                Property = property,
            });
        }

        return ResolveConstructor<T>(bindings, requiredCheck: true, queryName);
    }

    /// <summary>The §7.8 construction algorithm over pre-matched column bindings.</summary>
    private static Plan ResolveConstructor<T>(List<Binding> bindings, bool requiredCheck, string queryName)
    {
        var candidates = new List<(ConstructorInfo Ctor, Binding[] Bound)>();
        foreach (var ctor in typeof(T).GetConstructors())
        {
            var parameters = ctor.GetParameters();
            if (parameters.Length == 0)
            {
                continue;
            }

            var bound = new Binding[parameters.Length];
            var allMatched = true;
            for (var i = 0; i < parameters.Length; i++)
            {
                var match = bindings.FirstOrDefault(b => NamesMatch(b.TargetName, parameters[i].Name!));
                if (match is null)
                {
                    allMatched = false;
                    break;
                }

                bound[i] = new Binding
                {
                    Ordinal = match.Ordinal,
                    TargetName = parameters[i].Name!,
                    TargetType = parameters[i].ParameterType,
                    Property = match.Property,
                };
            }

            if (allMatched)
            {
                candidates.Add((ctor, bound));
            }
        }

        var plan = new Plan();
        if (candidates.Count > 0)
        {
            var maxArity = candidates.Max(c => c.Bound.Length);
            var best = candidates.Where(c => c.Bound.Length == maxArity).ToArray();
            if (best.Length > 1)
            {
                throw new SimpleOrmException(
                    "MAP-003", queryName,
                    $"{typeof(T).Name} has {best.Length} constructors with {maxArity} matching parameters; construction is ambiguous");
            }

            plan.Constructor = best[0].Ctor;
            plan.ConstructorBindings = best[0].Bound.ToList();
        }
        else
        {
            plan.Constructor = typeof(T).GetConstructor(Type.EmptyTypes)
                ?? throw new SimpleOrmException(
                    "MAP-003", queryName,
                    $"{typeof(T).Name} has no constructor matching the result columns and no parameterless constructor");
        }

        var consumed = new HashSet<int>(plan.ConstructorBindings.Select(b => b.Ordinal));
        foreach (var binding in bindings.Where(b => !consumed.Contains(b.Ordinal)))
        {
            if (binding.Property is null)
            {
                throw new SimpleOrmException(
                    "MAP-001", queryName,
                    $"result column '{binding.TargetName}' matches no constructor parameter or settable property on {typeof(T).Name}");
            }

            plan.MemberBindings.Add(binding);
        }

        if (requiredCheck)
        {
            var boundProperties = plan.MemberBindings.Select(b => b.Property!.Name)
                .Concat(plan.ConstructorBindings.Select(b => b.TargetName))
                .ToArray();
            var unboundRequired = typeof(T).GetProperties(BindingFlags.Public | BindingFlags.Instance)
                .Where(p => p.CustomAttributes.Any(
                    a => a.AttributeType.FullName == "System.Runtime.CompilerServices.RequiredMemberAttribute"))
                .Where(p => !boundProperties.Any(n => NamesMatch(n, p.Name)))
                .Select(p => p.Name)
                .ToArray();
            if (unboundRequired.Length > 0)
            {
                throw new SimpleOrmException(
                    "MAP-002", queryName,
                    $"required member(s) {string.Join(", ", unboundRequired)} of {typeof(T).Name} have no matching result column");
            }
        }

        return plan;
    }

    private Func<DbDataReader, T> Compile<T>(Plan plan, string queryName)
    {
        var reader = Expression.Parameter(typeof(DbDataReader), "reader");

        Expression Read(Binding binding) => ReadCell(reader, binding, typeof(T), queryName);

        var instance = Expression.Variable(typeof(T), "instance");
        var body = new List<Expression>
        {
            Expression.Assign(
                instance,
                Expression.New(plan.Constructor, plan.ConstructorBindings.Select(Read))),
        };

        foreach (var binding in plan.MemberBindings)
        {
            var property = binding.Property!;
            if (IsInitOnly(property))
            {
                // init-only setters carry a modreq; assign via reflection instead.
                var setValue = typeof(PropertyInfo).GetMethod(nameof(PropertyInfo.SetValue), [typeof(object), typeof(object)])!;
                body.Add(Expression.Call(
                    Expression.Constant(property), setValue,
                    Expression.Convert(instance, typeof(object)),
                    Expression.Convert(Read(binding), typeof(object))));
            }
            else
            {
                body.Add(Expression.Assign(Expression.Property(instance, property), Read(binding)));
            }
        }

        body.Add(instance);
        return Expression.Lambda<Func<DbDataReader, T>>(
            Expression.Block([instance], body), reader).Compile();
    }

    /// <summary>
    /// One cell read (milestone 8): provider-native types compile to typed getters
    /// (no boxing, no converter dispatch); everything with rules — dates (the UTC
    /// rule), enums, handler-registered types — stays on the converter path.
    /// </summary>
    private Expression ReadCell(ParameterExpression reader, Binding binding, Type resultOwner, string queryName)
    {
        var context = $"{queryName} → {resultOwner.Name}.{binding.TargetName}";
        var ordinal = Expression.Constant(binding.Ordinal);
        var target = binding.TargetType;
        var underlying = Nullable.GetUnderlyingType(target) ?? target;

        var getter = DirectGetter(underlying);
        if (getter is not null && !converter.HasHandler(underlying))
        {
            Expression value = Expression.Call(reader, getter, ordinal);
            if (value.Type != target)
            {
                value = Expression.Convert(value, target);
            }

            // Provider conversion failures must still carry MAP-031 ("errors name
            // things"); the try/catch costs nothing on the non-throwing path.
            var exception = Expression.Parameter(typeof(Exception), "exception");
            value = Expression.TryCatch(
                value,
                Expression.Catch(
                    exception,
                    Expression.Call(
                        typeof(ResultMapper).GetMethod(nameof(FailConvert), BindingFlags.NonPublic | BindingFlags.Static)!
                            .MakeGenericMethod(target),
                        Expression.Constant(context),
                        exception),
                    Expression.OrElse(
                        Expression.TypeIs(exception, typeof(FormatException)),
                        Expression.OrElse(
                            Expression.TypeIs(exception, typeof(InvalidCastException)),
                            Expression.TypeIs(exception, typeof(OverflowException))))));

            var nullable = !target.IsValueType || Nullable.GetUnderlyingType(target) is not null;
            Expression whenNull = nullable
                ? Expression.Default(target)
                : Expression.Call(
                    typeof(ResultMapper).GetMethod(nameof(FailNull), BindingFlags.NonPublic | BindingFlags.Static)!
                        .MakeGenericMethod(target),
                    Expression.Constant(context));
            return Expression.Condition(
                Expression.Call(reader, IsDbNullMethod, ordinal), whenNull, value);
        }

        var raw = Expression.Call(reader, GetValueMethod, ordinal);
        var converted = Expression.Call(
            Expression.Constant(converter), FromDatabaseMethod,
            raw, Expression.Constant(target), Expression.Constant(context));
        return Expression.Convert(converted, target);
    }

    private static readonly MethodInfo IsDbNullMethod =
        typeof(DbDataReader).GetMethod(nameof(DbDataReader.IsDBNull), [typeof(int)])!;

    private static readonly MethodInfo GetValueMethod =
        typeof(DbDataReader).GetMethod(nameof(DbDataReader.GetValue))!;

    private static readonly MethodInfo FromDatabaseMethod =
        typeof(TypeConverter).GetMethod(nameof(TypeConverter.FromDatabase))!;

    private static MethodInfo? DirectGetter(Type type)
    {
        string? name = null;
        if (type == typeof(long))
        {
            name = nameof(DbDataReader.GetInt64);
        }
        else if (type == typeof(int))
        {
            name = nameof(DbDataReader.GetInt32);
        }
        else if (type == typeof(short))
        {
            name = nameof(DbDataReader.GetInt16);
        }
        else if (type == typeof(string))
        {
            name = nameof(DbDataReader.GetString);
        }
        else if (type == typeof(double))
        {
            name = nameof(DbDataReader.GetDouble);
        }
        else if (type == typeof(float))
        {
            name = nameof(DbDataReader.GetFloat);
        }
        else if (type == typeof(bool))
        {
            name = nameof(DbDataReader.GetBoolean);
        }
        else if (type == typeof(decimal))
        {
            name = nameof(DbDataReader.GetDecimal);
        }
        else if (type == typeof(Guid))
        {
            name = nameof(DbDataReader.GetGuid);
        }

        return name is null ? null : typeof(DbDataReader).GetMethod(name, [typeof(int)]);
    }

    private static T FailNull<T>(string context)
        => throw new SimpleOrmException("MAP-031", context, $"NULL cannot convert to non-nullable {typeof(T).Name}");

    private static T FailConvert<T>(string context, Exception inner)
        => throw new SimpleOrmException("MAP-031", context, $"cannot convert the value to {typeof(T).Name}: {inner.Message}");

    private static bool IsInitOnly(PropertyInfo property)
        => property.SetMethod!.ReturnParameter.GetRequiredCustomModifiers()
            .Any(m => m.Name == "IsExternalInit");

    /// <summary>Case- and underscore-insensitive: <c>created_at</c> matches <c>CreatedAt</c>.</summary>
    private static bool NamesMatch(string left, string right)
        => string.Equals(
            left.Replace("_", string.Empty),
            right.Replace("_", string.Empty),
            StringComparison.OrdinalIgnoreCase);

    private bool IsScalar(Type type)
    {
        var underlying = Nullable.GetUnderlyingType(type) ?? type;
        return underlying.IsPrimitive || underlying.IsEnum
            || underlying == typeof(string) || underlying == typeof(decimal) || underlying == typeof(Guid)
            || underlying == typeof(DateTime) || underlying == typeof(DateTimeOffset)
            || underlying == typeof(byte[])
            || converter.HasHandler(underlying);
    }
}
