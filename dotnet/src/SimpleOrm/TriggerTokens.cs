using System.Text;
using System.Text.RegularExpressions;

namespace SimpleOrm;

/// <summary>
/// The three tokens a trigger body may carry (ADR-0042) and their expansion.
/// The body is real SQL; the tokens are the only non-SQL in it, and they stand
/// for exactly the things that differ per database: the changed rows as a
/// relation (<c>{new: a, b}</c>, <c>{old: a, b}</c> — a column list is
/// required, so the rendering depends on the body alone and never on the
/// table's current shape) and the current UTC time (<c>{now}</c>). A dialect
/// renders a body by walking the parsed segments and supplying its own
/// expansions; the parser and the declaration-time checks are shared.
/// </summary>
public static class TriggerTokens
{
    private static readonly Regex Token = new(@"\{(?<body>[^{}]*)\}", RegexOptions.Compiled);

    /// <summary>One parsed piece of a body: literal SQL, or a token with its kind and columns.</summary>
    public sealed class Segment
    {
        internal Segment(string literal) => Literal = literal;

        internal Segment(string kind, IReadOnlyList<string> columns)
        {
            Kind = kind;
            Columns = columns;
        }

        /// <summary>Literal SQL text; null for a token.</summary>
        public string? Literal { get; }

        /// <summary><c>new</c>, <c>old</c>, or <c>now</c>; null for literal text.</summary>
        public string? Kind { get; }

        /// <summary>The declared columns of a <c>new</c>/<c>old</c> token; empty for <c>now</c>.</summary>
        public IReadOnlyList<string> Columns { get; } = [];
    }

    /// <summary>
    /// Splits a body into literal text and tokens; every malformed token is one
    /// error (<c>MAP-029</c>): an unknown name, a row token without columns, an
    /// empty column entry, <c>{now}</c> with a column list.
    /// </summary>
    public static IReadOnlyList<Segment> Parse(string sql, string target, List<MappingError> errors)
    {
        var segments = new List<Segment>();
        var last = 0;
        foreach (Match match in Token.Matches(sql))
        {
            if (match.Index > last)
            {
                segments.Add(new Segment(sql.Substring(last, match.Index - last)));
            }

            last = match.Index + match.Length;
            var body = match.Groups["body"].Value;
            var colon = body.IndexOf(':');
            var kind = (colon < 0 ? body : body.Substring(0, colon)).Trim().ToLowerInvariant();
            var list = colon < 0 ? null : body.Substring(colon + 1);
            switch (kind)
            {
                case "new":
                case "old":
                    if (list is null)
                    {
                        errors.Add(new MappingError(
                            "MAP-029", target, $"{{{kind}}} needs its column list — {{{kind}: a, b}} — so the rendering never depends on the table's current shape"));
                        continue;
                    }

                    var columns = list.Split(',').Select(c => c.Trim()).ToArray();
                    if (columns.Length == 0 || columns.Any(c => c.Length == 0))
                    {
                        errors.Add(new MappingError("MAP-029", target, $"{{{kind}: …}} has an empty column entry"));
                        continue;
                    }

                    segments.Add(new Segment(kind, columns));
                    break;
                case "now":
                    if (list is not null)
                    {
                        errors.Add(new MappingError("MAP-029", target, "{now} takes no column list"));
                        continue;
                    }

                    segments.Add(new Segment("now", []));
                    break;
                default:
                    errors.Add(new MappingError(
                        "MAP-029", target, $"unknown token '{match.Value}'; the body tokens are {{new: cols}}, {{old: cols}}, and {{now}}"));
                    continue;
            }
        }

        if (last < sql.Length)
        {
            segments.Add(new Segment(sql.Substring(last)));
        }

        return segments;
    }

    /// <summary>
    /// Renders a body: literal text verbatim, each token through the caller's
    /// expansion. The dialects call this with their own spellings.
    /// </summary>
    public static string Render(string sql, Func<Segment, string> expand)
    {
        var errors = new List<MappingError>();
        var segments = Parse(sql, "trigger", errors);
        if (errors.Count > 0)
        {
            throw new MappingException(typeof(TriggerTokens), errors);   // a declaration the loader never saw (the data-driven form); same code
        }

        var builder = new StringBuilder(sql.Length);
        foreach (var segment in segments)
        {
            builder.Append(segment.Literal ?? expand(segment));
        }

        return builder.ToString();
    }

    /// <summary>A one-row derived table over a row variable: <c>(select new.a as a, new.b as b)</c> — SQLite's and PostgreSQL's row relation.</summary>
    public static string RowSelect(string rowVariable, IReadOnlyList<string> columns, Func<string, string> quote)
        => "(select " + string.Join(", ", columns.Select(c => rowVariable + "." + quote(c) + " as " + quote(c))) + ")";
}
