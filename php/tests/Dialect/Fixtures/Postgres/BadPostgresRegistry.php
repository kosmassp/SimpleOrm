<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Dialect\Fixtures\Postgres;

use SimpleOrm\Session\EmptyArgs;
use SimpleOrm\Session\Query;

/** One deliberate `VAL-011` mismatch for the PostgreSQL SchemaGuard leg. */
final class BadPostgresRegistry
{
    public static function emailAsInt(): Query
    {
        return Query::inline(EmptyArgs::class, EmailAsInt::class, 'select id, email from users order by id');
    }
}
