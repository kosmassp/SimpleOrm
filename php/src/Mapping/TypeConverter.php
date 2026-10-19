<?php

declare(strict_types=1);

namespace SimpleOrm\Mapping;

use DateTimeImmutable;
use DateTimeZone;
use Exception;
use InvalidArgumentException;
use SimpleOrm\Dialect\BinaryText;
use SimpleOrm\Dialect\TemporalText;
use SimpleOrm\Errors\SimpleOrmException;
use SimpleOrm\Metadata\ColumnType;
use SimpleOrm\Parameters\BinaryParameter;
use SimpleOrm\Types\Decimal;
use SimpleOrm\Types\Iso8601;
use UnitEnum;

/**
 * The fixed conversion table of §7.9 plus the handler registry — the only two
 * ways a value crosses the database boundary; no reflection-based guessing.
 * Mirrors dotnet/src/SimpleOrm/TypeConverter.cs. Handlers win over the fixed
 * table. Failures carry codes: `MAP-030` (no rule), `MAP-031` (rule failed for
 * the value), `VAL-020` (the UTC rule: a stored datetime must carry a UTC/offset
 * marker; CODING-STANDARD §10 — PHP's `DateTimeImmutable` has no "unspecified"
 * zone, so the write-side refusal never applies, only the read-side one).
 */
final class TypeConverter
{
    public function __construct(
        private readonly TypeHandlerRegistry $handlers,
        private readonly bool $bindsTemporalsNatively = false,
        private readonly TemporalText $temporalText = TemporalText::Iso8601,
        private readonly BinaryText $binaryText = BinaryText::Raw,
    ) {
    }

    /** @param class-string $phpType */
    public function hasHandler(string $phpType): bool
    {
        return $this->handlers->has($phpType);
    }

    /**
     * Database value → PHP value. Null passes through unchanged — enforcing
     * "null into a non-nullable property" is the mapping pipeline's job
     * (PHP's own typed-property assignment already refuses it there).
     */
    public function fromDatabase(mixed $value, ColumnType $type, ?string $phpType, string $context): mixed
    {
        if ($value === null) {
            return null;
        }

        if ($phpType !== null && $this->handlers->tryParse($phpType, $value, $parsed)) {
            return $parsed;
        }

        return match ($type) {
            ColumnType::Int16, ColumnType::Int32, ColumnType::Int64 => $this->toInt($value, $type, $context),
            ColumnType::Decimal => $this->toDecimal($value, $context),
            ColumnType::Double, ColumnType::Float => $this->toFloat($value, $context),
            ColumnType::Bool => $this->toBool($value),
            ColumnType::String => (string) $value,
            ColumnType::Guid => $this->toGuid($value, $context),
            ColumnType::Bytes => $this->toBytes($value),
            ColumnType::DateTime => $this->toUtcDateTime($value, $context),
            ColumnType::DateTimeOffset => $this->toOffsetDateTime($value, $context),
            ColumnType::Date => $this->toDate($value, $context),
            ColumnType::Time => $this->toTime($value, $context),
            ColumnType::EnumText => $this->toEnumByName($value, $phpType, $context),
            ColumnType::EnumInt => $this->toEnumByOrdinal($value, $phpType, $context),
            ColumnType::Custom => throw self::noRule($value, $phpType ?? 'custom', $context),
        };
    }

    /**
     * PHP value → database value (§7.9 storage conventions). Handlers win;
     * unknown types are `MAP-030`. `$enumAsInt` reflects the mapped column's
     * `#[EnumAsInt]` flag; `$type` is the mapped column's neutral token where the
     * caller has one (a `PropertyMap`), which decides a `DateTimeImmutable`'s
     * storage form — the reference tells `DateOnly`/`TimeOnly`/`DateTimeOffset`
     * apart by type, PHP only by the declared token (CODING-STANDARD §10). A
     * value with no token (a registered query's argument) follows the datetime rule.
     */
    public function toDatabase(mixed $value, string $context, bool $enumAsInt = false, ?ColumnType $type = null): mixed
    {
        if ($value === null) {
            return null;
        }

        if ($this->handlers->tryFormat($value, $formatted)) {
            return $formatted;
        }

        if ($value instanceof UnitEnum) {
            if ($enumAsInt) {
                return array_search($value, $value::cases(), strict: true);
            }

            return $value->name;
        }

        if ($value instanceof DateTimeImmutable) {
            return $this->formatTemporal($value, $type);
        }

        if ($value instanceof Decimal) {
            return (string) $value;
        }

        if (is_bool($value)) {
            return $value ? 1 : 0;
        }

        if (is_int($value) || is_float($value) || is_string($value)) {
            return $value;
        }

        throw new SimpleOrmException(
            'MAP-030',
            $context,
            'no conversion or handler stores a ' . get_debug_type($value) . '; register a TypeHandler (§7.9)',
        );
    }

