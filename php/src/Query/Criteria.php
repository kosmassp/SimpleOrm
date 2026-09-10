<?php

declare(strict_types=1);

namespace SimpleOrm\Query;

use InvalidArgumentException;
use SimpleOrm\Query\Nodes\Comparison;
use SimpleOrm\Query\Nodes\Composite;
use SimpleOrm\Query\Nodes\InList;
use SimpleOrm\Query\Nodes\Negation;
use SimpleOrm\Query\Nodes\NullCheck;
use SimpleOrm\Query\Nodes\SubqueryMembership;

/**
 * The criteria AST's node root and its factories (ADR-0012/0020,
 * spec/query-ast.md): criteria name **properties**, never columns, and carry
 * values — the dialect resolves and binds. Null semantics are strict: `eq`/`ne`
 * with null render `IS [NOT] NULL`; any other null comparison, or a null inside
 * an IN list, is `QRY-007` at render time. Nodes are immutable data.
 */
abstract class Criteria
{
    public static function eq(string $property, mixed $value): self
    {
        return new Comparison($property, '=', $value);
    }

    public static function ne(string $property, mixed $value): self
    {
        return new Comparison($property, '<>', $value);
    }

    public static function gt(string $property, mixed $value): self
    {
        return new Comparison($property, '>', $value);
    }

    public static function ge(string $property, mixed $value): self
    {
        return new Comparison($property, '>=', $value);
    }

    public static function lt(string $property, mixed $value): self
    {
        return new Comparison($property, '<', $value);
    }

    public static function le(string $property, mixed $value): self
    {
        return new Comparison($property, '<=', $value);
    }

    /** LIKE with the caller's wildcards verbatim. */
    public static function like(string $property, ?string $pattern): self
    {
        return new Comparison($property, 'like', $pattern);
    }

    /**
     * Membership in a value list; an empty list matches no rows (renders `1 = 0`).
     *
     * @param list<mixed> $values
     */
    public static function in(string $property, array $values): self
    {
        return new InList($property, array_values($values));
    }

    /**
     * Subquery membership (ADR-0022 add.1, SubSelect eager loading): the listed
     * properties of the root, as a row value when more than one, `in (select …)`
     * over `$subquery`, whose projection must have the same arity. Part of the
     * Level 2 AST (spec/query-ast.md); the criteria chain does not expose it —
     * loading builds it, and the conformance runner replays it.
     *
     * @param list<string> $properties
     */
    public static function inSelect(array $properties, SelectAst $subquery): self
    {
        return new SubqueryMembership(array_values($properties), $subquery);
    }

    public static function isNull(string $property): self
    {
        return new NullCheck($property, negated: false);
    }

    public static function isNotNull(string $property): self
    {
        return new NullCheck($property, negated: true);
    }

    /** AND of the given criteria; empty renders the identity `1 = 1` (ADR-0020 add.1). */
    public static function and(self ...$criteria): self
    {
        return new Composite('and', array_values($criteria));
    }

    /** OR of the given criteria; empty renders the identity `1 = 0` (ADR-0020 add.1). */
    public static function or(self ...$criteria): self
    {
        return new Composite('or', array_values($criteria));
    }

    public static function not(self $criteria): self
    {
        return new Negation($criteria);
    }

    /** Guards the value-list overload trap: a lone string is a one-element list, never its characters. */
    public static function inOne(string $property, string $value): self
    {
        if ($value === '') {
            throw new InvalidArgumentException("the IN value for '{$property}' is empty; pass values or an empty list");
        }

        return new InList($property, [$value]);
    }
}
