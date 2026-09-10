<?php

declare(strict_types=1);

namespace SimpleOrm\Metadata;

use DateTimeInterface;
use SimpleOrm\Types\Decimal;
use UnitEnum;

/**
 * A key or foreign-key tuple compared by **value**, never by string rendering
 * (§7.4, ADR-0021 add.1: lossy stringification silently loaded the wrong
 * entity for date and blob keys). Entity identity is a Level 1 metadata
 * concept (§7.1 point 4); this is the **one** implementation of it — used by
 * {@see EntityMap::keysEqual()} and by every relationship-loading path
 * (explicit/batch `DbLoading`, join-mode `DbEagerJoin`, CODING-STANDARD §8) —
 * so correlation and ordering agree everywhere: numbers compare numerically
 * (`Decimal` included, digit-by-digit, never through a float), strings
 * ordinally, temporals by instant, enums by case, booleans false before true
 * (spec/loading.md "Clarifications"). {@see token()} is a type-tagged,
 * collision-free encoding used only to bucket tuples in a PHP associative
 * array; it is exact (never lossy) for every type this port maps, so grouping
 * by it is equivalent to grouping by {@see equals()}.
 *
 * @internal an implementation detail of entity identity and relationship
 *     loading, not part of the public API
 */
final readonly class KeyTuple
{
    /** @param list<mixed> $values one entry per key part, in key order — already converted PHP values, never raw database cells */
    public function __construct(public array $values)
    {
    }

    /** An owner/FK part that is null excludes the tuple from querying (the null-FK / null-key symmetric rule). */
    public function hasNullPart(): bool
    {
        foreach ($this->values as $value) {
            if ($value === null) {
                return true;
            }
        }

        return false;
    }

    public function equals(self $other): bool
    {
        if (count($this->values) !== count($other->values)) {
            return false;
        }

        foreach ($this->values as $i => $value) {
            if (!self::valueEquals($value, $other->values[$i])) {
                return false;
            }
        }

        return true;
    }

    /** Value-wise ordering, position by position (spec/loading.md: "ordered by target key … compared value-wise"). */
    public function compare(self $other): int
    {
        foreach ($this->values as $i => $value) {
            $cmp = self::valueCompare($value, $other->values[$i]);
            if ($cmp !== 0) {
                return $cmp;
            }
        }

        return 0;
    }

    /** The grouping key for an associative array — see the class docblock. */
    public function token(): string
    {
        return implode("\x1f", array_map(self::valueToken(...), $this->values));
    }

    public static function valueEquals(mixed $a, mixed $b): bool
    {
        return match (true) {
            $a === null || $b === null => $a === $b,
            $a instanceof Decimal && $b instanceof Decimal => $a->equals($b),
            $a instanceof DateTimeInterface && $b instanceof DateTimeInterface => self::sameInstant($a, $b),
            $a instanceof UnitEnum && $b instanceof UnitEnum => $a === $b,
            is_bool($a) !== is_bool($b) => false,
            is_int($a) && is_int($b) => $a === $b,
            (is_int($a) || is_float($a)) && (is_int($b) || is_float($b)) => (float) $a === (float) $b,
            is_string($a) && is_string($b) => $a === $b,
            default => $a === $b,
        };
    }

    /**
     * Numbers numerically — `Decimal` compared digit-by-digit (never rounded
     * through a float, so no magnitude loses precision), strings ordinally,
     * temporals by instant, booleans false before true (spec/loading.md
     * "Clarifications").
     */
    public static function valueCompare(mixed $a, mixed $b): int
    {
        return match (true) {
            $a instanceof Decimal && $b instanceof Decimal => self::compareDecimal($a, $b),
            $a instanceof DateTimeInterface && $b instanceof DateTimeInterface => $a <=> $b,
            is_bool($a) && is_bool($b) => ($a ? 1 : 0) <=> ($b ? 1 : 0),
            (is_int($a) || is_float($a)) && (is_int($b) || is_float($b)) => $a <=> $b,
            is_string($a) && is_string($b) => $a <=> $b,
            $a instanceof UnitEnum && $b instanceof UnitEnum => $a->name <=> $b->name,
            default => (string) $a <=> (string) $b,
        };
    }

    private static function sameInstant(DateTimeInterface $a, DateTimeInterface $b): bool
    {
        return $a->getTimestamp() === $b->getTimestamp() && $a->format('u') === $b->format('u');
    }

    private static function valueToken(mixed $value): string
    {
        return match (true) {
            $value === null => 'null',
            $value instanceof Decimal => 'dec:' . self::normalizedDecimal($value),
            $value instanceof DateTimeInterface => 'dt:' . $value->getTimestamp() . '.' . $value->format('u'),
            $value instanceof UnitEnum => 'enum:' . $value::class . ':' . $value->name,
            is_bool($value) => 'bool:' . ($value ? '1' : '0'),
            is_int($value) => 'int:' . $value,
            is_float($value) => 'float:' . sprintf('%.17G', $value),
            is_string($value) => 'str:' . $value,
            default => 'obj:' . spl_object_id($value),
        };
    }

    /** `Decimal::equals()`'s own normalization (trailing fraction zeros ignored), replicated: `Decimal` exposes no public accessor for it. */
    private static function normalizedDecimal(Decimal $value): string
    {
        $text = (string) $value;

        return str_contains($text, '.') ? rtrim(rtrim($text, '0'), '.') : $text;
    }

    /**
     * Exact digit-string comparison — never a float cast, so no magnitude or
     * fractional precision is lost (the bug a float-based compare would carry:
     * `(float) "10" <=> (float) "2"` is fine, but two long digit strings can
     * round to the same double).
     */
    private static function compareDecimal(Decimal $a, Decimal $b): int
    {
        $left = self::normalizedDecimal($a);
        $right = self::normalizedDecimal($b);
        if ($left === $right) {
            return 0;
        }

        $leftNegative = str_starts_with($left, '-');
        $rightNegative = str_starts_with($right, '-');
        if ($leftNegative !== $rightNegative) {
            return $leftNegative ? -1 : 1;
        }

        $magnitude = self::compareMagnitude(ltrim($left, '-'), ltrim($right, '-'));

        return $leftNegative ? -$magnitude : $magnitude;
    }

    private static function compareMagnitude(string $a, string $b): int
    {
        [$aInt, $aFrac] = array_pad(explode('.', $a, 2), 2, '');
        [$bInt, $bFrac] = array_pad(explode('.', $b, 2), 2, '');
        $aInt = ltrim($aInt, '0');
        $bInt = ltrim($bInt, '0');
        if (strlen($aInt) !== strlen($bInt)) {
            return strlen($aInt) <=> strlen($bInt);
        }

        if ($aInt !== $bInt) {
            return $aInt <=> $bInt;
        }

        $width = max(strlen($aFrac), strlen($bFrac));

        return str_pad($aFrac, $width, '0') <=> str_pad($bFrac, $width, '0');
    }
}
