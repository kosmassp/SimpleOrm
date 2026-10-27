using System.Collections;
using System.Collections.Concurrent;
using System.Data.Common;
using System.Reflection;
using System.Runtime.CompilerServices;
using System.Text;
using System.Text.RegularExpressions;

namespace SimpleOrm;

/// <summary>
/// Binds <c>@name</c> placeholders from the public properties of the args object
/// (§7.12/§7.13). Both directions are strict: an unmatched placeholder is
/// <c>PRM-001</c>, an unused property is <c>PRM-002</c>. Collection-typed
/// properties expand <c>IN (@ids)</c> to generated placeholders
/// (<c>@ids_0..@ids_N</c>), always parameterized; an empty collection becomes
/// <c>NULL</c>, which matches no rows. On a dialect with native array parameters
/// (<c>SupportsArrayParameters</c>, ADR-0025 Postgres) the collection binds as
/// <b>one</b> typed-array parameter and the SQL is untouched — registry SQL is
/// written dialect-natively (<c>= any(@ids)</c>); an empty array still matches no
/// rows, so the observable contract is identical (spec/session.md).
/// </summary>
internal static class ParameterBinder
{
    /// <summary>
    /// The placeholder scan of one SQL text and its binding plans per args type
    /// (ADR-0048), so neither the scan nor the args reflection repeats per call.
    /// Keyed by the SQL string instance (a registry entry's resolved text, a
    /// statement entity's defining SQL — the same instance every call); the scan
    /// is a pure function of the text, so two instances with equal text simply
    /// hold equal entries, and an entry dies with its string.
    /// </summary>
    private static readonly ConditionalWeakTable<string, SqlScan> Scans = new();

    public static void Bind(
        DbCommand command, string sql, object args, string queryName, TypeConverter converter,
        bool arrayParameters = false)
    {
        var argsType = args.GetType();
        var plan = Scans.GetValue(sql, static text => new SqlScan(text)).PlanFor(argsType);

        if (plan.UnmatchedPlaceholder is { } unmatched)
        {
            throw new SimpleOrmException(
                "PRM-001", queryName, $"SQL parameter @{unmatched} has no matching property on {argsType.Name}");
        }

        var text = sql;
        foreach (var slot in plan.Slots)
        {
            // In property order, binding as it goes — so an earlier property's
            // conversion error still wins over a later unused property, as before.
            if (slot.Placeholder is not { } placeholder)
            {
                throw new SimpleOrmException(
                    "PRM-002", queryName, $"property {argsType.Name}.{slot.Property.Name} is never used by the SQL");
            }

            var value = slot.Property.GetValue(args);
            var context = slot.Context(queryName);
            if (value is IEnumerable enumerable and not string and not byte[])
            {
                if (arrayParameters)
                {
                    AddParameter(
                        command, slot.ParameterName,
                        ToTypedArray(enumerable, slot.Property.PropertyType, converter, context));
                }
                else
                {
                    // The scan's spans hold for the text as written; once one list
                    // expanded, positions moved, so a further list rescans the text.
                    text = ExpandList(
                        command, text, placeholder, enumerable, converter, context,
                        ReferenceEquals(text, sql) ? slot.Occurrences : SqlPlaceholders.Occurrences(text, placeholder));
                }
            }
            else
            {
                var parameter = command.CreateParameter();
                parameter.ParameterName = slot.ParameterName;
                converter.BindValue(parameter, value, context, enumAsInt: false, slot.Property.PropertyType);   // a typed null (ADR-0040 add.2)
                command.Parameters.Add(parameter);
            }
        }

        command.CommandText = text;
    }

    /// <summary>One SQL text, scanned once: its placeholders and every real occurrence of each.</summary>
    private sealed class SqlScan(string sql)
    {
        private readonly IReadOnlyList<string> _placeholders = SqlPlaceholders.Find(sql);
        private readonly ConcurrentDictionary<Type, BindPlan> _plans = new();

        public BindPlan PlanFor(Type argsType)
            => _plans.TryGetValue(argsType, out var plan)
                ? plan
                : _plans.GetOrAdd(argsType, new BindPlan(sql, _placeholders, argsType));
    }

