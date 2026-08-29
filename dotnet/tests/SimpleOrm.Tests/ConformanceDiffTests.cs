using System.Text.Json;
using Xunit;

namespace SimpleOrm.Tests;

/// <summary>
/// The diff-case runner (§9, ADR-0017): each conformance/diff-cases/*.json feeds
/// two shapes in snapshot form — the current model and the latest snapshot — plus
/// declared renames through the generator's diff, and checks the resulting change
/// exactly: no database, no entities, pure data. `unsupported` entries are
/// substrings (usually the column name): messages differ per language, what they
/// name may not.
/// </summary>
public sealed class ConformanceDiffTests
{
    public static TheoryData<string> CaseFiles
    {
        get
        {
            var data = new TheoryData<string>();
            foreach (var file in Directory.GetFiles(
                Path.Combine(ConformanceMigrationTests.ConformanceDirectory(), "diff-cases"), "*.json"))
            {
                data.Add(Path.GetFileName(file));
            }

            return data;
        }
    }

    [Theory]
    [MemberData(nameof(CaseFiles))]
    public void Case_diffs_as_specified(string fileName)
    {
        using var document = JsonDocument.Parse(File.ReadAllText(
            Path.Combine(ConformanceMigrationTests.ConformanceDirectory(), "diff-cases", fileName)));
        var spec = document.RootElement;

        var current = SchemaSnapshot.Parse(spec.GetProperty("current").GetRawText()).Schema;
        var snapshot = spec.TryGetProperty("snapshot", out var previous) && previous.ValueKind != JsonValueKind.Null
            ? SchemaSnapshot.Parse(previous.GetRawText()).Schema
            : null;
        var renames = spec.TryGetProperty("renames", out var declared)
            ? declared.EnumerateObject().ToDictionary(p => p.Name, p => p.Value.GetString()!)
            : [];

        var diff = MigrationGenerator.Diff(current, snapshot, renames);
        var expect = spec.GetProperty("expect");

        Assert.Equal(Flag(expect, "isNew"), diff.IsNew);
        Assert.Equal(Names(expect, "added"), diff.Added.Select(c => c.Name).OrderBy(n => n));
        Assert.Equal(Names(expect, "removed"), diff.Removed.Select(c => c.Name).OrderBy(n => n));
        Assert.Equal(
            Pairs(expect, "renamed"),
            diff.Renamed.Select(r => (r.From, r.To)).OrderBy(r => r));
        Assert.Equal(Names(expect, "removedIndexes"), diff.RemovedIndexNames.OrderBy(n => n));

        var expectedAddedIndexes = Names(expect, "addedIndexes").ToArray();
        Assert.Equal(expectedAddedIndexes.Length, diff.AddedIndexSql.Count);
        foreach (var name in expectedAddedIndexes)
        {
            Assert.Contains(diff.AddedIndexSql, sql => sql.Contains(name));
        }

        var expectedUnsupported = Names(expect, "unsupported").ToArray();
        Assert.Equal(expectedUnsupported.Length, diff.Unsupported.Count);
        foreach (var fragment in expectedUnsupported)
        {
            Assert.Contains(diff.Unsupported, message => message.Contains(fragment));
        }
    }

    private static bool Flag(JsonElement expect, string name)
        => expect.TryGetProperty(name, out var value) && value.GetBoolean();

    private static IEnumerable<string> Names(JsonElement expect, string name)
        => expect.TryGetProperty(name, out var value)
            ? value.EnumerateArray().Select(v => v.GetString()!).OrderBy(n => n).ToArray()
            : [];

    private static IEnumerable<(string, string)> Pairs(JsonElement expect, string name)
        => expect.TryGetProperty(name, out var value)
            ? value.EnumerateArray().Select(v => (v[0].GetString()!, v[1].GetString()!)).OrderBy(p => p).ToArray()
            : [];
}
