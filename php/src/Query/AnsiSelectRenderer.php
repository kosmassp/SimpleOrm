<?php

declare(strict_types=1);

namespace SimpleOrm\Query;

use InvalidArgumentException;
use LogicException;
use SimpleOrm\Dialect\Dialect;
use SimpleOrm\Errors\SimpleOrmException;
use SimpleOrm\Metadata\EntityMap;
use SimpleOrm\Metadata\PropertyMap;
use SimpleOrm\Query\Nodes\Comparison;
use SimpleOrm\Query\Nodes\Composite;
use SimpleOrm\Query\Nodes\InList;
use SimpleOrm\Query\Nodes\Negation;
use SimpleOrm\Query\Nodes\NullCheck;
use SimpleOrm\Query\Nodes\SubqueryMembership;

/**
 * The shared ANSI rendering of a {@see SelectAst} (ADR-0020/0022 add.1,
 * spec/query-ast.md), mirroring dotnet/src/SimpleOrm/AnsiSelectRenderer.cs.
 * Explicit column list (never `*`), parameters bound in render order (`@c0…`:
 * WHERE first, then limit, then offset), dialect knobs consulted where SQL
 * differs. A dialect's `selectSql` normally delegates here.
 *
 * Null semantics are explicit and strict (ADR-0020): `eq(p, null)` renders
 * `is null` and `ne(p, null)` renders `is not null`; any other comparison with
 * null, or a null inside an IN list, is `QRY-007`. Degenerate composites render
 * their identity truth-values — an empty IN or OR is `1 = 0`, an empty AND is
 * `1 = 1`. Negative limit/offset is `QRY-008`.
 *
 * Level 2 (ADR-0022 add.1): an optional `$projection` restricts the root's
 * columns; `$joins` render LEFT JOINs, aliasing the root `t` and every
 * projected column `<alias>_<column>` so segment readers can partition a row;
 * a composite `SubqueryMembership` renders as a row value `in (select …)`
 * where the dialect supports it, else a correlated EXISTS that aliases the
 * root `t` for the correlation only (no column re-aliasing). Once a select's
 * root is aliased — by a join or by an EXISTS rewrite anywhere in its
 * predicate tree — every root column reference in that select qualifies with
 * the alias: predicates, orderings, and every other membership's columns
 * (spec/query-ast.md "Clarifications").
 */
final class AnsiSelectRenderer
{
    /** @param callable(mixed, ?PropertyMap): string $bindParameter */
    public static function selectSql(Dialect $dialect, SelectAst $select, callable $bindParameter): string
    {
        $map = $select->map;
        $queryName = $map->entityName() . ' criteria';

        if (($select->limit !== null && $select->limit < 0) || ($select->offset !== null && $select->offset < 0)) {
            $which = $select->limit !== null && $select->limit < 0
                ? 'limit ' . $select->limit
                : 'offset ' . $select->offset;
            throw new SimpleOrmException(
                'QRY-008',
                $queryName,
                "negative {$which} — dialects disagree on its meaning (SQLite: no limit at all); "
                    . 'refuse the arithmetic bug instead',
            );
        }

        $relation = $map->relationName ?? throw new LogicException(
            "{$queryName}: a criteria query needs a named relation (QRY-005 is the session's gate before rendering)",
        );

        // SPEC-GAP: joins are validated (and thus refused with QRY-006) before
        // the WHERE/ORDER BY tree is resolved. Neither spec/query-ast.md nor
        // conformance/ast/level2 says which refusal wins when a select carries
        // more than one simultaneously (e.g. an undeclared join parent alias
        // *and* an unrelated unknown WHERE property) — every pinned case
        // exercises exactly one problem at a time. The error code is QRY-006
        // either way, so this ordering is unobservable in the conformance suite.
        $preparedJoins = self::prepareJoins($select->joins, $map, $queryName, $dialect);
        $hasJoins = $preparedJoins !== [];
        $rootAlias = ($hasJoins || self::treeNeedsExistsRewrite($select->where, $dialect)) ? 't' : null;

        $projectionProps = $select->projection ?? $map->properties;

        if ($hasJoins) {
            $columns = array_map(
                static fn (PropertyMap $p): string => self::aliasedColumn('t', $p, $dialect),
                $projectionProps,
            );
            foreach ($preparedJoins as $prepared) {
                if (!$prepared['join']->project) {
                    continue;   // an unprojected join (e.g. a many-to-many's link) contributes no columns
                }

                foreach ($prepared['join']->target->properties as $property) {
                    $columns[] = self::aliasedColumn($prepared['join']->alias, $property, $dialect);
                }
            }
        } else {
            $columns = array_map(
                static fn (PropertyMap $p): string => self::qualifiedIdentifier($p->columnName, $dialect, $rootAlias),
                $projectionProps,
            );
        }

        $sql = 'select ' . implode(', ', $columns) . ' from ' . $dialect->quoteIdentifier($relation)
            . ($rootAlias !== null ? " {$rootAlias}" : '');

        foreach ($preparedJoins as $prepared) {
            $join = $prepared['join'];
            $targetRelation = $join->target->relationName ?? throw new LogicException(
                "{$queryName}: join '{$join->alias}' targets a relation-less entity",
            );
            $sql .= ' left join ' . $dialect->quoteIdentifier($targetRelation) . " {$join->alias} on {$prepared['onSql']}";
        }

        if ($select->where !== []) {
            $predicate = count($select->where) === 1 ? $select->where[0] : Criteria::and(...$select->where);
            $sql .= ' where ' . self::render($predicate, $map, $queryName, $dialect, $bindParameter, $rootAlias);
        }

        if ($select->orderings !== []) {
            $sql .= ' order by ' . implode(', ', array_map(
                static fn (Ordering $o): string => self::qualifiedColumn($map, $o->property, $queryName, $dialect, $rootAlias)
                    . ($o->order === SortOrder::Desc ? ' desc' : ''),
                $select->orderings,
            ));
        }

        if ($select->limit !== null || $select->offset !== null) {
            if ($select->orderings === [] && $dialect->pagingRequiresOrderBy()) {
                // The dialect's paging clause is only legal after ORDER BY (SQL
                // Server); an unordered page is already order-arbitrary, so the
                // constant placeholder changes nothing observable (ADR-0024).
                $sql .= ' order by (select null)';
            }

            $limitParameter = $select->limit !== null ? $bindParameter($select->limit, null) : null;
            $offsetParameter = $select->offset !== null ? $bindParameter($select->offset, null) : null;
            $clause = $dialect->limitOffsetClause($limitParameter, $offsetParameter);
            if ($clause !== '') {
                $sql .= ' ' . $clause;
            }
        }

        return $sql;
    }

