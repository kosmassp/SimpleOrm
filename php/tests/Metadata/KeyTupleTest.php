<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Metadata;

use DateTimeImmutable;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use SimpleOrm\Metadata\KeyTuple;
use SimpleOrm\Types\Decimal;

/**
 * {@see KeyTuple} is the one value-wise identity/ordering implementation
 * relationship loading and {@see \SimpleOrm\Metadata\EntityMap::keysEqual()}
 * share (CODING-STANDARD §8) — spec/loading.md "Clarifications": numbers
 * compare numerically (`Decimal` included, never by text), strings ordinally,
 * temporals by instant, booleans false before true.
 */
final class KeyTupleTest extends TestCase
{
    #[Test]
    public function a_decimal_pair_compares_numerically_not_textually(): void
    {
        // The exact bug a text or float-rounded compare would get wrong: "10"
        // sorts before "2" as text, and a naive float cast can round two long
        // digit strings to the same double.
        $ten = new KeyTuple([Decimal::of('10')]);
        $two = new KeyTuple([Decimal::of('2')]);

        self::assertSame(1, $ten->compare($two), '10 must compare greater than 2');
        self::assertSame(-1, $two->compare($ten));
    }

    #[Test]
    public function decimal_equality_ignores_trailing_fraction_zeros(): void
    {
        $a = new KeyTuple([Decimal::of('10')]);
        $b = new KeyTuple([Decimal::of('10.0')]);

        self::assertTrue($a->equals($b));
        self::assertSame(0, $a->compare($b));
        self::assertSame($a->token(), $b->token(), 'the two representations must bucket together');
    }

    #[Test]
    public function a_negative_decimal_compares_less_than_a_positive_one(): void
    {
        $negative = new KeyTuple([Decimal::of('-5')]);
        $positive = new KeyTuple([Decimal::of('5')]);

        self::assertSame(-1, $negative->compare($positive));
    }

    #[Test]
    public function two_negative_decimals_compare_by_magnitude_in_reverse(): void
    {
        $moreNegative = new KeyTuple([Decimal::of('-10')]);
        $lessNegative = new KeyTuple([Decimal::of('-2')]);

        self::assertSame(-1, $moreNegative->compare($lessNegative), '-10 is less than -2');
    }

    #[Test]
    public function temporals_compare_by_instant_regardless_of_offset_representation(): void
    {
        $utc = new KeyTuple([new DateTimeImmutable('2026-08-28T09:30:00+00:00')]);
        $zulu = new KeyTuple([new DateTimeImmutable('2026-08-28T09:30:00Z')]);

        self::assertTrue($utc->equals($zulu));
        self::assertSame(0, $utc->compare($zulu));
        self::assertSame($utc->token(), $zulu->token());
    }

    #[Test]
    public function an_earlier_instant_compares_less_than_a_later_one(): void
    {
        $earlier = new KeyTuple([new DateTimeImmutable('2026-08-28T09:30:01Z')]);
        $later = new KeyTuple([new DateTimeImmutable('2026-08-28T09:30:02Z')]);

        self::assertSame(-1, $earlier->compare($later));
    }

    #[Test]
    public function false_compares_before_true(): void
    {
        self::assertSame(-1, (new KeyTuple([false]))->compare(new KeyTuple([true])));
        self::assertSame(1, (new KeyTuple([true]))->compare(new KeyTuple([false])));
    }

    #[Test]
    public function strings_compare_ordinally(): void
    {
        self::assertSame(-1, (new KeyTuple(['apple']))->compare(new KeyTuple(['banana'])));
    }

    #[Test]
    public function the_token_never_collapses_differently_typed_values_that_are_loosely_equal(): void
    {
        // Exactness (the class docblock's guarantee): int(1), the string "1",
        // and true are loosely `==` in PHP but must never bucket together.
        $tokens = array_map(
            static fn (mixed $v): string => (new KeyTuple([$v]))->token(),
            [1, '1', true],
        );

        self::assertSame(3, count(array_unique($tokens)), 'int(1), "1", and true must each get a distinct token');
    }

    #[Test]
    public function a_tuple_with_any_null_part_reports_it(): void
    {
        self::assertTrue((new KeyTuple([1, null]))->hasNullPart());
        self::assertFalse((new KeyTuple([1, 2]))->hasNullPart());
    }

    #[Test]
    public function composite_tuples_compare_position_by_position(): void
    {
        $first = new KeyTuple([1, 'b']);
        $second = new KeyTuple([1, 'a']);

        self::assertSame(1, $first->compare($second), 'the first part ties, so the second part decides');
    }
}
