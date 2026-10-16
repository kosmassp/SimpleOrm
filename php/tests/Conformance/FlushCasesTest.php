<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Conformance;

use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use SimpleOrm\Tests\Support\ConformancePaths;
use SimpleOrm\Tests\Support\StepScriptRunner;

/**
 * The flush-case runner (Level 3 M2, ADR-0034 §7c, spec/unit-of-work.md "Conformance: flush-cases/"):
 * each `conformance/flush-cases/*.json` is the tracking-case step vocabulary plus `persist`/`remove`,
 * `flush` (`expectStatements`, `expectSummary`), `begin`/`commit`/`rollback`, `new.navigations`, and
 * the `pending`/`sameValue`/`count` expectations — all replayed by the shared {@see StepScriptRunner}
 * against the seeded fixture database, the statement order observed at the dialect seam.
 */
final class FlushCasesTest extends TestCase
{
    /** @return array<string, list<string>> */
    public static function caseFiles(): array
    {
        $cases = [];
        foreach (ConformancePaths::cases('flush-cases') as $file) {
            $cases[$file] = [$file];
        }

        return $cases;
    }

    #[Test]
    #[DataProvider('caseFiles')]
    public function case_behaves_as_specified(string $fileName): void
    {
        StepScriptRunner::replay('flush-cases', $fileName);
    }
}