    /** @param callable(mixed, ?PropertyMap): string $bind */
    private static function render(
        Criteria $criteria,
        EntityMap $map,
        string $queryName,
        Dialect $dialect,
        callable $bind,
        ?string $rootAlias,
    ): string {
        if ($criteria instanceof Comparison && $criteria->value === null) {
            return match ($criteria->operator) {
                '=' => self::qualifiedColumn($map, $criteria->property, $queryName, $dialect, $rootAlias) . ' is null',
                '<>' => self::qualifiedColumn($map, $criteria->property, $queryName, $dialect, $rootAlias) . ' is not null',
                default => throw new SimpleOrmException(
                    'QRY-007',
                    $queryName,
                    "'{$criteria->property} {$criteria->operator} null' has no meaning in SQL; use isNull()/isNotNull()",
                ),
            };
        }

        if ($criteria instanceof Comparison) {
            $property = self::resolve($map, $criteria->property, $queryName);

            return self::qualifiedColumn($map, $criteria->property, $queryName, $dialect, $rootAlias)
                . " {$criteria->operator} " . $bind($criteria->value, $property);
        }

        if ($criteria instanceof InList && $criteria->values === []) {
            self::resolve($map, $criteria->property, $queryName);   // an unknown property is QRY-006 even when empty

            return '1 = 0';
        }

        if ($criteria instanceof InList) {
            foreach ($criteria->values as $value) {
                if ($value === null) {
                    throw new SimpleOrmException(
                        'QRY-007',
                        $queryName,
                        "the IN list for '{$criteria->property}' contains null, which SQL IN can never match; "
                            . 'combine Criteria::or(Criteria::in(…), Criteria::isNull(…))',
                    );
                }
            }

            $property = self::resolve($map, $criteria->property, $queryName);

            return self::qualifiedColumn($map, $criteria->property, $queryName, $dialect, $rootAlias) . ' in ('
                . implode(', ', array_map(static fn (mixed $v): string => $bind($v, $property), $criteria->values))
                . ')';
        }

        if ($criteria instanceof NullCheck) {
            return self::qualifiedColumn($map, $criteria->property, $queryName, $dialect, $rootAlias)
                . ($criteria->negated ? ' is not null' : ' is null');
        }

        if ($criteria instanceof SubqueryMembership) {
            return self::renderMembership($criteria, $map, $queryName, $dialect, $bind, $rootAlias);
        }

        if ($criteria instanceof Composite && $criteria->children === []) {
            // The identity truth-values: an empty AND is true, an empty OR is
            // false — dynamic composition may legitimately produce either, and
            // invalid SQL ("()") names nothing (§2).
            return $criteria->operator === 'and' ? '1 = 1' : '1 = 0';
        }

        if ($criteria instanceof Composite) {
            return '(' . implode(
                " {$criteria->operator} ",
                array_map(
                    static fn (Criteria $child): string => self::render($child, $map, $queryName, $dialect, $bind, $rootAlias),
                    $criteria->children,
                ),
            ) . ')';
        }

        if ($criteria instanceof Negation) {
            return 'not ' . self::render($criteria->inner, $map, $queryName, $dialect, $bind, $rootAlias);
        }

        throw new InvalidArgumentException('unknown criteria node ' . $criteria::class);
    }

