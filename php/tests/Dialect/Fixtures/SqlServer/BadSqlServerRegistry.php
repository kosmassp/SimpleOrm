<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Dialect\Fixtures\SqlServer;

use SimpleOrm\Session\Query;
use SimpleOrm\Tests\Session\Fixtures\UserEmailRow;

/** A registry with a statement SQL Server cannot parse against the live schema (`VAL-001`). */
final class BadSqlServerRegistry
{
    public static function ghostColumn(): Query
    {
        return Query::inline(ByIdArgs::class, UserEmailRow::class, 'select id, no_such_column from users where id = @id');
    }
}
