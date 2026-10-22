<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Conformance;

use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use SimpleOrm\Dialect\Dialects;
use SimpleOrm\Errors\SimpleOrmException;
use SimpleOrm\Tests\Support\ConformancePaths;
use SimpleOrm\Tests\Support\TriggerCaseData;

/**
 * The trigger-rendering runner (§9, ADR-0042): each `conformance/trigger-sql/*.json` is a neutral declaration — relation,
 * relation kind, name, timing, events, update-of columns, body — and the exact create and drop statements every dialect
 * renders for it, or `DDL-005` where the dialect has no form. Every case is rendered through **every** dialect
 * {@see Dialects} ships, byte for byte (§9: every case pins every dialect); pure rendering, no database.
 */
final class TriggerSqlCasesTest extends TestCase
{
    /** @return array<string, list<string>> */
    public static function caseFiles(): array
    {
        $cases = [];
        foreach (ConformancePaths::cases('trigger-sql') as $file) {
            $cases[$file] = [$file];
        }

        return $cases;
    }

    #[Test]
    #[DataProvider('caseFiles')]
    public function renders_as_pinned(string $fileName): void
    {
        foreach (Dialects::names() as $dialectName) {
            $this->renderAsPinned($fileName, $dialectName);
        }
    }

    private function renderAsPinned(string $fileName, string $dialectName): void
    {
        $path = ConformancePaths::dir('trigger-sql') . DIRECTORY_SEPARATOR . $fileName;
        $spec = json_decode((string) file_get_contents($path), associative: true, flags: JSON_THROW_ON_ERROR);

        self::assertArrayHasKey($dialectName, $spec['expect'], "{$fileName} lacks the '{$dialectName}' expectation (§9: every case pins every dialect)");
        $expected = $spec['expect'][$dialectName];

        $dialect = Dialects::create($dialectName);
        $trigger = TriggerCaseData::definition((string) $spec['trigger']['name'], $spec['trigger']);
        $onView = $spec['relationKind'] === 'view';

        if (isset($expected['error'])) {
            try {
                $dialect->createTriggerSql((string) $spec['relation'], $onView, $trigger);
                self::fail("{$fileName} [{$dialectName}]: expected {$expected['error']}");
            } catch (SimpleOrmException $exception) {
                self::assertSame($expected['error'], $exception->errorCode, "{$fileName} [{$dialectName}]");
            }

            return;
        }

        self::assertSame(
            $expected['create'],
            $dialect->createTriggerSql((string) $spec['relation'], $onView, $trigger),
            "{$fileName} [{$dialectName}]: create statements",
        );
        self::assertSame(
            $expected['drop'],
            $dialect->dropTriggerSql((string) $spec['relation'], $trigger->name),
            "{$fileName} [{$dialectName}]: drop statements",
        );
    }
}
