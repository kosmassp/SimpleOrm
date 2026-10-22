using System.Text.Json;
using SimpleOrm.Postgres;
using SimpleOrm.Sqlite;
using SimpleOrm.SqlServer;
using Xunit;

namespace SimpleOrm.Tests;

/// <summary>
/// The snapshot-case runner (§9, ADR-0017, per dialect since ADR-0043): each
/// conformance/snapshot-cases/*.json names a fixture entity, a version, and a
/// pinned generation time, and expects the exact snapshot document per dialect
/// (<c>expect</c> keyed by dialect name, the <c>ast/</c> precedent) — every
/// implementation must export each one identically from its native entity
/// definition through every dialect it ships; a shipped dialect missing from
/// <c>expect</c> fails the suite. Tables export by columns in the dialect's
/// storage types; views by normalized, dialect-neutral DDL; triggers by
/// declaration; every non-SQLite document carries the dialect stamp.
/// </summary>
public sealed class ConformanceSnapshotTests
{
    private static readonly IDialect[] Dialects = [new SqliteDialect(), new SqlServerDialect(), new PostgresDialect()];

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
    public void Export_matches_expected_document_on_every_dialect(string fileName)
    {
        using var document = JsonDocument.Parse(File.ReadAllText(
            Path.Combine(ConformanceMigrationTests.ConformanceDirectory(), "snapshot-cases", fileName)));
        var spec = document.RootElement;

        var entityName = spec.GetProperty("entity").GetString()!;
        var entityType = typeof(SimpleOrm.Sample.Models.User).Assembly.GetExportedTypes()
            .Single(t => t.Name == entityName);
        var map = new EntityMapLoader().Load(entityType);
        var asOfVersion = spec.GetProperty("asOfVersion").GetInt64();
        var generatedAt = DateTimeOffset.Parse(spec.GetProperty("generatedAt").GetString()!);
        var kindToken = spec.TryGetProperty("kind", out var kind) ? kind.GetString() : "table";
        var expects = spec.GetProperty("expect");

        foreach (var dialect in Dialects)
        {
            Assert.True(expects.TryGetProperty(dialect.Name, out var expected), $"{fileName}: no expectation for dialect {dialect.Name}");
            var produced = kindToken switch
            {
                "table" => SchemaSnapshot.Export(map, dialect, asOfVersion, generatedAt),
                // A trigger snapshots by declaration (ADR-0042): the case names the trigger the entity links.
                "trigger" => SchemaSnapshot.ExportTrigger(
                    new TriggerSnapshot(
                        map.RelationName!, map.Kind == RelationKind.View,
                        map.Triggers.Single(t => t.Name == spec.GetProperty("trigger").GetString()).Definition),
                    asOfVersion, generatedAt, dialect.Name),
                _ => SchemaSnapshot.ExportDdl(map.RelationName!, kindToken!, dialect.CreateViewSql(map), asOfVersion, generatedAt, dialect.Name),
            };

            using var producedDocument = JsonDocument.Parse(produced);
            Assert.True(
                DeepEquals(expected, producedDocument.RootElement),
                $"{fileName} on {dialect.Name}: produced snapshot differs from the expected document:\n{produced}");
        }
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
