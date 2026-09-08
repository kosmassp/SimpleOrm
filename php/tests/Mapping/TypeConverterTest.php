<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Mapping;

use DateTimeImmutable;
use DateTimeZone;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use SimpleOrm\Errors\SimpleOrmException;
use SimpleOrm\Mapping\TypeConverter;
use SimpleOrm\Mapping\TypeHandler;
use SimpleOrm\Mapping\TypeHandlerRegistry;
use SimpleOrm\Metadata\ColumnType;
use SimpleOrm\Tests\Sample\Models\TransactionStatus;
use SimpleOrm\Types\Decimal;
use stdClass;

/**
 * The fixed conversion table of §7.9 both directions, the VAL-020 UTC rule, and
 * MAP-030/031, plus handler precedence — mirrors dotnet's `TypeMappingTests`
 * (via {@see TypeConverter} directly, since this port has no `Db`/session yet).
 */
final class TypeConverterTest extends TestCase
{
    private TypeConverter $converter;

    protected function setUp(): void
    {
        $this->converter = new TypeConverter(new TypeHandlerRegistry());
    }

    #[Test]
    public function integers_round_trip(): void
    {
        self::assertSame(42, $this->converter->fromDatabase(42, ColumnType::Int64, 'int', 'ctx'));
        self::assertSame(42, $this->converter->fromDatabase('42', ColumnType::Int64, 'int', 'ctx'));
        self::assertSame(7, $this->converter->fromDatabase(7.0, ColumnType::Int32, 'int', 'ctx'));
        self::assertSame(42, $this->converter->toDatabase(42, 'ctx'));
    }

    #[Test]
    public function int32_overflow_is_map_031(): void
    {
        $exception = $this->assertThrowsSimpleOrm(
            fn () => $this->converter->fromDatabase(2147483648, ColumnType::Int32, 'int', 'ctx'),
        );
        self::assertSame('MAP-031', $exception->errorCode);
    }

    #[Test]
    public function decimal_round_trips_as_canonical_text(): void
    {
        $decimal = $this->converter->fromDatabase('1234.56', ColumnType::Decimal, Decimal::class, 'ctx');
        self::assertInstanceOf(Decimal::class, $decimal);
        self::assertTrue($decimal->equals(Decimal::of('1234.56')));
        self::assertSame('1234.56', $this->converter->toDatabase(Decimal::of('1234.56'), 'ctx'));
    }

    #[Test]
    public function decimal_accepts_integer_and_real_storage_too(): void
    {
        $fromInt = $this->converter->fromDatabase(5, ColumnType::Decimal, Decimal::class, 'ctx');
        self::assertTrue($fromInt->equals(Decimal::of('5')));

        $fromFloat = $this->converter->fromDatabase(2.5, ColumnType::Decimal, Decimal::class, 'ctx');
        self::assertTrue($fromFloat->equals(Decimal::of('2.5')));
    }

    #[Test]
    public function double_and_float_round_trip(): void
    {
        self::assertSame(2.25, $this->converter->fromDatabase(2.25, ColumnType::Double, 'float', 'ctx'));
        self::assertSame(1.5, $this->converter->fromDatabase('1.5', ColumnType::Float, 'float', 'ctx'));
        self::assertSame(2.25, $this->converter->toDatabase(2.25, 'ctx'));
    }

    #[Test]
    public function bool_round_trips_through_integer_zero_one(): void
    {
        self::assertTrue($this->converter->fromDatabase(1, ColumnType::Bool, 'bool', 'ctx'));
        self::assertFalse($this->converter->fromDatabase(0, ColumnType::Bool, 'bool', 'ctx'));
        self::assertSame(1, $this->converter->toDatabase(true, 'ctx'));
        self::assertSame(0, $this->converter->toDatabase(false, 'ctx'));
    }

