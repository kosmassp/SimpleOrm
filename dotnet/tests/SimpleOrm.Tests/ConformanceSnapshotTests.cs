using System.Text.Json;
using SimpleOrm.Sqlite;
using Xunit;

namespace SimpleOrm.Tests;

/// <summary>
/// The snapshot-case runner (§9, ADR-0017): each conformance/snapshot-cases/*.json
/// names a fixture entity, a version, and a pinned generation time, and expects the
/// exact snapshot document — every implementation must export it identically from
/// its native entity definition. Tables export by columns; views by normalized DDL.
/// </summary>
public sealed class ConformanceSnapshotTests
{
    public static TheoryData<string> CaseFiles
    {
        get
        {
            var data = new TheoryData<string>();
            foreach (var file in Directory.GetFiles(
                Path.Combine(ConformanceMigrationTests.ConformanceDirectory(), "snapshot-cases"), "*.json"))
            {
                data.Add(Path.GetFileName(file));
            }

            return data;
        }
    }

    [Theory]
    [MemberData(nameof(CaseFiles))]
    public void Export_matches_expected_document(string fileName)
    {
        using var document = JsonDocument.Parse(File.ReadAllText(
            Path.Combine(ConformanceMigrationTests.ConformanceDirectory(), "snapshot-cases", fileName)));
        var spec = document.RootElement;

        var entityName = spec.GetProperty("entity").GetString()!;
        var entityType = typeof(SimpleOrm.Sample.Models.User).Assembly.GetExportedTypes()
            .Single(t => t.Name == entityName);
        var map = new EntityMapLoader().Load(entityType);
        var dialect = new SqliteDialect();
        var asOfVersion = spec.GetProperty("asOfVersion").GetInt64();
        var generatedAt = DateTimeOffset.Parse(spec.GetProperty("generatedAt").GetString()!);

        var produced = spec.TryGetProperty("kind", out var kind) && kind.GetString() != "table"
            ? SchemaSnapshot.ExportDdl(
                map.RelationName!, kind.GetString()!, dialect.CreateViewSql(map), asOfVersion, generatedAt)
            : SchemaSnapshot.Export(map, dialect, asOfVersion, generatedAt);

        using var producedDocument = JsonDocument.Parse(produced);
        Assert.True(
            DeepEquals(spec.GetProperty("expect"), producedDocument.RootElement),
            $"produced snapshot differs from the expected document:\n{produced}");
    }

    private static bool DeepEquals(JsonElement expected, JsonElement actual)
    {
        if (expected.ValueKind != actual.ValueKind)
        {
            return false;
        }

        switch (expected.ValueKind)
        {
            case JsonValueKind.Object:
                var expectedProperties = expected.EnumerateObject().ToDictionary(p => p.Name, p => p.Value);
                var actualProperties = actual.EnumerateObject().ToDictionary(p => p.Name, p => p.Value);
                return expectedProperties.Count == actualProperties.Count
                    && expectedProperties.All(p =>
                        actualProperties.TryGetValue(p.Key, out var value) && DeepEquals(p.Value, value));
            case JsonValueKind.Array:
                var expectedItems = expected.EnumerateArray().ToArray();
                var actualItems = actual.EnumerateArray().ToArray();
                return expectedItems.Length == actualItems.Length
                    && expectedItems.Zip(actualItems, DeepEquals).All(equal => equal);
            default:
                return expected.GetRawText() == actual.GetRawText();
        }
    }
}