    /**
     * {@see toDatabase()} for a value about to be bound as a statement parameter: the same
     * conversion, then a string whose token is `bytes` is wrapped as a {@see BinaryParameter}
     * so `PdoBinder` binds it as `PDO::PARAM_LOB` (ADR-0040 add.1). Snapshots and comparisons
     * keep calling {@see toDatabase()} — the wrapper exists only on the way to PDO.
     */
    public function toParameter(mixed $value, string $context, bool $enumAsInt = false, ?ColumnType $type = null): mixed
    {
        $bound = $this->toDatabase($value, $context, $enumAsInt, $type);

        return $type === ColumnType::Bytes && is_string($bound) ? new BinaryParameter($bound) : $bound;
    }

    /**
     * The storage form per token, each the C# `"o"`/`"O"` shape (seven fractional digits):
     *
     * - `datetime` (and no token): ISO-8601 UTC with a trailing `Z`; a non-UTC zone converts
     *   first — PHP's `DateTimeImmutable` always carries a real zone, so the write-side
     *   `Kind == Unspecified` refusal (VAL-020) has no PHP counterpart. Under
     *   `bindsTemporalsNatively` the UTC instant itself is handed on (`PdoBinder` sends its text).
     * - `datetimeoffset`: the value's own offset kept (`+02:00`, `+00:00` for UTC — never `Z`),
     *   as the reference keeps a `DateTimeOffset`; natively the UTC instant, as the reference's
     *   `ToUniversalTime()`.
     * - `date`: the value's **own calendar date** `Y-m-d` — converting the instant would shift
     *   the date of a value held in another zone, and a `date` carries no zone (the Go port's
     *   rule under native binding too).
     * - `time`: the time of day `H:i:s.u0`. Both calendar forms are text whatever the binding
     *   flag says: they carry no kind to refuse, and the database infers the parameter's type
     *   from the comparison (spec/dialects.md "Temporals").
     */
    private function formatTemporal(DateTimeImmutable $value, ?ColumnType $type): DateTimeImmutable|string
    {
        return match ($type) {
            ColumnType::Date => $value->format('Y-m-d'),
            ColumnType::Time => $value->format('H:i:s.u') . '0',
            ColumnType::DateTimeOffset => $this->bindsTemporalsNatively
                ? self::toUtc($value)
                : ($this->temporalText === TemporalText::Odbc
                    ? $value->format('Y-m-d H:i:s.u') . '0 ' . $value->format('P')
                    : $value->format('Y-m-d\TH:i:s.u') . '0' . $value->format('P')),
            default => $this->bindsTemporalsNatively
                ? self::toUtc($value)
                : ($this->temporalText === TemporalText::Odbc
                    ? self::toUtc($value)->format('Y-m-d H:i:s.u') . '0 +00:00'   // the ODBC parser's form (TemporalText)
                    : Iso8601::format($value)),
        };
    }

    private static function toUtc(DateTimeImmutable $value): DateTimeImmutable
    {
        return $value->getOffset() === 0 ? $value : $value->setTimezone(new DateTimeZone('UTC'));
    }

    /**
     * A `bytes` column as read: `pdo_pgsql` hands a stream, `pdo_sqlite` the bytes, and a driver
     * declaring {@see BinaryText::Hex} (`pdo_odbc`) the column's hexadecimal text, decoded here.
     */
    private function toBytes(mixed $value): string
    {
        if (is_resource($value)) {
            return (string) stream_get_contents($value);
        }

        $text = (string) $value;
        if ($this->binaryText === BinaryText::Hex && $text !== '' && strlen($text) % 2 === 0 && ctype_xdigit($text)) {
            return (string) hex2bin($text);
        }

        return $text;
    }

