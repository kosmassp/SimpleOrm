using System.Text.Json;
using Microsoft.Data.Sqlite;
using SimpleOrm.Sqlite;
using Xunit;

namespace SimpleOrm.Tests;

/// <summary>
/// The migrations-case runner (§9): each conformance/migrations-cases/*.json builds
/// its migration set as data (SqlVersion) and replays the commands against a fresh
/// database, comparing the recorded (version, object) rows or the error code.
/// </summary>
public sealed class ConformanceMigrationTests
{
    public static TheoryData<string> CaseFiles
    {
        get
        {
            var data = new TheoryData<string>();
            foreach (var file in Directory.GetFiles(Path.Combine(ConformanceDirectory(), "migrations-cases"), "*.json"))
            {
                data.Add(Path.GetFileName(file));
            }

            return data;
        }
    }

    [Theory]
    [MemberData(nameof(CaseFiles))]
    public async Task Case_behaves_as_specified(string fileName)
    {
        using var document = JsonDocument.Parse(
            File.ReadAllText(Path.Combine(ConformanceDirectory(), "migrations-cases", fileName)));
        var spec = document.RootElement;
        var defaultVersions = ParseVersions(spec.GetProperty("versions"));

        var path = Path.Combine(Path.GetTempPath(), $"simpleorm_migcase_{Guid.NewGuid():N}.db");
        try
        {
            await using var db = await Db.OpenAsync(
                $"Data Source={path}", new DbOptions { Dialect = new SqliteDialect() }, CancellationToken.None);

            foreach (var step in spec.GetProperty("run").EnumerateArray())
            {
                var versions = step.TryGetProperty("versions", out var overrideVersions)
                    ? ParseVersions(overrideVersions)
                    : defaultVersions;
                var runner = new MigrationRunner(db, versions);
                var expect = step.GetProperty("expect");

                string? error = null;
                try
                {
                    switch (step.GetProperty("command").GetString())
                    {
                        case "migrate":
                            await runner.MigrateAsync(CancellationToken.None);
                            break;
                        case "down":
                            await runner.MigrateDownAsync(step.GetProperty("to").GetInt64(), CancellationToken.None);
                            break;
                        case "baseline":
                            await runner.BaselineAsync(step.GetProperty("version").GetInt64(), CancellationToken.None);
                            break;
                        default:
                            throw new InvalidOperationException("unknown command");
                    }
                }
                catch (SimpleOrmException exception)
                {
                    error = exception.Code;
                }

                if (expect.TryGetProperty("error", out var expectedError))
                {
                    Assert.Equal(expectedError.GetString(), error);
                }
                else
                {
                    Assert.Null(error);
                    var expected = expect.GetProperty("applied").EnumerateArray()
                        .Select(row => (row[0].GetInt64(), row[1].GetString()!))
                        .OrderBy(r => r)
                        .ToArray();
                    Assert.Equal(expected, await ReadAppliedAsync(db));
                }
            }
        }
        finally
        {
            SqliteConnection.ClearAllPools();
            File.Delete(path);
        }
    }

    private static IReadOnlyList<MigrationVersion> ParseVersions(JsonElement element)
        => element.EnumerateArray()
            .Select(v => (MigrationVersion)new SqlVersion(
                v.GetProperty("version").GetInt64(),
                v.GetProperty("steps").EnumerateArray()
                    .Select(s => new SqlVersion.Step(
                        s.GetProperty("object").GetString()!,
                        s.GetProperty("description").GetString()!,
                        s.GetProperty("up").EnumerateArray().Select(x => x.GetString()!).ToArray(),
                        s.TryGetProperty("down", out var down)
                            ? down.EnumerateArray().Select(x => x.GetString()!).ToArray()
                            : null))
                    .ToArray()))
            .ToArray();

    private static async Task<(long, string)[]> ReadAppliedAsync(Db db)
    {
        Query<EmptyArgs, AppliedRow> query = Query.Inline("select version, object from schema_version");
        try
        {
            var rows = await db.QueryAsync(query, EmptyArgs.Value, CancellationToken.None);
            return rows.Select(r => (r.Version, r.Object)).OrderBy(r => r).ToArray();
        }
        catch (SqliteException)
        {
            return [];
        }
    }

    public sealed record AppliedRow(long Version, string Object);

    private static string ConformanceDirectory()
    {
        for (var dir = new DirectoryInfo(AppContext.BaseDirectory); dir is not null; dir = dir.Parent)
        {
            var candidate = Path.Combine(dir.FullName, "conformance");
            if (Directory.Exists(candidate))
            {
                return candidate;
            }
        }

        throw new InvalidOperationException("conformance/ directory not found above the test output directory");
    }
}
