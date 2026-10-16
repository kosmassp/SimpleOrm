using Xunit;

namespace SimpleOrm.Tests;

/// <summary>
/// The flush-case runner (§9, Level 3 M2 — ADR-0034 §7c): each
/// <c>conformance/flush-cases/*.json</c> schedules a graph and pins the ordered
/// statement sequence a flush issues, the summary, and the state afterwards
/// (spec/unit-of-work.md "Conformance: flush-cases/"), through the same
/// <see cref="StepScript"/> engine as the tracking cases.
/// </summary>
public sealed class ConformanceFlushTests
{
    public static TheoryData<string> CaseFiles => ConformanceTrackingTests.CaseFilesOf("flush-cases");

    [Theory]
    [MemberData(nameof(CaseFiles))]
    public Task Case_behaves_as_specified(string fileName) => ConformanceTrackingTests.RunCaseAsync("flush-cases", fileName);
}
