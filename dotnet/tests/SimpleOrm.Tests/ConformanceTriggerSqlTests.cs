using System.Text.Json;
using System.Text.Json.Nodes;
using SimpleOrm.Postgres;
using SimpleOrm.Sqlite;
using SimpleOrm.SqlServer;
using Xunit;

namespace SimpleOrm.Tests;

/// <summary>
/// The trigger-sql runner (§9, ADR-0042): each conformance/trigger-sql/*.json
/// carries a relation and a neutral trigger declaration — pure data, no entity —
/// and pins what every dialect renders for it: the create statements, the drop
/// statements, or the <c>DDL-005</c> refusal. Like the AST and crud-sql runners:
/// one entry per dialect, all mandatory, no database. Regenerate the
/// expectations from the reference with SIMPLEORM_CONFORMANCE_WRITE=1.
/// </summary>
public sealed class ConformanceTriggerSqlTests
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
            foreach (var file in Directory.GetFiles(Path.Combine(ConformanceMigrationTests.ConformanceDirectory(), "trigger-sql"), "*.json"))
            {
                data.Add(Path.GetFileName(file));
            }

            return data;
        }
    }

    [Theory]
    [MemberData(nameof(CaseFiles))]
    public void Trigger_renders_as_specified(string fileName)
    {
        var path = Path.Combine(ConformanceMigrationTests.ConformanceDirectory(), "trigger-sql", fileName);
        var spec = JsonNode.Parse(File.ReadAllText(path))!.AsObject();
        using var document = JsonDocument.Parse(spec["trigger"]!.ToJsonString());
        var relation = spec["relation"]!.GetValue<string>();
        var onView = spec["relationKind"]?.GetValue<string>() == "view";
        var definition = SchemaSnapshot.ParseTrigger(
            JsonDocument.Parse("{\"relation\": \"" + relation + "\", " + spec["trigger"]!.ToJsonString().TrimStart('{')).RootElement).Definition;

        var rendered = new JsonObject();
        foreach (var (dialectName, dialect) in Dialects)
        {
            rendered[dialectName] = Render(dialect(), relation, onView, definition);
        }

        if (Environment.GetEnvironmentVariable("SIMPLEORM_CONFORMANCE_WRITE") == "1")
        {
            spec["expect"] = rendered;
            File.WriteAllText(path, spec.ToJsonString(new JsonSerializerOptions
            {
                WriteIndented = true,
                NewLine = "\n",
                Encoder = System.Text.Encodings.Web.JavaScriptEncoder.UnsafeRelaxedJsonEscaping,
            }) + "\n");
            return;
        }

        var expect = spec["expect"]!.AsObject();
        foreach (var dialectName in Dialects.Keys)
        {
            Assert.True(expect.ContainsKey(dialectName), $"{fileName} lacks the '{dialectName}' expectation — every dialect pins every case (§9)");
            Assert.Equal(expect[dialectName]!.ToJsonString(), rendered[dialectName]!.ToJsonString());
        }
    }

    private static JsonObject Render(IDialect dialect, string relation, bool onView, TriggerDefinition definition)
    {
        try
        {
            return new JsonObject
            {
                ["create"] = new JsonArray(dialect.CreateTriggerSql(relation, onView, definition).Select(s => (JsonNode)s).ToArray()),
                ["drop"] = new JsonArray(dialect.DropTriggerSql(relation, definition.Name).Select(s => (JsonNode)s).ToArray()),
            };
        }
        catch (SimpleOrmException exception)
        {
            return new JsonObject { ["error"] = exception.Code };
        }
    }
}
