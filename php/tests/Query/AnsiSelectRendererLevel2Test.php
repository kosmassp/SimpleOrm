<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Query;

use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use SimpleOrm\Dialect\Dialect;
use SimpleOrm\Dialect\SqliteDialect;
use SimpleOrm\Errors\SimpleOrmException;
use SimpleOrm\Metadata\EntityMap;
use SimpleOrm\Metadata\EntityMapLoader;
use SimpleOrm\Metadata\MappingOptions;
use SimpleOrm\Metadata\PropertyMap;
use SimpleOrm\Query\Criteria;
use SimpleOrm\Query\JoinPair;
use SimpleOrm\Query\Nodes\SubqueryMembership;
use SimpleOrm\Query\Ordering;
use SimpleOrm\Query\SelectAst;
use SimpleOrm\Query\SelectJoin;
use SimpleOrm\Tests\Sample\Models\Role;
use SimpleOrm\Tests\Sample\Models\Transaction;
use SimpleOrm\Tests\Sample\Models\User;
use SimpleOrm\Tests\Sample\Models\UserRole;

/**
 * Level 2 renderer refusals and the EXISTS rewrite (ADR-0022 add.1,
 * spec/query-ast.md) that the pinned `conformance/ast/level2/*.json` cases
 * don't exercise on SQLite (`supportsRowValueIn()` is true there, so the
 * rewrite never fires on the reference dialect) — a test-local {@see Dialect}
 * decorator flips just that one knob to prove the seam is real.
 */
final class AnsiSelectRendererLevel2Test extends TestCase
{
    private static ?EntityMapLoader $loader = null;

    #[Test]
    public function composite_membership_arity_mismatch_is_qry_006(): void
    {
        $dialect = new SqliteDialect();
        $userRoleMap = self::map(UserRole::class);

        $subquery = new SelectAst(
            $userRoleMap,
            [],
            [],
            projection: [self::property($userRoleMap, 'userId')],   // one column …
        );
        // … against a two-property membership: arity 2 vs 1.
        $membership = new SubqueryMembership(['userId', 'roleId'], $subquery);
        $select = new SelectAst($userRoleMap, [$membership], []);

        $exception = self::renderExpectingException($dialect, $select);
        self::assertSame('QRY-006', $exception->errorCode);
    }

    #[Test]
    public function join_with_undeclared_parent_alias_is_qry_006(): void
    {
        $dialect = new SqliteDialect();
        $userMap = self::map(User::class);
        $roleMap = self::map(Role::class);

        $join = new SelectJoin(
            $roleMap,
            'j0',
            'nope',   // no earlier join declares alias 'nope'
            [new JoinPair('id', 'id')],
            project: true,
        );
        $select = new SelectAst($userMap, [], [], joins: [$join]);

        $exception = self::renderExpectingException($dialect, $select);
        self::assertSame('QRY-006', $exception->errorCode);
    }

    #[Test]
    public function join_with_unknown_property_on_the_parent_side_is_qry_006(): void
    {
        $dialect = new SqliteDialect();
        $transactionMap = self::map(Transaction::class);
        $userMap = self::map(User::class);

        $join = new SelectJoin($userMap, 'j0', null, [new JoinPair('NotAProperty', 'id')], project: true);
        $select = new SelectAst($transactionMap, [], [], joins: [$join]);

        $exception = self::renderExpectingException($dialect, $select);
        self::assertSame('QRY-006', $exception->errorCode);
    }

    /**
     * A join declaring its alias as the root's reserved `t` would otherwise
     * silently overwrite the alias→map entry {@see AnsiSelectRenderer::prepareJoins()}
     * uses to resolve later ON pairs — spec/query-ast.md is silent on this
     * exact case (only an undeclared *parent* alias is pinned), so this is a
     * defensive refusal, not a pinned conformance case.
     */
    #[Test]
    public function a_join_alias_colliding_with_the_root_alias_is_qry_006(): void
    {
        $dialect = new SqliteDialect();
        $userMap = self::map(User::class);
        $roleMap = self::map(Role::class);

        $join = new SelectJoin($roleMap, 't', null, [new JoinPair('id', 'id')], project: true);
        $select = new SelectAst($userMap, [], [], joins: [$join]);

        $exception = self::renderExpectingException($dialect, $select);
        self::assertSame('QRY-006', $exception->errorCode);
    }