    private function toInt(mixed $value, ColumnType $type, string $context): int
    {
        $result = match (true) {
            is_int($value) => $value,
            is_float($value) && floor($value) === $value => (int) $value,
            is_string($value) && preg_match('/^-?\d+$/', trim($value)) === 1 => (int) trim($value),
            default => throw self::conversionFailed($value, $type->value, $context),
        };

        $bounds = match ($type) {
            ColumnType::Int16 => [-32768, 32767],
            ColumnType::Int32 => [-2147483648, 2147483647],
            default => null,
        };
        if ($bounds !== null && ($result < $bounds[0] || $result > $bounds[1])) {
            throw self::conversionFailed($value, $type->value, $context);
        }

        return $result;
    }

    private function toDecimal(mixed $value, string $context): Decimal
    {
        try {
            return match (true) {
                is_string($value) => Decimal::of($value),
                is_int($value) => Decimal::of($value),
                is_float($value) => Decimal::of(self::floatToDecimalString($value)),
                default => throw self::conversionFailed($value, 'decimal', $context),
            };
        } catch (InvalidArgumentException) {
            throw self::conversionFailed($value, 'decimal', $context);
        }
    }

    private static function floatToDecimalString(float $value): string
    {
        $text = rtrim(rtrim(sprintf('%.10f', $value), '0'), '.');

        return $text === '' || $text === '-' ? '0' : $text;
    }

    private function toFloat(mixed $value, string $context): float
    {
        if (is_int($value) || is_float($value)) {
            return (float) $value;
        }

        if (is_string($value) && is_numeric($value)) {
            return (float) $value;
        }

        throw self::conversionFailed($value, 'double', $context);
    }

    private function toBool(mixed $value): bool
    {
        return match (true) {
            is_bool($value) => $value,
            is_int($value) => $value !== 0,
            is_string($value) => $value === '1' || strcasecmp($value, 'true') === 0,
            default => (bool) $value,
        };
    }

    private function toGuid(mixed $value, string $context): string
    {
        if (is_string($value)) {
            $trimmed = trim($value);
            if (preg_match('/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i', $trimmed) === 1) {
                return strtolower($trimmed);
            }

            if (strlen($trimmed) === 16) {
                $hex = bin2hex($trimmed);

                return sprintf(
                    '%s-%s-%s-%s-%s',
                    substr($hex, 0, 8),
                    substr($hex, 8, 4),
                    substr($hex, 12, 4),
                    substr($hex, 16, 4),
                    substr($hex, 20, 12),
                );
            }
        }

        throw self::conversionFailed($value, 'guid', $context);
    }

    /** The §7.9 date rule: a stored datetime must carry a UTC marker ('Z') or an explicit offset. */
    private function toUtcDateTime(mixed $value, string $context): DateTimeImmutable
    {
        $trimmed = self::requireMarkedTemporal($value, $context);
        try {
            $parsed = new DateTimeImmutable($trimmed);
        } catch (Exception) {
            throw self::conversionFailed($value, 'datetime', $context);
        }

        return $parsed->setTimezone(new DateTimeZone('UTC'));
    }

    /** Like `datetime`, but the original offset is kept rather than normalized to UTC. */
    private function toOffsetDateTime(mixed $value, string $context): DateTimeImmutable
    {
        $trimmed = self::requireMarkedTemporal($value, $context);
        try {
            return new DateTimeImmutable($trimmed);
        } catch (Exception) {
            throw self::conversionFailed($value, 'datetimeoffset', $context);
        }
    }

