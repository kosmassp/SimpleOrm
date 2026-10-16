<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Support;

use DateTimeImmutable;
use DateTimeZone;
use RuntimeException;
use SimpleOrm\Metadata\ColumnType;
use SimpleOrm\Metadata\PropertyMap;
use SimpleOrm\Types\Decimal;
use UnitEnum;

/**
 * The documented conformance value encoding (spec/mapping-rules.md) as the step-script runners
 * (`Conformance\CrudCasesTest`, `Conformance\TrackingCasesTest`) read and compare it: JSON
 * values decode into the PHP value of the property's type, PHP values reduce to a comparable
 * string. One implementation for both (CODING-STANDARD §8).
 */
final class ConformanceValues
{
    /** A JSON value decoded into a PHP value for the property's type. */
    public static function decode(mixed $value, PropertyMap $property): mixed
    {
        if ($value === null) {
            return null;
        }

        return match ($property->type) {
            ColumnType::Int16, ColumnType::Int32, ColumnType::Int64 => (int) $value,
            ColumnType::Decimal => Decimal::of((string) $value),
            ColumnType::Double, ColumnType::Float => (float) $value,
            ColumnType::Bool => (bool) $value,
            ColumnType::EnumText, ColumnType::EnumInt => self::enumCase($property->phpType, (string) $value),
            ColumnType::DateTime, ColumnType::DateTimeOffset, ColumnType::Date, ColumnType::Time
                => new DateTimeImmutable((string) $value),
            default => (string) $value,
        };
    }

    /** A PHP value reduced to a comparable string (or null). */
    public static function encodeForCompare(mixed $value): ?string
    {
        return match (true) {
            $value === null => null,
            is_bool($value) => $value ? 'true' : 'false',
            is_int($value) => (string) $value,
            is_float($value) => (string) $value,
            $value instanceof Decimal => (string) $value,
            $value instanceof DateTimeImmutable => $value->setTimezone(new DateTimeZone('UTC'))->format('Y-m-d\TH:i:s.u') . '0Z',
            $value instanceof UnitEnum => $value->name,
            default => (string) $value,
        };
    }

    /** A JSON expectation reduced to the same comparable string. */
    public static function expectedText(mixed $jsonValue): ?string
    {
        return match (true) {
            $jsonValue === null => null,
            is_bool($jsonValue) => $jsonValue ? 'true' : 'false',
            default => (string) $jsonValue,
        };
    }

    /** @param class-string|null $enumClass */
    private static function enumCase(?string $enumClass, string $value): UnitEnum
    {
        if ($enumClass === null || !enum_exists($enumClass)) {
            throw new RuntimeException("'{$enumClass}' is not an enum");
        }

        foreach ($enumClass::cases() as $case) {
            if (strcasecmp($case->name, $value) === 0) {
                return $case;
            }
        }

        throw new RuntimeException("'{$value}' is not a case of {$enumClass}");
    }
}
