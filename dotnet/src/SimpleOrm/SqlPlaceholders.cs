using System.Text;
using System.Text.RegularExpressions;

namespace SimpleOrm;

/// <summary>Finds <c>@name</c> placeholders in SQL, ignoring string literals and comments.</summary>
internal static class SqlPlaceholders
{
    private static readonly Regex Placeholder = new(@"@([A-Za-z_][A-Za-z0-9_]*)", RegexOptions.Compiled);

    public static IReadOnlyList<string> Find(string sql)
    {
        var stripped = Strip(sql);
        var seen = new HashSet<string>();
        var names = new List<string>();
        foreach (Match match in Placeholder.Matches(stripped))
        {
            var name = match.Groups[1].Value;
            if (seen.Add(name))
            {
                names.Add(name);
            }
        }

        return names;
    }

    private static string Strip(string sql)
    {
        var builder = new StringBuilder(sql.Length);
        var i = 0;
        while (i < sql.Length)
        {
            var c = sql[i];
            if (c == '\'')
            {
                // String literal; '' is the escaped quote.
                i++;
                while (i < sql.Length)
                {
                    if (sql[i] == '\'')
                    {
                        if (i + 1 < sql.Length && sql[i + 1] == '\'')
                        {
                            i += 2;
                            continue;
                        }

                        i++;
                        break;
                    }

                    i++;
                }
            }
            else if (c == '-' && i + 1 < sql.Length && sql[i + 1] == '-')
            {
                while (i < sql.Length && sql[i] != '\n')
                {
                    i++;
                }
            }
            else if (c == '/' && i + 1 < sql.Length && sql[i + 1] == '*')
            {
                i += 2;
                while (i + 1 < sql.Length && !(sql[i] == '*' && sql[i + 1] == '/'))
                {
                    i++;
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
