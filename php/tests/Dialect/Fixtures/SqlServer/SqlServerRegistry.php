<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Dialect\Fixtures\SqlServer;

use SimpleOrm\Session\Query;
use SimpleOrm\Tests\Session\Fixtures\UserEmailRow;

/**
 * A registry the live SchemaGuard run validates. `pdo_sqlsrv` reports no base table for a result column, so every
 * column is declared `-- notnull:` (the expression-column rule of §7.19) instead of being checked against the schema.
 */
final class SqlServerRegistry
{
    public static function userById(): Query
    {
        return Query::inline(
            ByIdArgs::class,
            UserEmailRow::class,
            "select id, email from users where id = @id\n-- notnull: id, email",
        );
    }
}
