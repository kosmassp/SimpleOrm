<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Conformance;

use DateTimeImmutable;
use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use SimpleOrm\Dialect\SqliteDialect;
use SimpleOrm\Metadata\EntityMapLoader;
use SimpleOrm\Migrations\SchemaSnapshot;
use SimpleOrm\Tests\Support\ConformancePaths;

/**
 * The snapshot-case runner (§9, ADR-0017), mirroring
 * `dotnet/tests/SimpleOrm.Tests/ConformanceSnapshotTests.cs`: each
 * `conformance/snapshot-cases/*.json` names a fixture entity, a version, and a
 * pinned generation time, and expects the exact snapshot document — every
 * implementation must export it identically from its own native entity
 * definition. Tables export by columns; views by normalized DDL.
 */
final class SnapshotCasesTest extends TestCase
{
    /** @return list<array{0: string}> */
    public static function caseFiles(): array
    {
        return array_map(
            static fn (string $file): array => [$file],
            ConformancePaths::cases('snapshot-cases'),
        );
    }

    #[Test]
    #[DataProvider('caseFiles')]
    public function export_matches_expected_document(string $fileName): void
    {
        $path = ConformancePaths::dir('snapshot-cases') . DIRECTORY_SEPARATOR . $fileName;
        $spec = json_decode(file_get_contents($path), true, flags: JSON_THROW_ON_ERROR);

        $entityName = $spec['entity'];
        $entityClass = self::findFixtureClass($entityName);
        $map = (new EntityMapLoader())->load($entityClass);
        $dialect = new SqliteDialect();
        $asOfVersion = (int) $spec['asOfVersion'];
        $generatedAt = self::parseGeneratedAt($spec['generatedAt']);

        $kind = $spec['kind'] ?? 'table';
        $produced = $kind !== 'table'
            ? SchemaSnapshot::exportDdl($map->relationName, $kind, $dialect->createViewSql($map), $asOfVersion, $generatedAt)
            : SchemaSnapshot::export(SchemaSnapshot::fromMap($map, $dialect), $asOfVersion, $generatedAt);

        self::assertSame(
            $spec['expect'],
            json_decode($produced, true, flags: JSON_THROW_ON_ERROR),
            "produced snapshot differs from the expected document:\n{$produced}",
        );
    }

    /** @return class-string */
    private static function findFixtureClass(string $entityName): string
    {
        $class = 'SimpleOrm\\Tests\\Sample\\Models\\' . $entityName;
        if (!class_exists($class)) {
            self::fail("fixture entity {$entityName} not found under SimpleOrm\\Tests\\Sample\\Models");
        }

        return $class;
    }

    /** The C# `"o"` format carries 7 fractional digits; PHP parses at most 6, so the 7th is trimmed before parsing. */
    private static function parseGeneratedAt(string $value): DateTimeImmutable
    {
        $trimmed = preg_replace('/(\.\d{6})\d*Z$/', '$1Z', $value);

        return new DateTimeImmutable($trimmed ?? $value);
    }
}
