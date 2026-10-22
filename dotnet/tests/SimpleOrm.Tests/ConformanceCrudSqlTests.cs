using System.Text.Json;
using System.Text.Json.Nodes;
using SimpleOrm.Postgres;
using SimpleOrm.Sqlite;
using SimpleOrm.SqlServer;
using Xunit;

namespace SimpleOrm.Tests;

/// <summary>
/// The crud-sql runner (§9, ADR-0040 add.3): each conformance/crud-sql/*.json
/// names a fixture entity and one generated write — <c>insert</c>, <c>update</c>,
/// <c>update_only</c> (with its property list), or <c>delete</c> (with
/// <c>checkVersion</c>) — and pins the exact SQL text every dialect renders for
/// it (spec/crud.md, spec/dialects.md). Like the AST runner: one entry per
/// dialect, all mandatory, rendering is pure, no database. Regenerate the
/// expectations from the reference with SIMPLEORM_CONFORMANCE_WRITE=1.
/// </summary>
public sealed class ConformanceCrudSqlTests
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
            foreach (var file in Directory.GetFiles(Path.Combine(ConformanceMigrationTests.ConformanceDirectory(), "crud-sql"), "*.json"))
            {
                data.Add(Path.GetFileName(file));
            }

            return data;
        }
    }

    [Theory]
    [MemberData(nameof(CaseFiles))]
    public void Generated_write_renders_as_specified(string fileName)
    {
        var path = Path.Combine(ConformanceMigrationTests.ConformanceDirectory(), "crud-sql", fileName);
        var spec = JsonNode.Parse(File.ReadAllText(path))!.AsObject();
        var map = ConformanceAstTests.LoadMap(spec["entity"]!.GetValue<string>());
        var statement = spec["statement"]!.GetValue<string>();

        var rendered = new JsonObject();
        foreach (var (dialectName, dialect) in Dialects)
        {
            rendered[dialectName] = Render(dialect(), map, statement, spec);
        }

        if (Environment.GetEnvironmentVariable("SIMPLEORM_CONFORMANCE_WRITE") == "1")
        {
            spec["expect"] = rendered;
            File.WriteAllText(path, spec.ToJsonString(new JsonSerializerOptions
            {
                WriteIndented = true,
                NewLine = "\n",   // the repo pins LF
                Encoder = System.Text.Encodings.Web.JavaScriptEncoder.UnsafeRelaxedJsonEscaping,
            }) + "\n");
            return;
        }

        var expect = spec["expect"]!.AsObject();
        foreach (var dialectName in Dialects.Keys)
        {
            Assert.True(expect.ContainsKey(dialectName), $"{fileName} lacks the '{dialectName}' expectation — every dialect pins every case (§9)");
            Assert.Equal(expect[dialectName]!.GetValue<string>(), rendered[dialectName]!.GetValue<string>());
        }
    }

    private static string Render(IDialect dialect, EntityMap map, string statement, JsonObject spec) => statement switch
    {
        "insert" => dialect.InsertSql(map),
        "update" => dialect.UpdateSql(map),
        "update_only" => dialect.UpdateOnlySql(
            map,
            spec["properties"]!.AsArray()
                .Select(name => map.Properties.Single(p => p.PropertyName == name!.GetValue<string>()))
                .ToArray()),
        "delete" => dialect.DeleteSql(map, spec["checkVersion"]!.GetValue<bool>()),
        _ => throw new InvalidOperationException($"unknown statement '{statement}'"),
    };
}
