using System.Text;
using System.Text.RegularExpressions;

namespace SimpleOrm;

/// <summary>Finds <c>@name</c> placeholders in SQL, ignoring string literals and comments.</summary>
internal static class SqlPlaceholders
{
    private static readonly Regex Placeholder = new(@"@([A-Za-z_][A-Za-z0-9_]*)", RegexOptions.Compiled);

    public static IReadOnlyList<string> Find(string sql)
    {
        var masked = Mask(sql);
        var seen = new HashSet<string>();
        var names = new List<string>();
        foreach (Match match in Placeholder.Matches(masked))
        {
            var name = match.Groups[1].Value;
            if (seen.Add(name))
            {
                names.Add(name);
            }
        }

        return names;
    }

    /// <summary>
    /// The positions (index, length) of every real occurrence of one placeholder —
    /// lookalikes inside string literals and comments are not placeholders, for
    /// rewriting (IN-list expansion) exactly as for detection.
    /// </summary>
    public static IReadOnlyList<(int Index, int Length)> Occurrences(string sql, string placeholder)
    {
        var masked = Mask(sql);
        var spans = new List<(int, int)>();
        foreach (Match match in Placeholder.Matches(masked))
        {
            if (string.Equals(match.Groups[1].Value, placeholder, StringComparison.OrdinalIgnoreCase))
            {
                spans.Add((match.Index, match.Length));
            }
        }

        return spans;
    }

    /// <summary>
    /// Length-preserving mask: string-literal and comment characters become spaces,
    /// so match positions in the mask are positions in the original SQL.
    /// </summary>
    private static string Mask(string sql)
    {
        var builder = new StringBuilder(sql.Length);
        var i = 0;
        while (i < sql.Length)
        {
            var c = sql[i];
            if (c == '\'')
            {
                // String literal; '' is the escaped quote.
                builder.Append(' ');
                i++;
                while (i < sql.Length)
                {
                    if (sql[i] == '\'')
                    {
                        if (i + 1 < sql.Length && sql[i + 1] == '\'')
                        {
                            builder.Append("  ");
                            i += 2;
                            continue;
                        }

                        builder.Append(' ');
                        i++;
                        break;
                    }

                    builder.Append(' ');
                    i++;
                }
            }
            else if (c == '-' && i + 1 < sql.Length && sql[i + 1] == '-')
            {
                while (i < sql.Length && sql[i] != '\n')
                {
                    builder.Append(' ');
                    i++;
                }
            }
            else if (c == '/' && i + 1 < sql.Length && sql[i + 1] == '*')
            {
                builder.Append("  ");
                i += 2;
                while (i + 1 < sql.Length && !(sql[i] == '*' && sql[i + 1] == '/'))
                {
                    builder.Append(' ');
                    i++;
                }

                if (i + 1 < sql.Length)
                {
                    builder.Append("  ");
                }
                else if (i < sql.Length)
                {
                    builder.Append(' ');
                }

                i = Math.Min(i + 2, sql.Length);
            }
            else
            {
                builder.Append(c);
                i++;
            }
        }

        return builder.ToString();
    }
}
