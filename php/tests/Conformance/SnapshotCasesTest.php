<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Conformance;

use DateTimeImmutable;
use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use SimpleOrm\Dialect\Dialect;
use SimpleOrm\Dialect\Dialects;
use SimpleOrm\Metadata\EntityMap;
use SimpleOrm\Metadata\EntityMapLoader;
use SimpleOrm\Migrations\SchemaSnapshot;
use SimpleOrm\Tests\Support\ConformancePaths;

/**
 * The snapshot-case runner (§9, ADR-0017, per dialect since ADR-0043), mirroring the reference's: each
 * `conformance/snapshot-cases/*.json` names a fixture entity, a version, and a pinned generation time, and expects the
 * exact snapshot document **per dialect** (`expect` keyed by dialect name) — every implementation must export it
 * identically from its own native entity definition through every dialect it ships. Tables export by columns in the
 * dialect's storage types, views by normalized dialect-neutral DDL, triggers by their neutral declaration; every
 * non-SQLite document carries the dialect stamp. A shipped dialect missing from `expect` fails the suite.
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
        $map = (new EntityMapLoader())->load(self::findFixtureClass($entityName));
        $asOfVersion = (int) $spec['asOfVersion'];
        $generatedAt = self::parseGeneratedAt($spec['generatedAt']);

        foreach (Dialects::names() as $name) {
            self::assertArrayHasKey($name, $spec['expect'], "{$fileName}: a shipped dialect ({$name}) is missing from expect");

            $produced = self::export($spec, $map, Dialects::create($name), $asOfVersion, $generatedAt);
            self::assertSame(
                $spec['expect'][$name],
                json_decode($produced, true, flags: JSON_THROW_ON_ERROR),
                "{$name}: produced snapshot differs from the expected document:\n{$produced}",
            );
        }
    }

    /** @param array<string, mixed> $spec */
    private static function export(array $spec, EntityMap $map, Dialect $dialect, int $asOfVersion, DateTimeImmutable $generatedAt): string
    {
        $kind = $spec['kind'] ?? 'table';

        return match ($kind) {
            'table' => SchemaSnapshot::export(SchemaSnapshot::fromMap($map, $dialect), $asOfVersion, $generatedAt, $dialect->name()),
            // ADR-0042: the linked trigger the case names, as its neutral declaration.
            'trigger' => SchemaSnapshot::exportTrigger(
                SchemaSnapshot::fromTrigger($map, $map->trigger((string) $spec['trigger']) ?? self::fail("{$map->entityName()} links no trigger '{$spec['trigger']}'")),
                $asOfVersion,
                $generatedAt,
                $dialect->name(),
            ),
            default => SchemaSnapshot::exportDdl($map->relationName, $kind, $dialect->createViewSql($map), $asOfVersion, $generatedAt, $dialect->name()),
        };
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
