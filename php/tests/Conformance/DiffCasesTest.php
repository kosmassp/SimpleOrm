<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Conformance;

use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use SimpleOrm\Migrations\MigrationGenerator;
use SimpleOrm\Migrations\SchemaSnapshot;
use SimpleOrm\Tests\Support\ConformancePaths;

/**
 * The diff-case runner (§9, ADR-0017), mirroring
 * `dotnet/tests/SimpleOrm.Tests/ConformanceDiffTests.cs`: each
 * `conformance/diff-cases/*.json` feeds two shapes in snapshot form — the
 * current model and the latest snapshot — plus declared renames through
 * `MigrationGenerator::diff()`, and checks the resulting change exactly: no
 * database, no entities, pure data. `unsupported` entries are substrings
 * (usually the column name): messages differ per language, what they name may
 * not.
 */
final class DiffCasesTest extends TestCase
{
    /** @return list<array{0: string}> */
    public static function caseFiles(): array
    {
        return array_map(
            static fn (string $file): array => [$file],
            ConformancePaths::cases('diff-cases'),
        );
    }

    #[Test]
    #[DataProvider('caseFiles')]
    public function case_diffs_as_specified(string $fileName): void
    {
        $path = ConformancePaths::dir('diff-cases') . DIRECTORY_SEPARATOR . $fileName;
        $spec = json_decode(file_get_contents($path), true, flags: JSON_THROW_ON_ERROR);

        $current = SchemaSnapshot::parse(json_encode($spec['current'], JSON_THROW_ON_ERROR))->schema;
        $snapshot = array_key_exists('snapshot', $spec) && $spec['snapshot'] !== null
            ? SchemaSnapshot::parse(json_encode($spec['snapshot'], JSON_THROW_ON_ERROR))->schema
            : null;
        /** @var array<string, string> $renames */
        $renames = $spec['renames'] ?? [];

        $diff = MigrationGenerator::diff($current, $snapshot, $renames);
        $expect = $spec['expect'];

        self::assertSame($expect['isNew'] ?? false, $diff->isNew);
        self::assertSame(
            self::sortedNames($expect['added'] ?? []),
            self::sortedNames(array_map(static fn ($c) => $c->name, $diff->added)),
        );
        self::assertSame(
            self::sortedNames($expect['removed'] ?? []),
            self::sortedNames(array_map(static fn ($c) => $c->name, $diff->removed)),
        );
        self::assertSame(
            self::sortedPairs($expect['renamed'] ?? []),
            self::sortedPairs(array_map(static fn ($r) => [$r->from, $r->to], $diff->renamed)),
        );
        self::assertSame(
            self::sortedNames($expect['removedIndexes'] ?? []),
            self::sortedNames($diff->removedIndexNames),
        );

        $expectedAddedIndexes = $expect['addedIndexes'] ?? [];
        self::assertCount(count($expectedAddedIndexes), $diff->addedIndexSql);
        foreach ($expectedAddedIndexes as $name) {
            self::assertNotEmpty(array_filter($diff->addedIndexSql, static fn (string $sql) => str_contains($sql, $name)));
        }

        $expectedUnsupported = $expect['unsupported'] ?? [];
        self::assertCount(count($expectedUnsupported), $diff->unsupported);
        foreach ($expectedUnsupported as $fragment) {
            self::assertNotEmpty(array_filter($diff->unsupported, static fn (string $message) => str_contains($message, $fragment)));
        }
    }

    /** @param list<string> $names @return list<string> */
    private static function sortedNames(array $names): array
    {
        sort($names, SORT_STRING);

        return $names;
    }

    /**
     * @param list<array{0: string, 1: string}> $pairs
     * @return list<array{0: string, 1: string}>
     */
    private static function sortedPairs(array $pairs): array
    {
        usort($pairs, static fn (array $a, array $b): int => $a <=> $b);

        return $pairs;
    }
}