    /** The same collision, between two declared joins rather than against the root. */
    #[Test]
    public function two_joins_declaring_the_same_alias_is_qry_006(): void
    {
        $dialect = new SqliteDialect();
        $userMap = self::map(User::class);
        $roleMap = self::map(Role::class);
        $userRoleMap = self::map(UserRole::class);

        $first = new SelectJoin($userRoleMap, 'j0', null, [new JoinPair('id', 'userId')], project: false);
        $second = new SelectJoin($roleMap, 'j0', null, [new JoinPair('id', 'id')], project: true);
        $select = new SelectAst($userMap, [], [], joins: [$first, $second]);

        $exception = self::renderExpectingException($dialect, $select);
        self::assertSame('QRY-006', $exception->errorCode);
    }

    /**
     * Where row-value IN is unsupported (SQL Server; simulated here), a
     * composite membership rewrites as a correlated EXISTS: the root gains
     * alias `t` for the correlation only (its select-list columns stay
     * unaliased, unlike a join's `t.col as t_col`), the subquery becomes
     * derived table `s`, and each compared column correlates `s.col = t.col`.
     */
    #[Test]
    public function composite_membership_rewrites_as_exists_when_row_value_in_is_unsupported(): void
    {
        $dialect = new RowValueInDisabledDialect(new SqliteDialect());
        $userRoleMap = self::map(UserRole::class);

        $subquery = new SelectAst(
            $userRoleMap,
            [Criteria::isNull('grantedBy')],
            [],
            projection: [self::property($userRoleMap, 'userId'), self::property($userRoleMap, 'roleId')],
        );
        $membership = new SubqueryMembership(['userId', 'roleId'], $subquery);
        $select = new SelectAst(
            $userRoleMap,
            [$membership, Criteria::gt('userId', 0)],
            [new Ordering('userId')],
        );

        $bound = [];
        $sql = $dialect->selectSql($select, static function (mixed $value) use (&$bound): string {
            $bound[] = $value;

            return '@c' . (count($bound) - 1);
        });

        self::assertSame(
            'select t.user_id, t.role_id, t.granted_by, t.created_at, t.updated_at from user_roles t '
                . 'where (exists (select 1 from (select user_id, role_id from user_roles where granted_by is null) s '
                . 'where s.user_id = t.user_id and s.role_id = t.role_id) and t.user_id > @c0) order by t.user_id',
            $sql,
        );
        self::assertSame([0], $bound);
    }

    #[Test]
    public function single_property_membership_never_triggers_the_exists_rewrite(): void
    {
        // A single-property `in (select …)` never needs the row-value form, so
        // it renders as plain IN regardless of `supportsRowValueIn()` and the
        // root is never aliased.
        $dialect = new RowValueInDisabledDialect(new SqliteDialect());
        $transactionMap = self::map(Transaction::class);
        $userMap = self::map(User::class);

        $subquery = new SelectAst(
            $userMap,
            [Criteria::eq('name', 'Ada')],
            [],
            projection: [self::property($userMap, 'id')],
        );
        $select = new SelectAst($transactionMap, [new SubqueryMembership(['userId'], $subquery)], []);

        $bound = [];
        $sql = $dialect->selectSql($select, static function (mixed $value) use (&$bound): string {
            $bound[] = $value;

            return '@c' . (count($bound) - 1);
        });

        self::assertSame(
            'select id, user_id, status, amount, version, note, created_at, updated_at from transactions '
                . 'where user_id in (select id from users where name = @c0)',
            $sql,
        );
        self::assertSame(['Ada'], $bound);
    }

    private static function renderExpectingException(Dialect $dialect, SelectAst $select): SimpleOrmException
    {
        try {
            $dialect->selectSql($select, static fn (mixed $value): string => '@c0');
        } catch (SimpleOrmException $exception) {
            return $exception;
        }

        self::fail('expected a SimpleOrmException');
    }

    /** @param class-string $entityType */
    private static function map(string $entityType): EntityMap
    {
        return (self::$loader ??= new EntityMapLoader(MappingOptions::default()))->load($entityType);
    }

    private static function property(EntityMap $map, string $propertyName): PropertyMap
    {
        return $map->property($propertyName) ?? self::fail("no property '{$propertyName}' on {$map->entityName()}");
    }
}
