<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Conformance;

use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use RuntimeException;
use SimpleOrm\Dialect\Dialect;
use SimpleOrm\Dialect\Dialects;
use SimpleOrm\Metadata\EntityMap;
use SimpleOrm\Metadata\EntityMapLoader;
use SimpleOrm\Metadata\MappingOptions;
use SimpleOrm\Metadata\PropertyMap;
use SimpleOrm\Tests\Sample\Models\Role;
use SimpleOrm\Tests\Sample\Models\Transaction;
use SimpleOrm\Tests\Sample\Models\User;
use SimpleOrm\Tests\Sample\Models\UserRole;
use SimpleOrm\Tests\Support\ConformancePaths;

/**
 * The generated-write runner (§9, ADR-0040 add.3): each `conformance/crud-sql/*.json` names a fixture entity and one
 * generated statement (`insert`, `update`, `update_only`, `delete`) and pins its exact SQL text per dialect. The
 * statement is rendered through **every** dialect {@see Dialects} ships and compared byte for byte — pure rendering, no
 * database. A case that lacks a shipped dialect's expectation fails (§9: every case pins every dialect). The entity
 * maps come from the real loader over the fixture models, like {@see AstLevel2CasesTest}.
 */
final class CrudSqlCasesTest extends TestCase
{
    private static ?EntityMapLoader $loader = null;

    /** @return array<string, list<string>> */
    public static function caseFiles(): array
    {
        $cases = [];
        foreach (ConformancePaths::cases('crud-sql') as $file) {
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

    /** One dialect's rendering of a case: the exact SQL text of the named statement. */
    private function renderAsPinned(string $fileName, string $dialectName): void
    {
        $path = ConformancePaths::dir('crud-sql') . DIRECTORY_SEPARATOR . $fileName;
        $spec = json_decode((string) file_get_contents($path), associative: true, flags: JSON_THROW_ON_ERROR);

        self::assertArrayHasKey($dialectName, $spec['expect'], "{$fileName} lacks the '{$dialectName}' expectation (§9: every case pins every dialect)");

        $map = self::entityMap((string) $spec['entity']);
        $sql = self::render(Dialects::create($dialectName), $map, $spec);

        self::assertSame($spec['expect'][$dialectName], $sql, "{$fileName} [{$dialectName}]: rendered SQL");
    }

    /** @param array<string, mixed> $spec */
    private static function render(Dialect $dialect, EntityMap $map, array $spec): string
    {
        return match ($spec['statement']) {
            'insert' => $dialect->insertSql($map),
            'update' => $dialect->updateSql($map),
            'update_only' => $dialect->updateOnlySql($map, self::properties($map, $spec['properties'] ?? [])),
            'delete' => $dialect->deleteSql($map, (bool) ($spec['checkVersion'] ?? false)),
            default => throw new RuntimeException("unknown statement '{$spec['statement']}'"),
        };
    }

    /**
     * The listed properties as the session passes them, in the listed order. The case spells them as the reference does
     * (`UpdatedAtUtc`); this port's property names are camelCase (`updatedAtUtc`, CODING-STANDARD §10), so the first
     * letter is lowered.
     *
     * @param list<string> $names
     * @return list<PropertyMap>
     */
    private static function properties(EntityMap $map, array $names): array
    {
        return array_map(
            static fn (string $name): PropertyMap => $map->property(lcfirst($name))
                ?? throw new RuntimeException("{$map->entityName()} has no property '{$name}'"),
            $names,
        );
    }

    private static function entityMap(string $entityShortName): EntityMap
    {
        $class = match ($entityShortName) {
            'User' => User::class,
            'UserRole' => UserRole::class,
            'Transaction' => Transaction::class,
            'Role' => Role::class,
            default => throw new RuntimeException("no fixture model for entity '{$entityShortName}'"),
        };

        return (self::$loader ??= new EntityMapLoader(MappingOptions::default()))->load($class);
    }
}