    private function requireMarkedTemporal(mixed $value, string $context): string
    {
        if (!is_string($value)) {
            throw self::conversionFailed($value, 'datetime', $context);
        }

        // The marker is a trailing Z or an explicit offset — ISO-8601 `+hh:mm`, and the
        // forms the provider drivers spell (spec/dialects.md "Temporals"): PostgreSQL's
        // `+07` without minutes, SQL Server's ` +00:00` after a space, `+0700`.
        $trimmed = rtrim($value);
        $hasMarker = str_ends_with(strtoupper($trimmed), 'Z') || preg_match('/ ?[+-]\d{2}(:?\d{2})?$/', $trimmed) === 1;
        if (!$hasMarker) {
            throw new SimpleOrmException(
                'VAL-020',
                $context,
                "stored datetime '{$value}' has no UTC/offset marker; the convention is ISO-8601 UTC with a trailing Z",
            );
        }

        return $trimmed;
    }

    private function toDate(mixed $value, string $context): DateTimeImmutable
    {
        if (!is_string($value)) {
            throw self::conversionFailed($value, 'date', $context);
        }

        $parsed = DateTimeImmutable::createFromFormat('!Y-m-d', trim($value));
        if ($parsed === false) {
            throw self::conversionFailed($value, 'date', $context);
        }

        return $parsed;
    }

    /** `HH:mm:ss` with optional fractional seconds — SQLite stores none, SQL Server and PostgreSQL report up to seven digits. */
    private function toTime(mixed $value, string $context): DateTimeImmutable
    {
        if (!is_string($value)) {
            throw self::conversionFailed($value, 'time', $context);
        }

        $text = trim($value);
        $dot = strpos($text, '.');
        if ($dot !== false) {
            $text = substr($text, 0, $dot + 1) . str_pad(substr(substr($text, $dot + 1), 0, 6), 6, '0');   // PHP's `u` takes exactly six digits
        }

        $parsed = DateTimeImmutable::createFromFormat($dot === false ? '!H:i:s' : '!H:i:s.u', $text);
        if ($parsed === false) {
            throw self::conversionFailed($value, 'time', $context);
        }

        return $parsed;
    }

    /** @param class-string|null $phpType */
    private function toEnumByName(mixed $value, ?string $phpType, string $context): UnitEnum
    {
        if ($phpType === null || !enum_exists($phpType)) {
            throw self::noRule($value, $phpType ?? 'enum', $context);
        }

        if (!is_string($value)) {
            throw self::conversionFailed($value, $phpType, $context);
        }

        foreach ($phpType::cases() as $case) {
            if (strcasecmp($case->name, $value) === 0) {
                return $case;
            }
        }

        throw new SimpleOrmException('MAP-031', $context, "'{$value}' is not a case of {$phpType}");
    }

    /** @param class-string|null $phpType */
    private function toEnumByOrdinal(mixed $value, ?string $phpType, string $context): UnitEnum
    {
        if ($phpType === null || !enum_exists($phpType)) {
            throw self::noRule($value, $phpType ?? 'enum', $context);
        }

        $cases = $phpType::cases();
        if (is_string($value) && !is_numeric($value)) {
            foreach ($cases as $case) {
                if (strcasecmp($case->name, $value) === 0) {
                    return $case;
                }
            }

            throw new SimpleOrmException('MAP-031', $context, "'{$value}' is not a case of {$phpType}");
        }

        $ordinal = is_int($value) ? $value : (is_numeric($value) ? (int) $value : null);
        if ($ordinal === null || $ordinal < 0 || $ordinal >= count($cases)) {
            throw new SimpleOrmException(
                'MAP-031',
                $context,
                'ordinal ' . (string) ($ordinal ?? $value) . " is out of range for {$phpType}",
            );
        }

        return $cases[$ordinal];
    }

    private static function conversionFailed(mixed $value, string $targetLabel, string $context): SimpleOrmException
    {
        return new SimpleOrmException(
            'MAP-031',
            $context,
            "cannot convert '" . (is_scalar($value) ? (string) $value : get_debug_type($value)) . "' ("
                . get_debug_type($value) . ") to {$targetLabel}",
        );
    }

    private static function noRule(mixed $value, string $targetLabel, string $context): SimpleOrmException
    {
        return new SimpleOrmException(
            'MAP-030',
            $context,
            'no conversion or handler from ' . get_debug_type($value) . " to {$targetLabel}; register a TypeHandler (§7.9)",
        );
    }
}