    /// <summary>How one args type binds against one SQL text: the PRM-001 verdict and one slot per public property, in property order.</summary>
    private sealed class BindPlan
    {
        public BindPlan(string sql, IReadOnlyList<string> placeholders, Type argsType)
        {
            var properties = argsType
                .GetProperties(BindingFlags.Public | BindingFlags.Instance)
                .Where(p => p.GetMethod is { IsPublic: true } && p.GetIndexParameters().Length == 0)
                .ToArray();
            UnmatchedPlaceholder = placeholders.FirstOrDefault(
                placeholder => !properties.Any(p => string.Equals(p.Name, placeholder, StringComparison.OrdinalIgnoreCase)));
            Slots = properties
                .Select(property => new Slot(
                    property,
                    placeholders.FirstOrDefault(p => string.Equals(p, property.Name, StringComparison.OrdinalIgnoreCase)),
                    sql))
                .ToArray();
        }

        public string? UnmatchedPlaceholder { get; }

        public Slot[] Slots { get; }
    }

    /// <summary>One args property: the placeholder it binds (null = unused, <c>PRM-002</c>), its parameter name, its occurrences in the text as written, its error label.</summary>
    private sealed class Slot
    {
        private Label? _label;

        public Slot(PropertyInfo property, string? placeholder, string sql)
        {
            Property = property;
            Placeholder = placeholder;
            ParameterName = placeholder is null ? string.Empty : "@" + placeholder;
            Occurrences = placeholder is null ? [] : SqlPlaceholders.Occurrences(sql, placeholder);
        }

        public PropertyInfo Property { get; }

        public string? Placeholder { get; }

        public string ParameterName { get; }

        public IReadOnlyList<(int Index, int Length)> Occurrences { get; }

        /// <summary>The converter's label, <c>{queryName} @{placeholder}</c>, kept for the query name last seen.</summary>
        public string Context(string queryName)
        {
            var label = _label;
            if (label is null || !string.Equals(label.QueryName, queryName, StringComparison.Ordinal))
            {
                _label = label = new Label(queryName, $"{queryName} @{Placeholder}");
            }

            return label.Text;
        }

        private sealed class Label(string queryName, string text)
        {
            public string QueryName { get; } = queryName;

            public string Text { get; } = text;
        }
    }

    private static string ExpandList(
        DbCommand command, string sql, string placeholder, IEnumerable values, TypeConverter converter, string context,
        IReadOnlyList<(int Index, int Length)> occurrences)
    {
        var names = new List<string>();
        var index = 0;
        foreach (var value in values)
        {
            var name = $"@{placeholder}_{index++}";
            names.Add(name);
            AddParameter(command, name, converter.ToDatabase(value, context));
        }

        // An empty list becomes NULL: "x IN (NULL)" is valid SQL matching no rows.
        var replacement = names.Count == 0 ? "NULL" : string.Join(", ", names);

        // Rewrite only real occurrences — a lookalike inside a string literal or
        // comment is not a placeholder for expansion any more than for detection.
        var builder = new StringBuilder(sql);
        foreach (var (start, length) in occurrences.Reverse())
        {
            builder.Remove(start, length).Insert(start, replacement);
        }

        return builder.ToString();
    }

    /// <summary>
    /// The single-parameter form (ADR-0025): every element through the same
    /// conversion pipeline, materialized as an array typed by what the pipeline
    /// produces — the provider infers the database array type from it, and an
    /// empty collection stays correctly typed (<c>bigint = any(text[])</c>
    /// refuses even empty).
    /// </summary>
    private static Array ToTypedArray(IEnumerable values, Type propertyType, TypeConverter converter, string context)
    {
        var converted = new List<object>();
        foreach (var value in values)
        {
            converted.Add(converter.ToDatabase(value, context));
        }

        var elementType = converter.ArrayElementStorageType(ElementTypeOf(propertyType));
        if (elementType == typeof(object) && converted.Count > 0 && converted[0] is not DBNull)
        {
            elementType = converted[0].GetType();   // handler output: unknowable statically, knowable here
        }

        var array = Array.CreateInstance(elementType, converted.Count);
        for (var i = 0; i < converted.Count; i++)
        {
            array.SetValue(converted[i] is DBNull ? null : converted[i], i);
        }

        return array;
    }

    private static Type ElementTypeOf(Type propertyType)
        => new[] { propertyType }.Concat(propertyType.GetInterfaces())
            .Where(t => t.IsGenericType && t.GetGenericTypeDefinition() == typeof(IEnumerable<>))
            .Select(t => t.GetGenericArguments()[0])
            .FirstOrDefault() ?? typeof(object);

    private static void AddParameter(DbCommand command, string name, object value)
    {
        var parameter = command.CreateParameter();
        parameter.ParameterName = name;
        parameter.Value = value;
        command.Parameters.Add(parameter);
    }
}
