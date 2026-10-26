<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Session\Fixtures;

use SimpleOrm\Session\Command;
use SimpleOrm\Session\EmptyArgs;
use SimpleOrm\Session\Query;
use SimpleOrm\Tests\Support\SetDisplayNameArgs;

/**
 * A small registry for the statement-log tests (ADR-0045): SchemaGuard enumerates it, which is when the session learns the
 * names `LoggedRegistry.userCount` and `LoggedRegistry.renameUser`.
 */
final class LoggedRegistry
{
    public static function userCount(): Query
    {
        return Query::inline(EmptyArgs::class, 'int', 'select count(id) as n from users -- notnull: n');
    }

    public static function renameUser(): Command
    {
        return SetDisplayNameArgs::command();
    }
}
