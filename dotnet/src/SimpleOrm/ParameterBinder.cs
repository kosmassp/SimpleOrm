using System.Collections;
using System.Data.Common;
using System.Reflection;
using System.Text;
using System.Text.RegularExpressions;

namespace SimpleOrm;

/// <summary>
/// Binds <c>@name</c> placeholders from the public properties of the args object
/// (§7.12/§7.13). Both directions are strict: an unmatched placeholder is
/// <c>PRM-001</c>, an unused property is <c>PRM-002</c>. Collection-typed
/// properties expand <c>IN (@ids)</c> to generated placeholders
/// (<c>@ids_0..@ids_N</c>), always parameterized; an empty collection becomes
/// <c>NULL</c>, which matches no rows.
/// </summary>
internal static class ParameterBinder
{
    public static void Bind(DbCommand command, string sql, object args, string queryName, TypeConverter converter)
    {
        var properties = args.GetType()
            .GetProperties(BindingFlags.Public | BindingFlags.Instance)
            .Where(p => p.GetMethod is { IsPublic: true } && p.GetIndexParameters().Length == 0)
            .ToArray();
        var placeholders = SqlPlaceholders.Find(sql);

        foreach (var placeholder in placeholders)
        {
            if (!properties.Any(p => string.Equals(p.Name, placeholder, StringComparison.OrdinalIgnoreCase)))
            {
                throw new SimpleOrmException(
                    "PRM-001", queryName, $"SQL parameter @{placeholder} has no matching property on {args.GetType().Name}");
            }
        }

        var text = sql;
        foreach (var property in properties)
        {
            var used = placeholders.Any(p => string.Equals(p, property.Name, StringComparison.OrdinalIgnoreCase));
            if (!used)
            {
                throw new SimpleOrmException(
                    "PRM-002", queryName, $"property {args.GetType().Name}.{property.Name} is never used by the SQL");
            }

            var placeholder = placeholders.First(p => string.Equals(p, property.Name, StringComparison.OrdinalIgnoreCase));
            var value = property.GetValue(args);

            var context = $"{queryName} @{placeholder}";
            if (value is IEnumerable enumerable and not string and not byte[])
            {
                text = ExpandList(command, text, placeholder, enumerable, converter, context);
            }
            else
            {
                AddParameter(command, "@" + placeholder, converter.ToDatabase(value, context));
            }
        }

        command.CommandText = text;
    }

    private static string ExpandList(
        DbCommand command, string sql, string placeholder, IEnumerable values, TypeConverter converter, string context)
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
        foreach (var (start, length) in SqlPlaceholders.Occurrences(sql, placeholder).Reverse())
        {
            builder.Remove(start, length).Insert(start, replacement);
        }

        return builder.ToString();
    }

    private static void AddParameter(DbCommand command, string name, object value)
    {
        var parameter = command.CreateParameter();
        parameter.ParameterName = name;
        parameter.Value = value;
        command.Parameters.Add(parameter);
    }
}
