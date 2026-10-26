using System.Data.Common;
using System.Globalization;
using System.Text;

namespace SimpleOrm;

/// <summary>
/// The statement log (Level 4 item 4, ADR-0045, spec/session.md "Statement log"):
/// one line of text per statement, written as the statement is sent, through
/// <see cref="DbOptions.Log"/>. The library brings no logging dependency — the
/// application forwards the line to its own logger with one lambda, or uses
/// <see cref="Console"/>; unset, nothing is written and nothing is formatted.
/// </summary>
public static class DbLog
{
    /// <summary>The built-in sink: every line to standard output.</summary>
    public static readonly Action<string> Console = static line => System.Console.Out.WriteLine(line);

    /// <summary>
    /// The line: <c>&lt;name&gt;: &lt;sql&gt;</c>, then the parameters in bind order as
    /// <c>[@a, @b]</c> — or <c>[@a = 1, @b = 'x']</c> when values are included.
    /// </summary>
    internal static string Format(string name, string sql, IEnumerable<DbParameter> parameters, bool includeValues)
    {
        var line = new StringBuilder(name).Append(": ").Append(sql);
        var first = true;
        foreach (var parameter in parameters)
        {
            line.Append(first ? " [" : ", ").Append(parameter.ParameterName);
            if (includeValues)
            {
                line.Append(" = ").Append(FormatValue(parameter.Value));
            }

            first = false;
        }

        if (!first)
        {
            line.Append(']');
        }

        return line.ToString();
    }

    private static string FormatValue(object? value)
    {
        switch (value)
        {
            case null or DBNull:
                return "null";
            case string text:
                return "'" + text.Replace("'", "''") + "'";
            case bool flag:
                return flag ? "true" : "false";
            case byte[] bytes:
                return bytes.Length <= 16
                    ? "0x" + ToHex(bytes)
                    : "0x" + ToHex(bytes.Take(16).ToArray()) + "… (" + bytes.Length.ToString(CultureInfo.InvariantCulture) + " bytes)";
            case DateTime instant:
                return instant.ToString("o", CultureInfo.InvariantCulture);
            case DateTimeOffset instant:
                return instant.ToString("o", CultureInfo.InvariantCulture);
            case IFormattable formattable:
                return formattable.ToString(null, CultureInfo.InvariantCulture);
            case System.Collections.IEnumerable items:   // an array parameter (PostgreSQL, spec/session.md "Parameters")
                return "[" + string.Join(", ", items.Cast<object?>().Select(FormatValue)) + "]";
            default:
                return value.ToString() ?? "null";
        }
    }

    private static string ToHex(byte[] bytes)
    {
        var hex = new StringBuilder(bytes.Length * 2);
        foreach (var b in bytes)
        {
            hex.Append(b.ToString("x2", CultureInfo.InvariantCulture));
        }

        return hex.ToString();
    }
}

public sealed partial class Db
{
    /// <summary>
    /// The one funnel every statement passes as it is sent (spec/session.md
    /// "Statement log"): nothing when <see cref="DbOptions.Log"/> is unset.
    /// </summary>
    internal void LogStatement(string name, DbCommand command)
        => LogStatement(name, command.CommandText, command.Parameters);

    internal void LogStatement(string name, string sql, DbParameterCollection parameters)
    {
        if (Options.Log is { } log)
        {
            log(DbLog.Format(name, sql, parameters.Cast<DbParameter>(), Options.LogParameterValues));
        }
    }
}
