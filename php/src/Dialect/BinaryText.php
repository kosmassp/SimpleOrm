<?php

declare(strict_types=1);

namespace SimpleOrm\Dialect;

/**
 * The form a binary (`bytes`) column comes back in from a PDO driver (ADR-0040 add.2): `pdo_sqlite`
 * and `pdo_pgsql` hand over the bytes (the latter as a stream); `pdo_odbc` fetches every column as
 * character data, so a `varbinary` arrives as its upper-case hexadecimal text (`005CFF…`) and the
 * converter decodes it. Writes are unaffected: `PDO::PARAM_LOB` binds raw bytes on every driver.
 */
enum BinaryText
{
    case Raw;

    case Hex;
}
