using System.Text.Json;
using Microsoft.Data.Sqlite;
using SimpleOrm.Sqlite;
using Xunit;

namespace SimpleOrm.Tests;

/// <summary>
/// The tracking-case runner (§9, Level 3 M1 — ADR-0034 add.1): each
/// <c>conformance/tracking-cases/*.json</c> opens the seeded fixture database in
/// the declared tracking mode and replays its step script through
/// <see cref="StepScript"/> (spec/session.md "Tracking").
/// </summary>
public sealed class ConformanceTrackingTests
{
    public static TheoryData<string> CaseFiles => CaseFilesOf("tracking-cases");

    [Theory]
    [MemberData(nameof(CaseFiles))]
    public Task Case_behaves_as_specified(string fileName) => RunCaseAsync("tracking-cases", fileName);

    internal static TheoryData<string> CaseFilesOf(string folder)
    {
        var data = new TheoryData<string>();
        foreach (var file in Directory.GetFiles(Path.Combine(StepScript.ConformanceDirectory(), folder), "*.json"))
        {
            data.Add(Path.GetFileName(file));
        }

        return data;
    }

    /// <summary>Builds and seeds the fixture database, opens the session in the case's mode behind a recording dialect, and runs the script.</summary>
    internal static async Task RunCaseAsync(string folder, string fileName)
    {
        using var document = JsonDocument.Parse(
            File.ReadAllText(Path.Combine(StepScript.ConformanceDirectory(), folder, fileName)));
        var root = document.RootElement;
        var path = Path.Combine(Path.GetTempPath(), $"simpleorm_{folder}_{Guid.NewGuid():N}.db");
        try
        {
            await ConformanceDatabase.BuildAsync(path, StepScript.ConformanceDirectory());
            var dialect = new RecordingDialect(new SqliteDialect());
            var mode = root.GetProperty("tracking").GetString() == "tracked" ? TrackingMode.Tracked : TrackingMode.Untracked;
            // No pooling: the file is deleted right after, and a pool shared with the
            // other runners (which clear it) is one more thing to race against.
            await using (var db = await Db.OpenAsync(
                $"Data Source={path};Pooling=False", new DbOptions { Dialect = dialect, Tracking = mode }, CancellationToken.None))
            {
                await new StepScript(db, dialect, path).RunAsync(root);
            }
        }
        finally
        {
            SqliteConnection.ClearAllPools();
            File.Delete(path);
        }
    }
}
