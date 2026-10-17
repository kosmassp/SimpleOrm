<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Dialect\Fixtures\Postgres;

use SimpleOrm\Session\Query;
use SimpleOrm\Tests\Session\Fixtures\UserEmailRow;

/** Registry entries written PostgreSQL-natively (spec/dialects.md): array parameters via `= any(@param)`. */
final class PostgresRegistry
{
    public static function usersByIds(): Query
    {
        return Query::inline(IdsArgs::class, UserEmailRow::class, 'select id, email from users where id = any(@ids) order by id');
    }

    public static function usersByNames(): Query
    {
        return Query::inline(
            NamesArgs::class,
            UserEmailRow::class,
            'select id, email from users where name = any(@names) order by id',
        );
    }

    public static function usersCreatedAt(): Query
    {
        return Query::inline(
            StampsArgs::class,
            UserEmailRow::class,
            'select id, email from users where created_at = any(@stamps) order by id',
        );
    }
}
