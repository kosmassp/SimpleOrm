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
            return reader => (T)converter.FromDatabase(reader.GetValue(0), typeof(T), queryName)!;
        }

        return AttributeMapLoader.HasMappingAttributes(typeof(T))
            ? Compile<T>(EntityBindings<T>(columns, queryName), queryName)
            : Compile<T>(DtoBindings<T>(columns, queryName), queryName);
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
        var converterConstant = Expression.Constant(converter);
        var getValue = typeof(DbDataReader).GetMethod(nameof(DbDataReader.GetValue))!;
        var fromDatabase = typeof(TypeConverter).GetMethod(nameof(TypeConverter.FromDatabase))!;

        Expression Read(Binding binding)
        {
            var raw = Expression.Call(reader, getValue, Expression.Constant(binding.Ordinal));
            var context = $"{queryName} → {typeof(T).Name}.{binding.TargetName}";
            var converted = Expression.Call(
                converterConstant, fromDatabase,
                raw, Expression.Constant(binding.TargetType), Expression.Constant(context));
            return Expression.Convert(converted, binding.TargetType);
        }

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
