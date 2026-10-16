<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Conformance;

use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use SimpleOrm\Tests\Support\ConformancePaths;
use SimpleOrm\Tests\Support\StepScriptRunner;

/**
 * The hook-case runner (Level 3 M3, ADR-0034 §7c, spec/hooks.md "Conformance: hook-cases/"): each
 * `conformance/hook-cases/*.json` is the tracking/flush step vocabulary run with the session clock fixed at
 * `2026-08-29T00:00:00Z` — replayed by the shared {@see StepScriptRunner}, the statements observed at the dialect seam.
 */
final class HookCasesTest extends TestCase
{
    /** @return array<string, list<string>> */
    public static function caseFiles(): array
    {
        $cases = [];
        foreach (ConformancePaths::cases('hook-cases') as $file) {
            $cases[$file] = [$file];
        }

        return $cases;
    }

    #[Test]
    #[DataProvider('caseFiles')]
    public function case_behaves_as_specified(string $fileName): void
    {
        StepScriptRunner::replay('hook-cases', $fileName);
    }
}
