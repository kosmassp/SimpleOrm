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

/**
 * The shared ANSI rendering of a {@see SelectAst} (ADR-0020, spec/query-ast.md),
 * mirroring dotnet/src/SimpleOrm/AnsiSelectRenderer.cs minus joins, projection,
 * and subquery membership — Level 2 features not in this port (CLAUDE.md §12).
 * Explicit column list (never `*`), parameters bound in render order (`@c0…`:
 * WHERE first, then limit, then offset), dialect knobs consulted where SQL
 * differs. A dialect's `selectSql` normally delegates here.
 *
 * Null semantics are explicit and strict (ADR-0020): `eq(p, null)` renders
 * `is null` and `ne(p, null)` renders `is not null`; any other comparison with
 * null, or a null inside an IN list, is `QRY-007`. Degenerate composites render
 * their identity truth-values — an empty IN or OR is `1 = 0`, an empty AND is
 * `1 = 1`. Negative limit/offset is `QRY-008`.
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

        $columns = array_map(
            static fn (PropertyMap $p): string => $dialect->quoteIdentifier($p->columnName),
            $map->properties,
        );

        $sql = 'select ' . implode(', ', $columns) . ' from ' . $dialect->quoteIdentifier($relation);

        if ($select->where !== []) {
            $predicate = count($select->where) === 1 ? $select->where[0] : Criteria::and(...$select->where);
            $sql .= ' where ' . self::render($predicate, $map, $queryName, $dialect, $bindParameter);
        }

        if ($select->orderings !== []) {
            $sql .= ' order by ' . implode(', ', array_map(
                static fn (Ordering $o): string => self::column($map, $o->property, $queryName, $dialect)
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
    ): string {
        if ($criteria instanceof Comparison && $criteria->value === null) {
            return match ($criteria->operator) {
                '=' => self::column($map, $criteria->property, $queryName, $dialect) . ' is null',
                '<>' => self::column($map, $criteria->property, $queryName, $dialect) . ' is not null',
                default => throw new SimpleOrmException(
                    'QRY-007',
                    $queryName,
                    "'{$criteria->property} {$criteria->operator} null' has no meaning in SQL; use isNull()/isNotNull()",
                ),
            };
        }

        if ($criteria instanceof Comparison) {
            $property = self::resolve($map, $criteria->property, $queryName);

            return self::column($map, $criteria->property, $queryName, $dialect)
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

            return self::column($map, $criteria->property, $queryName, $dialect) . ' in ('
                . implode(', ', array_map(static fn (mixed $v): string => $bind($v, $property), $criteria->values))
                . ')';
        }

        if ($criteria instanceof NullCheck) {
            return self::column($map, $criteria->property, $queryName, $dialect)
                . ($criteria->negated ? ' is not null' : ' is null');
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
                    static fn (Criteria $child): string => self::render($child, $map, $queryName, $dialect, $bind),
                    $criteria->children,
                ),
            ) . ')';
        }

        if ($criteria instanceof Negation) {
            return 'not ' . self::render($criteria->inner, $map, $queryName, $dialect, $bind);
        }

        throw new InvalidArgumentException('unknown criteria node ' . $criteria::class);
    }

    private static function column(EntityMap $map, string $property, string $queryName, Dialect $dialect): string
    {
        return $dialect->quoteIdentifier(self::resolve($map, $property, $queryName)->columnName);
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
