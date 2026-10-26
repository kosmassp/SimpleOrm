<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Conformance;

use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use SimpleOrm\Tests\Support\ConformancePaths;
use SimpleOrm\Tests\Support\StepScriptRunner;

/**
 * The batch-case runner (Level 4 item 3, ADR-0044, spec/session.md "Batching" and "The step scripts"): each
 * `conformance/batch-cases/*.json` is the step-script form with a `batching` header, the `flush` and `batch` verbs
 * carrying `expectMessages` (the plan's statement count per message, observed through the session's test hook), `batchAgain`
 * on a kept batch (`TX-003`), and `expectAffected` — all replayed by the shared {@see StepScriptRunner} against the seeded
 * fixture database under the SQLite dialect's facts. SQLite is sequential in this port (`batchingInEffect()` is false), so
 * the grouping is the plan's, not a count of round trips.
 */
final class BatchCasesTest extends TestCase
{
    /** @return array<string, list<string>> */
    public static function caseFiles(): array
    {
        $cases = [];
        foreach (ConformancePaths::cases('batch-cases') as $file) {
            $cases[$file] = [$file];
        }

        return $cases;
    }

    #[Test]
    #[DataProvider('caseFiles')]
    public function case_behaves_as_specified(string $fileName): void
    {
        StepScriptRunner::replay('batch-cases', $fileName);
    }
}
