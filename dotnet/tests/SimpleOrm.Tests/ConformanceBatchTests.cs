using Xunit;

namespace SimpleOrm.Tests;

/// <summary>
/// The batch-case runner (§9, Level 4 item 3 — ADR-0044): each
/// <c>conformance/batch-cases/*.json</c> runs a flush or an explicit batch under
/// its <c>batching</c> header and pins the plan's messages, the statements, the
/// per-operation counts, and the failure and undo of a batched call
/// (spec/session.md "Batching"), through the same <see cref="StepScript"/> engine.
/// </summary>
public sealed class ConformanceBatchTests
{
    public static TheoryData<string> CaseFiles => ConformanceTrackingTests.CaseFilesOf("batch-cases");

    [Theory]
    [MemberData(nameof(CaseFiles))]
    public Task Case_behaves_as_specified(string fileName) => ConformanceTrackingTests.RunCaseAsync("batch-cases", fileName);
}
