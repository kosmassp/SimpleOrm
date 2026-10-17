<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Parameters;

use InvalidArgumentException;
use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use SimpleOrm\Parameters\PostgresArrayLiteral;

/**
 * The PostgreSQL array-literal grammar of the PDO realization of array
 * parameters (spec/dialects.md "Array parameters", CODING-STANDARD §10): pure
 * encoding, no database — {@see \SimpleOrm\Tests\Dialect\PostgresLiveTest}
 * proves the server reads it back.
 */
final class PostgresArrayLiteralTest extends TestCase
{
    /** @return iterable<string, array{list<mixed>, string}> */
    public static function literals(): iterable
    {
        yield 'empty' => [[], '{}'];
        yield 'ints' => [[1, 3, -7], '{1,3,-7}'];
        yield 'floats' => [[1.5, 100.0, -0.25], '{1.5,100,-0.25}'];
        yield 'booleans' => [[true, false], '{true,false}'];
        yield 'nulls are bare NULL' => [[1, null, 3], '{1,NULL,3}'];
        yield 'strings are always quoted' => [['Ada', 'Grace'], '{"Ada","Grace"}'];
        yield 'a numeric-looking string stays quoted' => [['12.50'], '{"12.50"}'];
        yield 'a string spelling NULL is a string' => [['NULL', 'null'], '{"NULL","null"}'];
        yield 'the empty string' => [[''], '{""}'];
        yield 'commas and braces' => [['a,b', '{x}'], '{"a,b","{x}"}'];
        yield 'whitespace' => [[' lead', 'in ner', "tab\t"], "{\" lead\",\"in ner\",\"tab\t\"}"];
        yield 'double quotes are backslash-escaped' => [['say "hi"'], '{"say \\"hi\\""}'];
        yield 'backslashes are backslash-escaped' => [['a\\b'], '{"a\\\\b"}'];
        yield 'quote after backslash' => [['\\"'], '{"\\\\\\""}'];
        yield 'multibyte text passes through' => [['héllo ☃'], '{"héllo ☃"}'];
        yield 'iso timestamps' => [['2026-08-28T09:30:00.0000000Z'], '{"2026-08-28T09:30:00.0000000Z"}'];
        yield 'keys are ignored, order is kept' => [[2 => 'b', 0 => 'a'], '{"b","a"}'];
    }

    /** @param list<mixed> $items */
    #[Test]
    #[DataProvider('literals')]
    public function elements_encode_per_the_literal_grammar(array $items, string $expected): void
    {
        self::assertSame($expected, PostgresArrayLiteral::encode($items));
    }

    #[Test]
    public function a_stringable_element_encodes_as_its_string(): void
    {
        $element = new class implements \Stringable {
            public function __toString(): string
            {
                return 'x,y';
            }
        };

        self::assertSame('{"x,y"}', PostgresArrayLiteral::encode([$element]));
    }

    #[Test]
    public function an_element_that_is_no_scalar_refuses(): void
    {
        $this->expectException(InvalidArgumentException::class);
        $this->expectExceptionMessage('array');

        PostgresArrayLiteral::encode([[1, 2]]);
    }

    #[Test]
    public function a_non_finite_float_refuses(): void
    {
        $this->expectException(InvalidArgumentException::class);

        PostgresArrayLiteral::encode([INF]);
    }
}
