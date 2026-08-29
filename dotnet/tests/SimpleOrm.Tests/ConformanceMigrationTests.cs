using System.Text.Json;
using Microsoft.Data.Sqlite;
using SimpleOrm.Sqlite;
using Xunit;

namespace SimpleOrm.Tests;

/// <summary>
/// The migrations-case runner (§9): each conformance/migrations-cases/*.json builds
/// its migration set as data (SqlVersion) and replays the commands against a fresh
/// database, comparing the recorded (version, object) rows or the error code.
/// A case may carry `snapshots` (the derived-rollback history, ADR-0018), step
/// `renames`/`expectDefinition` (typed renames; the MIG-012 view guard), a raw
/// `sql` command (the outside hotfix), `force` on migrate/down, and deep expects
/// (`columns` per table, `ddl` per view).
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
        var snapshotDir = Path.Combine(Path.GetTempPath(), $"simpleorm_migcase_{Guid.NewGuid():N}");
        try
        {
            var snapshots = LoadSnapshots(spec, snapshotDir);
            await using var db = await Db.OpenAsync(
                $"Data Source={path}", new DbOptions { Dialect = new SqliteDialect() }, CancellationToken.None);

            foreach (var step in spec.GetProperty("run").EnumerateArray())
            {
                var versions = step.TryGetProperty("versions", out var overrideVersions)
                    ? ParseVersions(overrideVersions)
                    : defaultVersions;
                var runner = new MigrationRunner(db, versions, snapshots);
                var force = step.TryGetProperty("force", out var forceFlag) && forceFlag.GetBoolean();
                var expect = step.GetProperty("expect");

                string? error = null;
                try
                {
                    switch (step.GetProperty("command").GetString())
                    {
                        case "migrate":
                            await runner.MigrateAsync(force, notify: null, CancellationToken.None);
                            break;
                        case "down":
                            await runner.MigrateDownAsync(
                                step.GetProperty("to").GetInt64(), force, notify: null, CancellationToken.None);
                            break;
                        case "baseline":
                            await runner.BaselineAsync(step.GetProperty("version").GetInt64(), CancellationToken.None);
                            break;
                        case "sql":
                            // The urgency hotfix: statements applied outside migrations.
                            await SchemaSync.ApplyAsync(
                                db,
                                step.GetProperty("statements").EnumerateArray().Select(s => s.GetString()!),
                                CancellationToken.None);
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
                    if (expect.TryGetProperty("applied", out var applied))
                    {
                        var expected = applied.EnumerateArray()
                            .Select(row => (row[0].GetInt64(), row[1].GetString()!))
                            .OrderBy(r => r)
                            .ToArray();
                        Assert.Equal(expected, await ReadAppliedAsync(db));
                    }
                }

                if (expect.TryGetProperty("columns", out var columnExpects))
                {
                    foreach (var table in columnExpects.EnumerateObject())
                    {
                        var expected = table.Value.EnumerateArray().Select(c => c.GetString()!).OrderBy(c => c).ToArray();
                        Assert.Equal(expected, ReadColumns(path, table.Name));
                    }
                }

                if (expect.TryGetProperty("ddl", out var ddlExpects))
                {
                    foreach (var view in ddlExpects.EnumerateObject())
                    {
                        Assert.Equal(
                            SchemaSnapshot.NormalizeDdl(view.Value.GetString()!),
                            ReadViewDdl(path, view.Name));
                    }
                }
            }
        }
        finally
        {
            SqliteConnection.ClearAllPools();
            File.Delete(path);
            if (Directory.Exists(snapshotDir))
            {
                Directory.Delete(snapshotDir, recursive: true);
            }
        }
    }

    private static SnapshotSet? LoadSnapshots(JsonElement spec, string snapshotDir)
    {
        if (!spec.TryGetProperty("snapshots", out var snapshots))
        {
            return null;
        }

        Directory.CreateDirectory(snapshotDir);
        var index = 0;
        foreach (var snapshot in snapshots.EnumerateArray())
        {
            File.WriteAllText(Path.Combine(snapshotDir, $"s{index++}.schema.json"), snapshot.GetRawText());
        }

        return SnapshotSet.FromDirectory(snapshotDir);
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
                            : null,
                        s.TryGetProperty("renames", out var renames)
                            ? renames.EnumerateArray()
                                .Select(r => (r.GetProperty("from").GetString()!, r.GetProperty("to").GetString()!))
                                .ToArray()
                            : null,
                        s.TryGetProperty("expectDefinition", out var guard) ? guard.GetString() : null))
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

    private static string[] ReadColumns(string databasePath, string table)
    {
        using var connection = new SqliteConnection($"Data Source={databasePath}");
        connection.Open();
        using var command = connection.CreateCommand();
        command.CommandText = "select name from pragma_table_info(@t) order by name";
        command.Parameters.AddWithValue("@t", table);
        var columns = new List<string>();
        using var reader = command.ExecuteReader();
        while (reader.Read())
        {
            columns.Add(reader.GetString(0));
        }

        return [.. columns];
    }

    private static string? ReadViewDdl(string databasePath, string view)
    {
        using var connection = new SqliteConnection($"Data Source={databasePath}");
        connection.Open();
        using var command = connection.CreateCommand();
        command.CommandText = "select sql from sqlite_master where type = 'view' and name = @v";
        command.Parameters.AddWithValue("@v", view);
        return command.ExecuteScalar() is string sql ? SchemaSnapshot.NormalizeDdl(sql) : null;
    }

    public sealed record AppliedRow(long Version, string Object);

    internal static string ConformanceDirectory()
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
