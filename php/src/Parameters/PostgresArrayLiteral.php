<?php

declare(strict_types=1);

namespace SimpleOrm\Parameters;

use DateTimeImmutable;
use InvalidArgumentException;
use SimpleOrm\Types\Iso8601;
use Stringable;

/**
 * Encodes a PHP list as a PostgreSQL array literal (spec/dialects.md "Array
 * parameters", CODING-STANDARD §10): the PDO realization of "a collection binds
 * as one typed array parameter". PDO has no array parameters, so the list is
 * sent as the literal text `{1,2,3}` under an untyped `PDO::PARAM_STR`
 * parameter and PostgreSQL infers the array type from the comparison
 * (`id = any(:ids)`).
 *
 * The grammar, one rule per element: `null` is the bare word `NULL`; an `int`
 * or a finite `float` is its decimal text, unquoted; a `bool` is `true`/`false`;
 * **every string (and `Stringable`) is double-quoted** with `\` and `"`
 * backslash-escaped — quoting always is valid for every element type (a quoted
 * `"12"` is a valid `integer[]` element) and removes the whitespace / comma /
 * brace / empty-string / `NULL`-lookalike cases from the encoder altogether. An
 * empty list is `{}`, which matches no row in `= any(…)`. Nested arrays are not
 * supported (a collection argument is one-dimensional, spec/session.md).
 */
final class PostgresArrayLiteral
{
    private function __construct()
    {
    }

    /** @param list<mixed> $items the already-converted database values */
    public static function encode(array $items): string
    {
        return '{' . implode(',', array_map(self::element(...), array_values($items))) . '}';
    }

    private static function element(mixed $item): string
    {
        return match (true) {
            $item === null => 'NULL',
            is_bool($item) => $item ? 'true' : 'false',
            is_int($item) => (string) $item,
            is_float($item) && is_finite($item) => self::floatText($item),
            $item instanceof DateTimeImmutable => '"' . Iso8601::format($item) . '"',   // a natively bound temporal (ADR-0025)
            is_string($item), $item instanceof Stringable => '"' . addcslashes((string) $item, '\\"') . '"',
            default => throw new InvalidArgumentException(
                'an array parameter element must be null, bool, int, float, or string; got ' . get_debug_type($item),
            ),
        };
    }

    /** Shortest round-trippable decimal text (json_encode honours serialize_precision = -1); PostgreSQL accepts the exponent form too. */
    private static function floatText(float $value): string
    {
        return (string) json_encode($value);
    }
}