    #[Test]
    public function string_round_trips_untouched(): void
    {
        $value = "O'Brien; drop table users; --";
        self::assertSame($value, $this->converter->fromDatabase($value, ColumnType::String, 'string', 'ctx'));
        self::assertSame($value, $this->converter->toDatabase($value, 'ctx'));
    }

    #[Test]
    public function guid_normalizes_to_lowercase_canonical_text(): void
    {
        $mixedCase = '6F9619FF-8B86-D011-B42D-00CF4FC964FF';
        self::assertSame(
            '6f9619ff-8b86-d011-b42d-00cf4fc964ff',
            $this->converter->fromDatabase($mixedCase, ColumnType::Guid, 'string', 'ctx'),
        );
    }

    #[Test]
    public function guid_accepts_a_sixteen_byte_binary_form(): void
    {
        $bytes = hex2bin('6f9619ff8b86d011b42d00cf4fc964ff');
        self::assertSame(
            '6f9619ff-8b86-d011-b42d-00cf4fc964ff',
            $this->converter->fromDatabase($bytes, ColumnType::Guid, 'string', 'ctx'),
        );
    }

    #[Test]
    public function bytes_round_trip_as_raw_binary(): void
    {
        $blob = "\x01\x02\x03";
        self::assertSame($blob, $this->converter->fromDatabase($blob, ColumnType::Bytes, 'string', 'ctx'));
        self::assertSame($blob, $this->converter->toDatabase($blob, 'ctx'));
    }

    #[Test]
    public function datetime_reads_utc_marker_and_normalizes_to_utc(): void
    {
        $value = $this->converter->fromDatabase('2026-08-28T10:00:00.0000000Z', ColumnType::DateTime, DateTimeImmutable::class, 'ctx');
        self::assertInstanceOf(DateTimeImmutable::class, $value);
        self::assertSame('UTC', $value->getTimezone()->getName());
        self::assertSame('2026-08-28T10:00:00', $value->format('Y-m-d\TH:i:s'));
    }

    #[Test]
    public function datetime_reads_an_explicit_offset_and_normalizes_to_utc(): void
    {
        $value = $this->converter->fromDatabase('2026-08-28T12:00:00+02:00', ColumnType::DateTime, DateTimeImmutable::class, 'ctx');
        self::assertSame('2026-08-28T10:00:00', $value->format('Y-m-d\TH:i:s'));
    }

    #[Test]
    public function datetime_without_a_marker_is_val_020_on_read(): void
    {
        $exception = $this->assertThrowsSimpleOrm(
            fn () => $this->converter->fromDatabase('2026-01-01T00:00:00', ColumnType::DateTime, DateTimeImmutable::class, 'ctx'),
        );
        self::assertSame('VAL-020', $exception->errorCode);
    }

    #[Test]
    public function datetime_writes_seven_fractional_digits_and_a_trailing_z(): void
    {
        $value = new DateTimeImmutable('2026-08-28T10:00:00.500000', new DateTimeZone('UTC'));
        self::assertSame('2026-08-28T10:00:00.5000000Z', $this->converter->toDatabase($value, 'ctx'));
    }

    #[Test]
    public function datetime_writes_convert_a_non_utc_zone_to_utc(): void
    {
        $value = new DateTimeImmutable('2026-08-28T12:00:00', new DateTimeZone('+02:00'));
        self::assertSame('2026-08-28T10:00:00.0000000Z', $this->converter->toDatabase($value, 'ctx'));
    }

    #[Test]
    public function date_and_time_tokens_parse_their_own_format(): void
    {
        $date = $this->converter->fromDatabase('2026-08-28', ColumnType::Date, DateTimeImmutable::class, 'ctx');
        self::assertSame('2026-08-28', $date->format('Y-m-d'));

        $time = $this->converter->fromDatabase('13:30:15', ColumnType::Time, DateTimeImmutable::class, 'ctx');
        self::assertSame('13:30:15', $time->format('H:i:s'));
    }

