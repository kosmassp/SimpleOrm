<?php

declare(strict_types=1);

namespace SimpleOrm\Parameters;

/**
 * A bound parameter whose metadata type token is `bytes` (ADR-0040 add.1):
 * {@see \SimpleOrm\Mapping\TypeConverter::toParameter()} wraps the string so
 * that {@see PdoBinder} binds it as `PDO::PARAM_LOB` — a binary parameter —
 * instead of the `PARAM_STR` a PHP string would otherwise get. PHP has one
 * string type for text and bytes, so the token is the only thing that tells
 * them apart: under `PARAM_STR` PostgreSQL parses a `bytea` payload as escaped
 * text (a backslash starts an escape, a NUL byte truncates), SQL Server
 * receives a character string for a `varbinary` column, and SQLite stores TEXT,
 * which a STRICT `BLOB` column refuses. Where C# and Go bind by the value's
 * static type (`byte[]`, `[]byte`), PHP binds by the declared token.
 */
final readonly class BinaryParameter
{
    public function __construct(public string $bytes)
    {
    }
}
