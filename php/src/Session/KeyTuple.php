<?php

declare(strict_types=1);

namespace SimpleOrm\Session;

use DateTimeInterface;
use SimpleOrm\Types\Decimal;
use UnitEnum;

/**
 * A key or foreign-key tuple compared by **value**, never by string rendering
 * (§7.4, ADR-0021 add.1: lossy stringification silently loaded the wrong
 * entity for date and blob keys). Used by {@see DbLoading} to correlate owners
 * with related rows: numbers compare numerically (`Decimal` included), strings
 * ordinally, temporals by instant, enums by case, booleans false before true
 * (spec/loading.md "Clarifications" — value-wise comparison). {@see token()}
 * is a type-tagged, collision-free encoding used only to bucket tuples in a
 * PHP associative array; it is exact (never lossy) for every type this port
 * maps, so grouping by it is equivalent to grouping by {@see equals()}.
 */
final readonly class KeyTuple
{
    /** @param list<mixed> $values one entry per key part, in key order */
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
}
