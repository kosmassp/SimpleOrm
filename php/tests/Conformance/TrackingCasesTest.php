<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Conformance;

use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use SimpleOrm\Tests\Support\ConformancePaths;
use SimpleOrm\Tests\Support\StepScriptRunner;

/**
 * The tracking-case runner (Level 3 M1, ADR-0034 add.1, spec/session.md "Conformance:
 * tracking-cases/"): each `conformance/tracking-cases/*.json` replays through the shared
 * {@see StepScriptRunner} — the engine `FlushCasesTest` uses too (CODING-STANDARD §8) — which
 * documents how reference-spelled property names and `expectWritten` are read.
 */
final class TrackingCasesTest extends TestCase
{
    /** @return array<string, list<string>> */
    public static function caseFiles(): array
    {
        $cases = [];
        foreach (ConformancePaths::cases('tracking-cases') as $file) {
            $cases[$file] = [$file];
        }

        return $cases;
    }

    #[Test]
    #[DataProvider('caseFiles')]
    public function case_behaves_as_specified(string $fileName): void
    {
        StepScriptRunner::replay('tracking-cases', $fileName);
    }
}