    /**
     * Subquery membership (ADR-0022 add.1): the listed root properties, as a row
     * value when more than one, `in (select …)` over a nested select whose
     * projection arity must match (`QRY-006` otherwise). A single property never
     * needs the row-value form and so never triggers the EXISTS rewrite or root
     * aliasing. A composite membership renders as a row value where the dialect
     * supports it (`Dialect::supportsRowValueIn`), else as a correlated EXISTS
     * over the same subquery as derived table `s` — the root's alias (already
     * `t`, forced by this very rewrite) correlates each projected subquery
     * column against the matching root property, with no column re-aliasing
     * (that is a join-only concern).
     *
     * @param callable(mixed, ?PropertyMap): string $bind
     */
    private static function renderMembership(
        SubqueryMembership $criteria,
        EntityMap $map,
        string $queryName,
        Dialect $dialect,
        callable $bind,
        ?string $rootAlias,
    ): string {
        $subProjection = $criteria->subquery->projection ?? $criteria->subquery->map->properties;

        if (count($subProjection) !== count($criteria->properties)) {
            throw new SimpleOrmException(
                'QRY-006',
                $queryName,
                'in_select over [' . implode(', ', $criteria->properties) . '] needs a subquery projection of '
                    . count($criteria->properties) . ' column(s); found ' . count($subProjection),
            );
        }

        $resolvedRootProperties = array_map(
            static fn (string $p): PropertyMap => self::resolve($map, $p, $queryName),
            $criteria->properties,
        );

        // SPEC-GAP (spec/query-ast.md "Subquery membership" says only "the same
        // renderer with the same bind function", not whether that means this
        // static class or the `Dialect::selectSql` entry point a dialect
        // normally delegates to it): dispatched through `$dialect->selectSql()`
        // rather than calling `self::selectSql()` directly, so a future dialect
        // that overrides top-level rendering also governs its own subqueries.
        // SQLite's `selectSql()` just delegates back here, so this is
        // unobservable on the only implemented dialect.
        if (count($resolvedRootProperties) === 1) {
            $subSql = $dialect->selectSql($criteria->subquery, $bind);

            return self::qualifiedColumn($map, $criteria->properties[0], $queryName, $dialect, $rootAlias)
                . ' in (' . $subSql . ')';
        }

        if ($dialect->supportsRowValueIn()) {
            $columns = implode(', ', array_map(
                static fn (string $p): string => self::qualifiedColumn($map, $p, $queryName, $dialect, $rootAlias),
                $criteria->properties,
            ));
            $subSql = $dialect->selectSql($criteria->subquery, $bind);

            return "({$columns}) in ({$subSql})";
        }

        // Row-value IN unsupported (ADR-0024; SQL Server): a correlated EXISTS
        // over the same subquery as a derived table `s`. `$rootAlias` is `t`
        // here — this very rewrite is why `treeNeedsExistsRewrite` aliased it.
        $subSql = $dialect->selectSql($criteria->subquery, $bind);
        $correlations = [];
        foreach ($resolvedRootProperties as $i => $rootProperty) {
            $subColumn = $dialect->quoteIdentifier($subProjection[$i]->columnName);
            $correlations[] = "s.{$subColumn} = "
                . self::qualifiedColumn($map, $criteria->properties[$i], $queryName, $dialect, $rootAlias);
        }

        return 'exists (select 1 from (' . $subSql . ') s where ' . implode(' and ', $correlations) . ')';
    }

    /**
     * Whether rendering `$where` needs the root aliased for a correlated EXISTS
     * rewrite: a composite (arity > 1) {@see SubqueryMembership} where the
     * dialect's row-value IN is unsupported, anywhere in the tree (composites
     * and negations included) — never a nested subquery's own tree, which
     * renders independently with its own aliasing decision.
     *
     * @param list<Criteria> $where
     */
    private static function treeNeedsExistsRewrite(array $where, Dialect $dialect): bool
    {
        foreach ($where as $criteria) {
            if (self::nodeNeedsExistsRewrite($criteria, $dialect)) {
                return true;
            }
        }

        return false;
    }

    private static function nodeNeedsExistsRewrite(Criteria $criteria, Dialect $dialect): bool
    {
        if ($criteria instanceof SubqueryMembership) {
            return count($criteria->properties) > 1 && !$dialect->supportsRowValueIn();
        }

        if ($criteria instanceof Composite) {
            foreach ($criteria->children as $child) {
                if (self::nodeNeedsExistsRewrite($child, $dialect)) {
                    return true;
                }
            }

            return false;
        }

        if ($criteria instanceof Negation) {
            return self::nodeNeedsExistsRewrite($criteria->inner, $dialect);
        }

        return false;
    }

