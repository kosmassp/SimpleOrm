using System.Data.Common;
using System.Text.Json;
using SimpleOrm.Postgres;
using SimpleOrm.Sqlite;
using SimpleOrm.SqlServer;
using Xunit;

namespace SimpleOrm.Tests;

/// <summary>
/// The migrations-case runner (§9): each conformance/migrations-cases/*.json builds
/// its migration set as data (SqlVersion) and replays the commands against a fresh
/// database, comparing the recorded (version, object) rows or the error code.
/// A case may carry `snapshots` (the derived-rollback history, ADR-0018), step
/// `renames`/`expectDefinition` (typed renames; the MIG-012 view guard), a raw
/// `sql` command (the outside hotfix), `force` on migrate/down, and deep expects
/// (`columns` per table, `ddl` per view, `triggers` per trigger). The root folder
/// runs on SQLite; the `postgres/` and `sqlserver/` subfolders (ADR-0043) run the
/// same runner against a live server, each case in a throwaway database the
/// dialect's shadow host provisions, and skip where no server answers.
/// </summary>
public sealed class ConformanceMigrationTests
{
    public static TheoryData<string> CaseFiles => CaseFilesIn(string.Empty);

    public static TheoryData<string> PostgresCaseFiles => CaseFilesIn("postgres");

    public static TheoryData<string> SqlServerCaseFiles => CaseFilesIn("sqlserver");

    private static TheoryData<string> CaseFilesIn(string dialectFolder)
    {
        var data = new TheoryData<string>();
        foreach (var file in Directory.GetFiles(Path.Combine(ConformanceDirectory(), "migrations-cases", dialectFolder), "*.json"))
        {
            data.Add(Path.GetFileName(file));
        }

        return data;
    }

    [Theory]
    [MemberData(nameof(CaseFiles))]
    public Task Case_behaves_as_specified(string fileName)
        => RunAsync(fileName, string.Empty, new SqliteDialect(), new SqliteShadowHost(), adminConnectionString: null);

    [PostgresTheory]
    [MemberData(nameof(PostgresCaseFiles))]
    public Task Postgres_case_behaves_as_specified(string fileName)
        => RunAsync(fileName, "postgres", new PostgresDialect(), new PostgresShadowHost(), PostgresFixture.AdminConnectionString);

    [SqlServerTheory]
    [MemberData(nameof(SqlServerCaseFiles))]
    public Task SqlServer_case_behaves_as_specified(string fileName)
        => RunAsync(fileName, "sqlserver", new SqlServerDialect(), new SqlServerShadowHost(), SqlServerFixture.MasterConnectionString);