    #[Test]
    public function enum_text_matches_case_insensitively_by_name(): void
    {
        $value = $this->converter->fromDatabase('completed', ColumnType::EnumText, TransactionStatus::class, 'ctx');
        self::assertSame(TransactionStatus::Completed, $value);
        self::assertSame('Cancelled', $this->converter->toDatabase(TransactionStatus::Cancelled, 'ctx'));
    }

    #[Test]
    public function enum_text_unknown_name_is_map_031(): void
    {
        $exception = $this->assertThrowsSimpleOrm(
            fn () => $this->converter->fromDatabase('Nope', ColumnType::EnumText, TransactionStatus::class, 'ctx'),
        );
        self::assertSame('MAP-031', $exception->errorCode);
    }

    #[Test]
    public function enum_int_reads_by_ordinal_and_writes_the_ordinal(): void
    {
        $value = $this->converter->fromDatabase(2, ColumnType::EnumInt, TransactionStatus::class, 'ctx');
        self::assertSame(TransactionStatus::Cancelled, $value);
        self::assertSame(2, $this->converter->toDatabase(TransactionStatus::Cancelled, 'ctx', enumAsInt: true));
    }

    #[Test]
    public function enum_int_out_of_range_ordinal_is_map_031(): void
    {
        $exception = $this->assertThrowsSimpleOrm(
            fn () => $this->converter->fromDatabase(99, ColumnType::EnumInt, TransactionStatus::class, 'ctx'),
        );
        self::assertSame('MAP-031', $exception->errorCode);
    }

    #[Test]
    public function custom_column_type_with_no_handler_is_map_030(): void
    {
        $exception = $this->assertThrowsSimpleOrm(
            fn () => $this->converter->fromDatabase('x', ColumnType::Custom, null, 'ctx'),
        );
        self::assertSame('MAP-030', $exception->errorCode);
    }

    #[Test]
    public function an_unstorable_php_value_is_map_030_on_write(): void
    {
        $exception = $this->assertThrowsSimpleOrm(
            fn () => $this->converter->toDatabase(new stdClass(), 'ctx'),
        );
        self::assertSame('MAP-030', $exception->errorCode);
    }

    #[Test]
    public function a_registered_handler_wins_over_the_fixed_table(): void
    {
        // A value type the fixed table cannot store — an anonymous class, since
        // it exists only for this one test (CODING-STANDARD §7).
        $moneyClass = (new class (0.0) {
            public function __construct(public readonly float $amount)
            {
            }
        })::class;

        $handler = new class ($moneyClass) implements TypeHandler {
            /** @param class-string $moneyClass */
            public function __construct(private readonly string $moneyClass)
            {
            }

            public function type(): string
            {
                return $this->moneyClass;
            }

            public function format(mixed $value): mixed
            {
                return (string) $value->amount;
            }

            public function parse(mixed $databaseValue): mixed
            {
                $class = $this->moneyClass;

                return new $class((float) $databaseValue);
            }
        };

        $registry = new TypeHandlerRegistry();
        $registry->register($handler);
        $converter = new TypeConverter($registry);

        $money = $converter->fromDatabase('19.99', ColumnType::Custom, $moneyClass, 'ctx');
        self::assertInstanceOf($moneyClass, $money);
        self::assertSame(19.99, $money->amount);
        self::assertSame('19.99', $converter->toDatabase($money, 'ctx'));
        self::assertTrue($converter->hasHandler($moneyClass));
    }

    #[Test]
    public function null_passes_through_both_directions(): void
    {
        self::assertNull($this->converter->fromDatabase(null, ColumnType::String, 'string', 'ctx'));
        self::assertNull($this->converter->toDatabase(null, 'ctx'));
    }

    private function assertThrowsSimpleOrm(callable $callback): SimpleOrmException
    {
        try {
            $callback();
        } catch (SimpleOrmException $exception) {
            return $exception;
        }

        self::fail('expected a SimpleOrmException');
    }
}
