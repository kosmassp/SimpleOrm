<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Conformance;

use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use SimpleOrm\Tests\Support\ConformancePaths;
use SimpleOrm\Tests\Support\StepScriptRunner;

/**
 * The merge-case runner (Level 3 M3, ADR-0034 §7c, spec/unit-of-work.md "Detach and merge"): each
 * `conformance/merge-cases/*.json` is the tracking/flush step vocabulary plus `detach` (`ref`) and
 * `merge` (`ref`, `as` — the returned instance), replayed by the shared {@see StepScriptRunner}.
 */
final class MergeCasesTest extends TestCase
{
    /** @return array<string, list<string>> */
    public static function caseFiles(): array
    {
        $cases = [];
        foreach (ConformancePaths::cases('merge-cases') as $file) {
            $cases[$file] = [$file];
        }

        return $cases;
    }

    #[Test]
    #[DataProvider('caseFiles')]
    public function case_behaves_as_specified(string $fileName): void
    {
        StepScriptRunner::replay('merge-cases', $fileName);
    }
}
