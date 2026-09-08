using System.Text.Json;
using SimpleOrm.Postgres;
using SimpleOrm.Sqlite;
using SimpleOrm.SqlServer;
using Xunit;

namespace SimpleOrm.Tests;

/// <summary>
/// The AST-case runner (§9, ADR-0020): each conformance/ast/*.json builds a
/// criteria query as data — the JSON encoding of <see cref="SelectAst"/> — and
/// renders it through every dialect, comparing the exact SQL text per dialect
/// (ADR-0024: the <c>expect</c> block carries one entry per dialect, all
/// mandatory) and the ordered parameter values (placeholder names are @c0… in
/// render order by contract — the same order on every dialect, even where the
/// rendered clause reorders them, as OFFSET/FETCH does), or the error code, which
/// is dialect-invariant. No database: rendering is pure.
/// </summary>
public sealed class ConformanceAstTests
{
    private static readonly IReadOnlyDictionary<string, Func<IDialect>> Dialects = new Dictionary<string, Func<IDialect>>
    {
        ["sqlite"] = () => new SqliteDialect(),
        ["sqlserver"] = () => new SqlServerDialect(),
        ["postgres"] = () => new PostgresDialect(),
    };

    public static TheoryData<string> CaseFiles
    {
        get
        {
            var data = new TheoryData<string>();
            var root = Path.Combine(ConformanceMigrationTests.ConformanceDirectory(), "ast");
            foreach (var file in Directory.GetFiles(root, "*.json"))
            {
                data.Add(Path.GetFileName(file));
            }

            // Level 2 extensions (projection, joins, subquery membership) live in
            // ast/level2/ so Level 0–1 ports enumerate only the top level.
            foreach (var file in Directory.GetFiles(Path.Combine(root, "level2"), "*.json"))
            {
                data.Add("level2/" + Path.GetFileName(file));
            }

            return data;
        }
    }

    [Theory]
    [MemberData(nameof(CaseFiles))]
    public void Ast_renders_as_specified(string fileName)
    {
        using var document = JsonDocument.Parse(File.ReadAllText(
            Path.Combine(ConformanceMigrationTests.ConformanceDirectory(), "ast", fileName)));
        var spec = document.RootElement;

        var ast = ParseSelect(LoadMap(spec.GetProperty("entity").GetString()!), spec.GetProperty("select"));
        var expect = spec.GetProperty("expect");

        foreach (var (dialectName, dialect) in Dialects)
        {
            var bound = new List<object?>();
            string Bind(object? value, PropertyMap? property)
            {
                bound.Add(value);
                return "@c" + (bound.Count - 1);
            }

            string? sql = null;
            string? error = null;
            try
            {
                sql = dialect().SelectSql(ast, Bind);
            }
            catch (SimpleOrmException exception)
            {
                error = exception.Code;
            }

            if (expect.TryGetProperty("error", out var expectedError))
            {
                Assert.Equal(expectedError.GetString(), error);
                continue;   // error codes are the cross-dialect contract (§9)
            }

            Assert.Null(error);
            Assert.True(
                expect.TryGetProperty(dialectName, out var expected),
                $"{fileName} lacks the '{dialectName}' expectation — every dialect pins every case (§9)");
            Assert.Equal(expected.GetProperty("sql").GetString(), sql);
            Assert.Equal(
                expected.GetProperty("parameters").EnumerateArray().Select(Value).ToArray(),
                bound.ToArray());
        }
    }

    internal static EntityMap LoadMap(string entityName)
    {
        var entityType = typeof(SimpleOrm.Sample.Models.User).Assembly.GetExportedTypes()
            .Single(t => t.Name == entityName);
        return new EntityMapLoader().Load(entityType);
    }

