using System.Text;

namespace SimpleOrm;

/// <summary>
/// The default convention: PascalCase/camelCase → snake_case. The exact algorithm is
/// part of the spec (ports must produce identical database names from their own
/// idioms), pinned by the test vectors in the reference implementation:
/// <c>UserId</c> → <c>user_id</c>, <c>UserID</c> → <c>user_id</c>,
/// <c>APIKey</c> → <c>api_key</c>, <c>HTMLParser</c> → <c>html_parser</c>,
/// <c>Address2</c> → <c>address2</c>, <c>Address2B</c> → <c>address2_b</c>.
/// </summary>
public sealed class SnakeCaseNamingConvention : INamingConvention
{
    /// <summary>Shared instance; the class is stateless.</summary>
    public static SnakeCaseNamingConvention Instance { get; } = new();

    public string ColumnName(string propertyName) => ToSnakeCase(propertyName);

    public string TableName(string typeName) => ToSnakeCase(typeName);

    public string IndexName(string tableName, IReadOnlyList<string> columnNames)
    {
        var builder = new StringBuilder("ix_").Append(tableName);
        foreach (var column in columnNames)
        {
            builder.Append('_').Append(column);
        }

        return builder.ToString();
    }

    /// <summary>
    /// An underscore is inserted before an upper-case letter that follows a
    /// lower-case letter or digit, or that starts the last word of an acronym run
    /// (an upper followed by a lower); everything is lower-cased.
    /// </summary>
    private static string ToSnakeCase(string name)
    {
        if (name.Length == 0)
        {
            return name;
        }

        var builder = new StringBuilder(name.Length + 4);
        for (var i = 0; i < name.Length; i++)
        {
            var c = name[i];
            if (char.IsUpper(c) && i > 0)
            {
                var previous = name[i - 1];
                var startsWordAfterLowerOrDigit = char.IsLower(previous) || char.IsDigit(previous);
                var endsAcronymRun = char.IsUpper(previous) && i + 1 < name.Length && char.IsLower(name[i + 1]);
                if (startsWordAfterLowerOrDigit || endsAcronymRun)
                {
                    builder.Append('_');
                }
            }

            builder.Append(char.ToLowerInvariant(c));
        }

        return builder.ToString();
    }
}
