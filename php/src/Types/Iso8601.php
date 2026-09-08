<?php

declare(strict_types=1);

namespace SimpleOrm\Types;

use DateTimeImmutable;
use DateTimeZone;

/**
 * Formats a `DateTimeImmutable` as ISO-8601 UTC with C#'s `"o"` round-trip
 * precision (§7.9): seven fractional digits — PHP's `DateTimeImmutable`
 * carries at most six, padded here with one trailing zero — and a literal
 * `Z`. The one place this shape is produced: `TypeConverter`'s temporal
 * binding, `JsonTypeHandler`'s nested JSON values, `MigrationRunner`'s
 * `applied_at`, and `SchemaSnapshot`'s `generatedAt` all call this rather than
 * reimplementing it (CODING-STANDARD §8).
 */
final class Iso8601
{
    private function __construct()
    {
    }

    /** Converts to UTC first when `$value` carries a different offset. */
    public static function format(DateTimeImmutable $value): string
    {
        $utc = $value->getOffset() === 0 ? $value : $value->setTimezone(new DateTimeZone('UTC'));

        return $utc->format('Y-m-d\TH:i:s.u') . '0Z';
    }
}