    private static async Task RunAsync(string fileName, string dialectFolder, IDialect dialect, IShadowHost host, string? adminConnectionString)
    {
        using var document = JsonDocument.Parse(
            File.ReadAllText(Path.Combine(ConformanceDirectory(), "migrations-cases", dialectFolder, fileName)));
        var spec = document.RootElement;
        var defaultVersions = ParseVersions(spec.GetProperty("versions"));

        var snapshotDir = Path.Combine(Path.GetTempPath(), $"simpleorm_migcase_{Guid.NewGuid():N}");
        var connectionString = await host.ProvisionAsync(adminConnectionString, CancellationToken.None);
        try
        {
            var snapshots = LoadSnapshots(spec, snapshotDir);
            var db = await Db.OpenAsync(connectionString, new DbOptions { Dialect = dialect }, CancellationToken.None);
            await using (db)
            {
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
                            var expected = table.Value.EnumerateArray().Select(c => c.GetString()!).OrderBy(c => c, StringComparer.Ordinal).ToArray();
                            var live = await SchemaShadow.IntrospectAsync(db, table.Name, CancellationToken.None);
                            Assert.Equal(expected, live?.Columns.Select(c => c.Name).OrderBy(c => c, StringComparer.Ordinal).ToArray() ?? []);
                        }
                    }

                    if (expect.TryGetProperty("triggers", out var triggerExpects))
                    {
                        // The live trigger, read back through the dialect and parsed to its declaration (ADR-0042);
                        // null expects it gone. A dialect without a lossless read-back cannot run such a case.
                        var readBack = host.TriggerReadBack ?? throw new InvalidOperationException(
                            $"{dialect.Name} cannot read a trigger back; the case must not carry a 'triggers' expect");
                        using var probe = dialect.CreateConnection(connectionString);
                        await probe.OpenAsync(CancellationToken.None);
                        foreach (var trigger in triggerExpects.EnumerateObject())
                        {
                            var live = readBack.Read(probe, trigger.Name, out _);
                            if (trigger.Value.ValueKind == JsonValueKind.Null)
                            {
                                Assert.Null(live);
                                continue;
                            }

                            Assert.NotNull(live);
                            var expectedTrigger = SchemaSnapshot.ParseTrigger(trigger.Value, trigger.Name);
                            Assert.Equal(expectedTrigger.Relation, live.Relation);
                            Assert.True(expectedTrigger.Definition.SameAs(live.Definition), $"trigger {trigger.Name} differs from the expected declaration");
                        }
                    }

                    if (expect.TryGetProperty("ddl", out var ddlExpects))
                    {
                        // Verbatim dialects only: the stored text, normalized.
                        foreach (var view in ddlExpects.EnumerateObject())
                        {
                            Assert.Equal(
                                SchemaSnapshot.NormalizeDdl(view.Value.GetString()!),
                                await ReadViewDdlAsync(dialect, connectionString, view.Name));
                        }
                    }
                }
            }
        }
        finally
        {
            await host.DropAsync(CancellationToken.None);
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
                        s.TryGetProperty("up", out var up) ? up.EnumerateArray().Select(x => x.GetString()!).ToArray() : [],
                        s.TryGetProperty("down", out var down)
                            ? down.EnumerateArray().Select(x => x.GetString()!).ToArray()
                            : null,
                        s.TryGetProperty("renames", out var renames)
                            ? renames.EnumerateArray()
                                .Select(r => (r.GetProperty("from").GetString()!, r.GetProperty("to").GetString()!))
                                .ToArray()
                            : null,
                        s.TryGetProperty("expectDefinition", out var guard) ? guard.GetString() : null,
                        s.TryGetProperty("trigger", out var trigger)
                            ? SchemaSnapshot.ParseTrigger(trigger, s.GetProperty("object").GetString()!) : null,
                        s.TryGetProperty("expectTrigger", out var expectTrigger)
                            ? SchemaSnapshot.ParseTrigger(expectTrigger, s.GetProperty("object").GetString()!) : null))
                    .ToArray())
            {
                Draft = v.TryGetProperty("draft", out var draft) && draft.GetBoolean(),   // Level 3 M5
            })
            .ToArray();

    private static async Task<(long, string)[]> ReadAppliedAsync(Db db)
    {
        Query<EmptyArgs, AppliedRow> query = Query.Inline("select version, object from schema_version");
        try
        {
            var rows = await db.QueryAsync(query, EmptyArgs.Value, CancellationToken.None);
            return rows.Select(r => (r.Version, r.Object)).OrderBy(r => r).ToArray();
        }
        catch (DbException)
        {
            return [];   // no version table yet
        }
    }

    private static async Task<string?> ReadViewDdlAsync(IDialect dialect, string connectionString, string view)
    {
        using var connection = dialect.CreateConnection(connectionString);
        await connection.OpenAsync(CancellationToken.None);
        using var command = connection.CreateCommand();
        command.CommandText = dialect.ViewDefinitionSql;
        var parameter = command.CreateParameter();
        parameter.ParameterName = "@relation";
        parameter.Value = view;
        command.Parameters.Add(parameter);
        return await command.ExecuteScalarAsync(CancellationToken.None) is string sql ? SchemaSnapshot.NormalizeDdl(sql) : null;
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
