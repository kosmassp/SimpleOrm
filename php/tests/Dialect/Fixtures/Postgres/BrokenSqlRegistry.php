<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Dialect\Fixtures\Postgres;

use SimpleOrm\Session\EmptyArgs;
use SimpleOrm\Session\Query;

/**
 * Entries whose SQL the server refuses: PostgreSQL aborts the surrounding transaction on any statement error,
 * so SchemaGuard's shield must contain each failure (`VAL-001`) without failing every entry after it.
 */
final class BrokenSqlRegistry
{
    public static function doesNotParse(): Query
    {
        return Query::inline(EmptyArgs::class, 'int', 'select frm users');
    }

    public static function parsesButFailsToPlan(): Query
    {
        return Query::inline(EmptyArgs::class, 'int', 'select count(id) from no_such_table');
    }

    public static function fine(): Query
    {
        return Query::inline(EmptyArgs::class, 'int', 'select count(id) as n from users -- notnull: n');
    }
}