    /// <summary>
    /// The select encoding of spec/query-ast.md — the Level 1 fields plus the
    /// Level 2 extensions: <c>projection</c> (root property names), <c>joins</c>
    /// (entity, alias, optional parent alias, ON pairs [parent, target], project),
    /// and the <c>in_select</c> predicate whose nested select names its own entity.
    /// </summary>
    internal static SelectAst ParseSelect(EntityMap map, JsonElement select)
    {
        var projection = select.TryGetProperty("projection", out var projected)
            ? projected.EnumerateArray()
                .Select(name => map.Properties.Single(p => p.PropertyName == name.GetString()))
                .ToArray()
            : null;
        var joins = select.TryGetProperty("joins", out var joined)
            ? joined.EnumerateArray()
                .Select(j => new SelectJoin(
                    LoadMap(j.GetProperty("entity").GetString()!),
                    j.GetProperty("alias").GetString()!,
                    j.TryGetProperty("parent", out var parent) && parent.ValueKind == JsonValueKind.String ? parent.GetString() : null,
                    j.GetProperty("on").EnumerateArray()
                        .Select(pair => (pair[0].GetString()!, pair[1].GetString()!))
                        .ToArray(),
                    j.GetProperty("project").GetBoolean()))
                .ToArray()
            : null;

        return new SelectAst(
            map,
            select.TryGetProperty("where", out var where)
                ? where.EnumerateArray().Select(ParseCriteria).ToArray()
                : [],
            select.TryGetProperty("orderBy", out var orderBy)
                ? orderBy.EnumerateArray()
                    .Select(o => new Ordering(
                        o.GetProperty("property").GetString()!,
                        !o.TryGetProperty("order", out var order) ? SortOrder.Asc : order.GetString() switch
                        {
                            "asc" => SortOrder.Asc,
                            "desc" => SortOrder.Desc,
                            var token => throw new InvalidOperationException($"unknown order '{token}'"),
                        }))
                    .ToArray()
                : [],
            select.TryGetProperty("limit", out var limit) ? limit.GetInt64() : null,
            select.TryGetProperty("offset", out var offset) ? offset.GetInt64() : null,
            projection,
            joins);
    }

    private static Criteria ParseCriteria(JsonElement node)
    {
        var property = node.TryGetProperty("property", out var p) ? p.GetString()! : string.Empty;
        return node.GetProperty("op").GetString() switch
        {
            "in_select" => Criteria.InSelect(
                node.GetProperty("properties").EnumerateArray().Select(n => n.GetString()!).ToArray(),
                ParseSelect(
                    LoadMap(node.GetProperty("select").GetProperty("entity").GetString()!),
                    node.GetProperty("select"))),
            "eq" => Criteria.Eq(property, Value(node.GetProperty("value"))!),
            "ne" => Criteria.Ne(property, Value(node.GetProperty("value"))!),
            "gt" => Criteria.Gt(property, Value(node.GetProperty("value"))!),
            "ge" => Criteria.Ge(property, Value(node.GetProperty("value"))!),
            "lt" => Criteria.Lt(property, Value(node.GetProperty("value"))!),
            "le" => Criteria.Le(property, Value(node.GetProperty("value"))!),
            "like" => Criteria.Like(property, (string)Value(node.GetProperty("value"))!),
            "in" => Criteria.In(property, node.GetProperty("values").EnumerateArray().Select(Value).ToArray()!),
            "is_null" => Criteria.IsNull(property),
            "is_not_null" => Criteria.IsNotNull(property),
            "and" => Criteria.And(node.GetProperty("args").EnumerateArray().Select(ParseCriteria).ToArray()),
            "or" => Criteria.Or(node.GetProperty("args").EnumerateArray().Select(ParseCriteria).ToArray()),
            "not" => Criteria.Not(ParseCriteria(node.GetProperty("arg"))),
            var op => throw new InvalidOperationException($"unknown op '{op}'"),
        };
    }

    private static object? Value(JsonElement element) => element.ValueKind switch
    {
        JsonValueKind.Null => null,
        JsonValueKind.True => true,
        JsonValueKind.False => false,
        JsonValueKind.String => element.GetString(),
        JsonValueKind.Number when element.TryGetInt64(out var integer) => integer,
        JsonValueKind.Number => element.GetDouble(),
        var kind => throw new InvalidOperationException($"unsupported value kind {kind}"),
    };
}
