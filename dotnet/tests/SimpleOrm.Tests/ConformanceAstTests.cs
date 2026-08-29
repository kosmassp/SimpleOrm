using System.Text.Json;
using SimpleOrm.Sqlite;
using Xunit;

namespace SimpleOrm.Tests;

/// <summary>
/// The AST-case runner (§9, ADR-0020): each conformance/ast/*.json builds a
/// criteria query as data — the JSON encoding of <see cref="SelectAst"/> — and
/// renders it through the dialect, comparing the exact SQL text and the ordered
/// parameter values (placeholder names are @c0… in render order by contract), or
/// the error code. No database: rendering is pure.
/// </summary>
public sealed class ConformanceAstTests
{
    public static TheoryData<string> CaseFiles
    {
        get
        {
            var data = new TheoryData<string>();
            foreach (var file in Directory.GetFiles(
                Path.Combine(ConformanceMigrationTests.ConformanceDirectory(), "ast"), "*.json"))
            {
                data.Add(Path.GetFileName(file));
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

        var entityName = spec.GetProperty("entity").GetString()!;
        var entityType = typeof(SimpleOrm.Sample.Models.User).Assembly.GetExportedTypes()
            .Single(t => t.Name == entityName);
        var map = new EntityMapLoader().Load(entityType);
        var ast = ParseSelect(map, spec.GetProperty("select"));

        var bound = new List<object?>();
        string Bind(object? value, PropertyMap? property)
        {
            bound.Add(value);
            return "@c" + (bound.Count - 1);
        }

        var expect = spec.GetProperty("expect");
        string? sql = null;
        string? error = null;
        try
        {
            sql = new SqliteDialect().SelectSql(ast, Bind);
        }
        catch (SimpleOrmException exception)
        {
            error = exception.Code;
        }

        if (expect.TryGetProperty("error", out var expectedError))
        {
            Assert.Equal(expectedError.GetString(), error);
            return;
        }

        Assert.Null(error);
        var sqlite = expect.GetProperty("sqlite");
        Assert.Equal(sqlite.GetProperty("sql").GetString(), sql);
        Assert.Equal(
            sqlite.GetProperty("parameters").EnumerateArray().Select(Value).ToArray(),
            bound.ToArray());
    }

    private static SelectAst ParseSelect(EntityMap map, JsonElement select)
        => new(
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
            select.TryGetProperty("offset", out var offset) ? offset.GetInt64() : null);

    private static Criteria ParseCriteria(JsonElement node)
    {
        var property = node.TryGetProperty("property", out var p) ? p.GetString()! : string.Empty;
        return node.GetProperty("op").GetString() switch
        {
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
