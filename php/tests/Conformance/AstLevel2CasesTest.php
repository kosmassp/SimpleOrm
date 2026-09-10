<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Conformance;

use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use RuntimeException;
use SimpleOrm\Dialect\SqliteDialect;
use SimpleOrm\Errors\SimpleOrmException;
use SimpleOrm\Metadata\EntityMap;
use SimpleOrm\Metadata\EntityMapLoader;
use SimpleOrm\Metadata\MappingOptions;
use SimpleOrm\Query\AnsiSelectRenderer;
use SimpleOrm\Query\Criteria;
use SimpleOrm\Query\JoinPair;
use SimpleOrm\Query\Nodes\SubqueryMembership;
use SimpleOrm\Query\Ordering;
use SimpleOrm\Query\SelectAst;
use SimpleOrm\Query\SelectJoin;
use SimpleOrm\Query\SortOrder;
use SimpleOrm\Tests\Sample\Models\Role;
use SimpleOrm\Tests\Sample\Models\Transaction;
use SimpleOrm\Tests\Sample\Models\TransactionDetail;
use SimpleOrm\Tests\Sample\Models\User;
use SimpleOrm\Tests\Sample\Models\UserProfile;
use SimpleOrm\Tests\Sample\Models\UserRole;
use SimpleOrm\Tests\Support\ConformancePaths;

/**
 * The Level 2 AST-case runner (spec/query-ast.md "Level 2 extensions"): each
 * `conformance/ast/level2/*.json` builds a criteria query carrying projection,
 * joins, or subquery membership and renders it through {@see SqliteDialect},
 * comparing the exact SQL text and ordered parameter values, or the error
 * code — mirrors {@see AstCasesTest} but loads real `EntityMap`s (through
 * {@see EntityMapLoader}) for the fixture entities under
 * `tests/Sample/Models`, since Level 2 cases join across several of them.
 */
final class AstLevel2CasesTest extends TestCase
{
    private static ?EntityMapLoader $loader = null;

    /** @return array<string, list<string>> */
    public static function caseFiles(): array
    {
        $cases = [];
        foreach (ConformancePaths::cases('ast/level2') as $file) {
            $cases[$file] = [$file];
        }

        return $cases;
    }

    #[Test]
    #[DataProvider('caseFiles')]
    public function renders_as_pinned(string $fileName): void
    {
        $path = ConformancePaths::dir('ast/level2') . DIRECTORY_SEPARATOR . $fileName;
        $spec = json_decode((string) file_get_contents($path), associative: true, flags: JSON_THROW_ON_ERROR);

        $dialect = new SqliteDialect();
        $bound = [];
        $bind = static function (mixed $value) use (&$bound): string {
            $bound[] = $value;

            return '@c' . (count($bound) - 1);
        };

        $sql = null;
        $errorCode = null;
        try {
            $map = self::entityMap($spec['entity']);
            $ast = self::parseSelect($map, $spec['select'] ?? []);
            $sql = $dialect->selectSql($ast, $bind);
        } catch (SimpleOrmException $exception) {
            $errorCode = $exception->errorCode;
        }

        if (isset($spec['expect']['error'])) {
            self::assertSame($spec['expect']['error'], $errorCode, "{$fileName}: expected error code");

            return;
        }

        self::assertNull($errorCode, "{$fileName}: rendering threw unexpectedly ({$errorCode})");
        self::assertArrayHasKey('sqlite', $spec['expect'], "{$fileName} lacks the 'sqlite' expectation (§9: every case pins every dialect)");
        self::assertSame($spec['expect']['sqlite']['sql'], $sql, "{$fileName}: rendered SQL");
        self::assertSame($spec['expect']['sqlite']['parameters'], $bound, "{$fileName}: bound parameter values");
    }

    /** @param array<string, mixed> $select */
    private static function parseSelect(EntityMap $map, array $select): SelectAst
    {
        $queryName = $map->entityName() . ' criteria';

        $projection = isset($select['projection'])
            ? array_map(
                static fn (string $p) => AnsiSelectRenderer::resolve($map, $p, $queryName),
                $select['projection'],
            )
            : null;

        return new SelectAst(
            $map,
            array_map(
                static fn (array $node) => self::parseCriteria($map, $node),
                $select['where'] ?? [],
            ),
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
            $projection,
            array_map(self::parseJoin(...), $select['joins'] ?? []),
        );
    }

    /** @param array<string, mixed> $node */
    private static function parseJoin(array $node): SelectJoin
    {
        return new SelectJoin(
            self::entityMap($node['entity']),
            (string) $node['alias'],
            isset($node['parent']) ? (string) $node['parent'] : null,
            array_map(
                static fn (array $pair): JoinPair => new JoinPair((string) $pair[0], (string) $pair[1]),
                $node['on'] ?? [],
            ),
            (bool) ($node['project'] ?? false),
        );
    }

    /** @param array<string, mixed> $node */
    private static function parseCriteria(EntityMap $map, array $node): Criteria
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
            'and' => Criteria::and(...array_map(
                static fn (array $n) => self::parseCriteria($map, $n),
                $node['args'],
            )),
            'or' => Criteria::or(...array_map(
                static fn (array $n) => self::parseCriteria($map, $n),
                $node['args'],
            )),
            'not' => Criteria::not(self::parseCriteria($map, $node['arg'])),
            'in_select' => new SubqueryMembership(
                array_map(static fn (mixed $p): string => (string) $p, $node['properties']),
                self::parseSelect(self::entityMap($node['select']['entity']), $node['select']),
            ),
            default => throw new RuntimeException("unknown op '{$node['op']}'"),
        };
    }

    private static function entityMap(string $entityShortName): EntityMap
    {
        $class = match ($entityShortName) {
            'User' => User::class,
            'Role' => Role::class,
            'UserRole' => UserRole::class,
            'Transaction' => Transaction::class,
            'TransactionDetail' => TransactionDetail::class,
            'UserProfile' => UserProfile::class,
            default => throw new RuntimeException("no fixture model for entity '{$entityShortName}'"),
        };

        return (self::$loader ??= new EntityMapLoader(MappingOptions::default()))->load($class);
    }
}
