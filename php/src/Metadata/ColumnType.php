<?php

declare(strict_types=1);

namespace SimpleOrm\Metadata;

use DateTimeImmutable;
use SimpleOrm\Types\Decimal;

/**
 * The neutral type vocabulary of spec/metadata-model.md — the export token and
 * the key of the fixed conversion table (§7.9). The loaders resolve every mapped
 * property to one token once (from the PHP type, or from an explicit
 * `#[Column(type: …)]`); every other subsystem — conversion, storage types,
 * export — reads the token, never the PHP type (CODING-STANDARD §10).
 */
enum ColumnType: string
{
    case Int16 = 'int16';
    case Int32 = 'int32';
    case Int64 = 'int64';
    case Decimal = 'decimal';
    case Double = 'double';
    case Float = 'float';
    case Bool = 'bool';
    case String = 'string';
    case Guid = 'guid';
    case Bytes = 'bytes';
    case DateTime = 'datetime';
    case DateTimeOffset = 'datetimeoffset';
    case Date = 'date';
    case Time = 'time';
    case EnumText = 'enum_text';
    case EnumInt = 'enum_int';

    /** A type outside the vocabulary: needs a registered TypeHandler; exports as `php:<class>` and is not portable. */
    case Custom = 'custom';

    /**
     * The default token for a declared PHP type (§10 of the standard): the only
     * place the PHP-type → token rule lives. Enums resolve to EnumText here;
     * `#[EnumAsInt]` switches to EnumInt in the loader.
     *
     * @param string $phpType a builtin name (`int`, `string`, …) or a class-string
     */
    public static function fromPhpType(string $phpType): self
    {
        return match (true) {
            $phpType === 'int' => self::Int64,
            $phpType === 'float' => self::Double,
            $phpType === 'bool' => self::Bool,
            $phpType === 'string' => self::String,
            $phpType === Decimal::class => self::Decimal,
            $phpType === DateTimeImmutable::class => self::DateTime,
            enum_exists($phpType) => self::EnumText,
            default => self::Custom,
        };
    }

    public function isInteger(): bool
    {
        return $this === self::Int16 || $this === self::Int32 || $this === self::Int64;
    }

    public function isEnum(): bool
    {
        return $this === self::EnumText || $this === self::EnumInt;
    }

    public function isTemporal(): bool
    {
        return $this === self::DateTime || $this === self::DateTimeOffset || $this === self::Date || $this === self::Time;
    }
}
