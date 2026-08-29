using SimpleOrm.Sample.Models;
using Xunit;

namespace SimpleOrm.Tests;

/// <summary>
/// The conformance runner for entity metadata: exports every sample model and
/// compares it against conformance/entities/*.json — the files every port must
/// reproduce. Regenerate with SIMPLEORM_CONFORMANCE_WRITE=1.
/// </summary>
public sealed class ConformanceEntityTests
{
    public static TheoryData<Type> Entities => new()
    {
        typeof(User),
        typeof(Role),
        typeof(UserRole),
        typeof(Transaction),
        typeof(TransactionDetail),
        typeof(UserProfile),
        typeof(UserTransactionTotal),
        typeof(MonthlySalesTotal),
        typeof(DailySales),
        typeof(UserActivityReport),
    };

    [Theory]
    [MemberData(nameof(Entities))]
    public void Export_matches_conformance_file(Type entityType)
    {
        var json = EntityMapJson.Export(new EntityMapLoader().Load(entityType));
        var file = Path.Combine(
            ConformanceDirectory(),
            "entities",
            SnakeCaseNamingConvention.Instance.TableName(entityType.Name) + ".json");

        if (Environment.GetEnvironmentVariable("SIMPLEORM_CONFORMANCE_WRITE") == "1")
        {
            Directory.CreateDirectory(Path.GetDirectoryName(file)!);
            File.WriteAllText(file, json + "\n");
            return;
        }

        Assert.True(File.Exists(file), $"missing conformance file {file} — run with SIMPLEORM_CONFORMANCE_WRITE=1");
        var expected = File.ReadAllText(file).Replace("\r\n", "\n").TrimEnd('\n');
        Assert.Equal(expected, json.Replace("\r\n", "\n"));
    }

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
