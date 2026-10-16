using Xunit;

namespace SimpleOrm.Tests;

/// <summary>
/// The hook-case runner (§9, Level 3 M3 — spec/hooks.md): audit columns and soft
/// delete replayed as step scripts with the session clock fixed, statements
/// observed through the dialect seam — the same <see cref="StepScript"/> engine
/// as the tracking and flush cases.
/// </summary>
public sealed class ConformanceHookTests
{
    public static TheoryData<string> CaseFiles => ConformanceTrackingTests.CaseFilesOf("hook-cases");

    [Theory]
    [MemberData(nameof(CaseFiles))]
    public Task Case_behaves_as_specified(string fileName) => ConformanceTrackingTests.RunCaseAsync("hook-cases", fileName);
}

/// <summary>The merge-case runner (Level 3 M3 — spec/unit-of-work.md "Detach and merge").</summary>
public sealed class ConformanceMergeTests
{
    public static TheoryData<string> CaseFiles => ConformanceTrackingTests.CaseFilesOf("merge-cases");

    [Theory]
    [MemberData(nameof(CaseFiles))]
    public Task Case_behaves_as_specified(string fileName) => ConformanceTrackingTests.RunCaseAsync("merge-cases", fileName);
}
