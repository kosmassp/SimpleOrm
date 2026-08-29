using System.Text.RegularExpressions;
using SimpleOrm.Sample.Models;
using SimpleOrm.Sqlite;
using Xunit;

namespace SimpleOrm.Tests;

/// <summary>
/// ADR-0017: the shadow replayer. A full rebuild replays every version into a
/// throwaway database and must reproduce the committed snapshots exactly (modulo
/// the generation timestamp) — that equality is what proves snapshots derive from
/// history, not from the current model. The range form trusts the committed
/// snapshots at --from and regenerates only the requested slice.
/// </summary>
public sealed class ShadowTests
{
    private static string MigrationsDir()
    {
        for (var dir = new DirectoryInfo(AppContext.BaseDirectory); dir is not null; dir = dir.Parent)
        {
            var candidate = Path.Combine(dir.FullName, "dotnet", "samples", "SimpleOrm.Sample", "Migrations");
            if (Directory.Exists(candidate))
            {
                return candidate;
            }
        }

        throw new InvalidOperationException("sample Migrations directory not found above the test output directory");
    }

    private static string Normalize(string json)
        => Regex.Replace(json, "\"generatedAt\": \"[^\"]+\"", "\"generatedAt\": \"-\"").Replace("\r", string.Empty);

    [Fact]
    public async Task Full_rebuild_reproduces_every_committed_snapshot()
    {
        var committed = MigrationsDir();
        var outDir = Path.Combine(Path.GetTempPath(), $"simpleorm_shadow_{Guid.NewGuid():N}");
        try
        {
            var result = await SqliteShadow.RebuildSnapshotsAsync(
                typeof(User).Assembly, "SimpleOrm.Sample.Migrations", outDir, ct: CancellationToken.None);

            var committedFiles = Directory.GetFiles(committed, "*.schema.json", SearchOption.AllDirectories);
            Assert.Equal(committedFiles.Length, result.WrittenFiles.Count);
            foreach (var file in committedFiles)
            {
                var relative = file.Substring(committed.Length + 1);
                var rebuilt = Path.Combine(outDir, relative);
                Assert.True(File.Exists(rebuilt), $"shadow did not regenerate {relative}");
                Assert.Equal(Normalize(File.ReadAllText(file)), Normalize(File.ReadAllText(rebuilt)));
            }
        }
        finally
        {
            if (Directory.Exists(outDir))
            {
                Directory.Delete(outDir, recursive: true);
            }
        }
    }

    [Fact]
    public async Task Range_rebuild_trusts_the_baseline_and_regenerates_only_the_slice()
    {
        var committed = MigrationsDir();
        var outDir = Path.Combine(Path.GetTempPath(), $"simpleorm_shadow_{Guid.NewGuid():N}");
        try
        {
            // Seed the out dir with the committed snapshots at <= 7: the trusted base.
            foreach (var file in Directory.GetFiles(committed, "*.schema.json", SearchOption.AllDirectories))
            {
                var (_, asOfVersion) = SchemaSnapshot.Parse(File.ReadAllText(file));
                if (asOfVersion <= 7)
                {
                    var target = Path.Combine(outDir, file.Substring(committed.Length + 1));
                    Directory.CreateDirectory(Path.GetDirectoryName(target)!);
                    File.Copy(file, target);
                }
            }

            var result = await SqliteShadow.RebuildSnapshotsAsync(
                typeof(User).Assembly, "SimpleOrm.Sample.Migrations", outDir,
                fromVersion: 7, toVersion: 8, ct: CancellationToken.None);

            // Only V0008 (roles) is in the slice; nothing below 7 was replayed or touched.
            var written = Assert.Single(result.WrittenFiles);
            Assert.EndsWith(Path.Combine("Table", "Role", "V0008.schema.json"), written);
            Assert.Equal(
                Normalize(File.ReadAllText(Path.Combine(committed, "Table", "Role", "V0008.schema.json"))),
                Normalize(File.ReadAllText(written)));
        }
        finally
        {
            if (Directory.Exists(outDir))
            {
                Directory.Delete(outDir, recursive: true);
            }
        }
    }
}
