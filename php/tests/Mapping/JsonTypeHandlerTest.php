<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Mapping;

use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use SimpleOrm\Mapping\JsonTypeHandler;
use SimpleOrm\Types\Decimal;

/**
 * The §7.10 JSON-column handler: a flat DTO round-trips through a JSON object
 * (single-item form) and a `json_group_array` of objects (list form), snake_case
 * keys matched case-insensitively — mirrors dotnet's `JsonNestingTests` as far as
 * a standalone handler (no session, no database) can.
 */
final class JsonTypeHandlerTest extends TestCase
{
    #[Test]
    public function a_single_object_round_trips_through_its_constructor(): void
    {
        $class = self::detailLineClass();
        $handler = new JsonTypeHandler($class);

        $json = '{"description":"cake","quantity":2,"unit_price":"5.00"}';
        $line = $handler->parse($json);

        self::assertInstanceOf($class, $line);
        self::assertSame('cake', $line->description);
        self::assertSame(2, $line->quantity);
        self::assertTrue($line->unitPrice->equals(Decimal::of('5.00')));

        $formatted = $handler->format($line);
        self::assertSame(['description' => 'cake', 'quantity' => 2, 'unit_price' => '5.00'], json_decode($formatted, true));
    }

    #[Test]
    public function a_list_of_objects_round_trips_via_json_group_array(): void
    {
        $class = self::detailLineClass();
        $handler = new JsonTypeHandler($class, list: true);

        $json = '[{"description":"cake","quantity":2,"unit_price":"5.00"},'
            . '{"description":"candle","quantity":1,"unit_price":"5.25"}]';
        $lines = $handler->parse($json);

        self::assertCount(2, $lines);
        self::assertSame('cake', $lines[0]->description);
        self::assertSame('candle', $lines[1]->description);

        self::assertSame('list<' . $class . '>', $handler->type());
    }

    #[Test]
    public function an_empty_json_array_parses_to_an_empty_list_not_null(): void
    {
        $handler = new JsonTypeHandler(self::detailLineClass(), list: true);

        self::assertSame([], $handler->parse('[]'));
    }

    #[Test]
    public function property_based_hydration_is_used_when_there_is_no_meaningful_constructor(): void
    {
        $bag = new class {
            public string $name = '';
            public int $count = 0;
        };
        $handler = new JsonTypeHandler($bag::class);

        $hydrated = $handler->parse('{"name":"Ada","count":3}');

        self::assertSame('Ada', $hydrated->name);
        self::assertSame(3, $hydrated->count);
    }

    /** @return class-string */
    private static function detailLineClass(): string
    {
        return (new class ('', 0, Decimal::of('0')) {
            public function __construct(
                public readonly string $description,
                public readonly int $quantity,
                public readonly Decimal $unitPrice,
            ) {
            }
        })::class;
    }
}
