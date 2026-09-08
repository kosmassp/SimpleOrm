<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Conformance;

use DateTimeImmutable;
use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use ReflectionProperty;
use RuntimeException;
use SimpleOrm\Dialect\SqliteDialect;
use SimpleOrm\Errors\SimpleOrmException;
use SimpleOrm\Metadata\ColumnType;
use SimpleOrm\Metadata\EntityMap;
use SimpleOrm\Metadata\KeyStrategy;
use SimpleOrm\Metadata\PropertyMap;
use SimpleOrm\Metadata\RelationKind;
use SimpleOrm\Query\Criteria;
use SimpleOrm\Query\Ordering;
use SimpleOrm\Query\SelectAst;
use SimpleOrm\Query\SortOrder;
use SimpleOrm\Tests\Sample\Models\Role;
use SimpleOrm\Tests\Sample\Models\User;
use SimpleOrm\Tests\Support\ConformancePaths;

/**
 * The AST-case runner (§9, ADR-0020/query-ast.md): each `conformance/ast/*.json`
 * builds a criteria query as data and renders it through `SqliteDialect`,
 * comparing the exact SQL text and the ordered parameter values, or the error
 * code — mirrors dotnet's `ConformanceAstTests`. `EntityMap`s for `User`/`Role`
 * are hand-built below (§9: the metadata loader is a different area's work and
 * may not exist yet); if `SimpleOrm\Metadata\EntityMapLoader` exists this test
 * could load them instead, but the hand-built path is what makes this suite
 * green on its own, so it stays.
 */
final class AstCasesTest extends TestCase
{
    /** @return array<string, list<string>> */
    public static function caseFiles(): array
    {
        $cases = [];
        foreach (ConformancePaths::cases('ast') as $file) {
            $cases[$file] = [$file];
        }

        return $cases;
    }

    #[Test]
    #[DataProvider('caseFiles')]
    public function renders_as_pinned(string $fileName): void
    {
        $path = ConformancePaths::dir('ast') . DIRECTORY_SEPARATOR . $fileName;
        $spec = json_decode((string) file_get_contents($path), associative: true, flags: JSON_THROW_ON_ERROR);

        $map = match ($spec['entity']) {
            'User' => self::userMap(),
            'Role' => self::roleMap(),
            default => throw new RuntimeException("no hand-built EntityMap for entity '{$spec['entity']}'"),
        };

        $dialect = new SqliteDialect();
        $bound = [];
        $bind = static function (mixed $value, ?PropertyMap $property) use (&$bound): string {
            $bound[] = $value;

            return '@c' . (count($bound) - 1);
        };

        $sql = null;
        $errorCode = null;
        try {
            $ast = self::parseSelect($map, $spec['select']);
            $sql = $dialect->selectSql($ast, $bind);
        } catch (SimpleOrmException $exception) {
            $errorCode = $exception->errorCode;
        }

        if (isset($spec['expect']['error'])) {
            self::assertSame($spec['expect']['error'], $errorCode, "{$fileName}: expected error code");

            return;
        }

        self::assertNull($errorCode, "{$fileName}: rendering threw unexpectedly");
        self::assertArrayHasKey('sqlite', $spec['expect'], "{$fileName} lacks the 'sqlite' expectation (§9: every case pins every dialect)");
        self::assertSame($spec['expect']['sqlite']['sql'], $sql, "{$fileName}: rendered SQL");
        self::assertSame($spec['expect']['sqlite']['parameters'], $bound, "{$fileName}: bound parameter values");
    }

    /** @param array<string, mixed> $select */
    private static function parseSelect(EntityMap $map, array $select): SelectAst
    {
        return new SelectAst(
            $map,
            array_map(self::parseCriteria(...), $select['where'] ?? []),
            array_map(
                static fn (array $o): Ordering => new Ordering(
                    (string) $o['property'],
                    match ($o['order'] ?? 'asc') {
                        'asc' => SortOrder::Asc,
                        'desc' => SortOrder::Desc,
                        default => throw new RuntimeException("unknown order '{$o['order']}'"),
                    },
                ),
                $select['orderBy'] ?? [],
            ),
            $select['limit'] ?? null,
            $select['offset'] ?? null,
        );
    }

    /** @param array<string, mixed> $node */
    private static function parseCriteria(array $node): Criteria
    {
        $property = (string) ($node['property'] ?? '');

        return match ($node['op']) {
            'eq' => Criteria::eq($property, $node['value']),
            'ne' => Criteria::ne($property, $node['value']),
            'gt' => Criteria::gt($property, $node['value']),
            'ge' => Criteria::ge($property, $node['value']),
            'lt' => Criteria::lt($property, $node['value']),
            'le' => Criteria::le($property, $node['value']),
            'like' => Criteria::like($property, $node['value']),
            'in' => Criteria::in($property, $node['values']),
            'is_null' => Criteria::isNull($property),
            'is_not_null' => Criteria::isNotNull($property),
            'and' => Criteria::and(...array_map(self::parseCriteria(...), $node['args'])),
            'or' => Criteria::or(...array_map(self::parseCriteria(...), $node['args'])),
            'not' => Criteria::not(self::parseCriteria($node['arg'])),
            default => throw new RuntimeException("unknown op '{$node['op']}'"),
        };
    }

    /** Column list pinned by `conformance/ast/select_all.json`: id, name, email, display_name, created_at, updated_at. */
    private static function userMap(): EntityMap
    {
        return new EntityMap(
            User::class,
            RelationKind::Table,
            'users',
            null,
            null,
            [],
            [
                self::prop(User::class, 'id', 'id', ColumnType::Int64, 'int', key: true, generated: true),
                self::prop(User::class, 'name', 'name', ColumnType::String, 'string'),
                self::prop(User::class, 'email', 'email', ColumnType::String, 'string'),
                self::prop(User::class, 'displayName', 'display_name', ColumnType::String, 'string', nullable: true),
                self::prop(User::class, 'createdAtUtc', 'created_at', ColumnType::DateTime, DateTimeImmutable::class),
                self::prop(User::class, 'updatedAtUtc', 'updated_at', ColumnType::DateTime, DateTimeImmutable::class, nullable: true),
            ],
            KeyStrategy::DatabaseGenerated,
            [],
            [],
        );
    }

    /** Column list pinned by `conformance/ast/select_all.json`: id, role_name, created_at, updated_at. */
    private static function roleMap(): EntityMap
    {
        return new EntityMap(
            Role::class,
            RelationKind::Table,
            'roles',
            null,
            null,
            [],
            [
                self::prop(Role::class, 'id', 'id', ColumnType::Int64, 'int', key: true, generated: true),
                self::prop(Role::class, 'name', 'role_name', ColumnType::String, 'string'),
                self::prop(Role::class, 'createdAtUtc', 'created_at', ColumnType::DateTime, DateTimeImmutable::class),
                self::prop(Role::class, 'updatedAtUtc', 'updated_at', ColumnType::DateTime, DateTimeImmutable::class, nullable: true),
            ],
            KeyStrategy::DatabaseGenerated,
            [],
            [],
        );
    }

    /** @param class-string $class */
    private static function prop(
        string $class,
        string $property,
        string $column,
        ColumnType $type,
        string $phpType,
        bool $nullable = false,
        bool $key = false,
        bool $generated = false,
    ): PropertyMap {
        return new PropertyMap(new ReflectionProperty($class, $property), $column, $type, $phpType, $nullable, $key, $generated);
    }
}