    /**
     * Validates and prepares the join list (ADR-0022 add.1): each join's parent
     * alias must be the root (`null`) or an earlier join's alias in this same
     * list (`QRY-006` otherwise); each ON pair's properties resolve through the
     * parent's map and the target's map respectively (`QRY-006` on either side).
     * Rendered eagerly here so the refusal happens before any SQL is built, even
     * for a join list with no WHERE/ORDER BY referencing it.
     *
     * @param list<SelectJoin> $joins
     * @return list<array{join: SelectJoin, onSql: string}>
     */
    private static function prepareJoins(array $joins, EntityMap $rootMap, string $queryName, Dialect $dialect): array
    {
        // SPEC-GAP: the root's reserved alias 't' is seeded into the same
        // alias→map table a declared join's own alias is recorded into. Neither
        // spec/query-ast.md nor any level2 case says what happens if a join
        // declares its alias as literally 't' (or reuses an earlier join's
        // alias) — no pinned case does either. Left unvalidated: such a join
        // silently overwrites this entry, so a later join's ON pair or
        // `parent` reference could resolve against the wrong map instead of
        // refusing. A real second dialect/port should decide whether that is
        // its own QRY-006 case.
        /** @var array<string, EntityMap> $mapsByAlias root under the alias every join sees the root as: 't' */
        $mapsByAlias = ['t' => $rootMap];
        $prepared = [];

        foreach ($joins as $join) {
            $parentAlias = $join->parentAlias ?? 't';
            $parentMap = $mapsByAlias[$parentAlias] ?? throw new SimpleOrmException(
                'QRY-006',
                $queryName,
                "join '{$join->alias}' names parent alias '{$parentAlias}', which no earlier join declares",
            );

            $onClauses = [];
            foreach ($join->on as $pair) {
                $parentProperty = self::resolve($parentMap, $pair->parentProperty, $queryName);
                $targetProperty = self::resolve($join->target, $pair->targetProperty, $queryName);
                $onClauses[] = "{$join->alias}.{$dialect->quoteIdentifier($targetProperty->columnName)}"
                    . " = {$parentAlias}.{$dialect->quoteIdentifier($parentProperty->columnName)}";
            }

            $prepared[] = ['join' => $join, 'onSql' => implode(' and ', $onClauses)];
            $mapsByAlias[$join->alias] = $join->target;
        }

        return $prepared;
    }

    /** `<alias>.<col> as <alias>_<col>` (join-mode eager loading's segment-reader convention). */
    private static function aliasedColumn(string $alias, PropertyMap $property, Dialect $dialect): string
    {
        $column = $dialect->quoteIdentifier($property->columnName);
        $aliasedName = $dialect->quoteIdentifier("{$alias}_{$property->columnName}");

        return "{$alias}.{$column} as {$aliasedName}";
    }

    /** A resolved property's column, qualified with `$rootAlias.` when the select's root is aliased. */
    private static function qualifiedColumn(
        EntityMap $map,
        string $property,
        string $queryName,
        Dialect $dialect,
        ?string $rootAlias,
    ): string {
        return self::qualifiedIdentifier(self::resolve($map, $property, $queryName)->columnName, $dialect, $rootAlias);
    }

    private static function qualifiedIdentifier(string $columnName, Dialect $dialect, ?string $rootAlias): string
    {
        $column = $dialect->quoteIdentifier($columnName);

        return $rootAlias === null ? $column : "{$rootAlias}.{$column}";
    }

    /**
     * Property-name resolution: exact match first, then case-insensitive — a
     * case-insensitive match that fits more than one property is ambiguous
     * (`QRY-006`), never a silent first-wins.
     */
    public static function resolve(EntityMap $map, string $property, string $queryName): PropertyMap
    {
        foreach ($map->properties as $candidate) {
            if ($candidate->propertyName() === $property) {
                return $candidate;
            }
        }

        $matches = array_values(array_filter(
            $map->properties,
            static fn (PropertyMap $p): bool => strcasecmp($p->propertyName(), $property) === 0,
        ));

        return match (count($matches)) {
            1 => $matches[0],
            0 => throw new SimpleOrmException(
                'QRY-006',
                $queryName,
                "'{$property}' is not a mapped property of {$map->entityName()}",
            ),
            default => throw new SimpleOrmException(
                'QRY-006',
                $queryName,
                "'{$property}' is ambiguous on {$map->entityName()}: matches "
                    . implode(', ', array_map(static fn (PropertyMap $p): string => $p->propertyName(), $matches)),
            ),
        };
    }
}
