<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Validation\Fixtures;

use SimpleOrm\Session\Command;
use SimpleOrm\Session\EmptyArgs;
use SimpleOrm\Session\Query;
use SimpleOrm\Tests\Sample\Models\User;

/**
 * One broken registry entry per SchemaGuard rule (mirrors `SchemaGuardTests.BadRegistry`).
 * Every method is a registry entry (CODING-STANDARD §10): SchemaGuard reflects,
 * calls, and validates each one.
 */
final class BadRegistry
{
    public static function badSql(): Query
    {
        return Query::inline(EmptyArgs::class, 'int', 'select frm users');
    }

    public static function star(): Query
    {
        return Query::inline(EmptyArgs::class, User::class, 'select * from users');
    }

    public static function countStarIsFine(): Query
    {
        return Query::inline(EmptyArgs::class, 'int', 'select count(*) as n from users -- notnull: n');
    }

    public static function wrongParams(): Query
    {
        return Query::inline(ProbeArgs::class, 'int', 'select count(id) from users where name = @Nope');
    }

    public static function wrongShape(): Query
    {
        return Query::inline(EmptyArgs::class, WrongShapeRow::class, 'select id, name, 1 as mystery from users');
    }

    public static function nullableIntoNonNullable(): Query
    {
        return Query::inline(EmptyArgs::class, StampRow::class, 'select updated_at as stamp from users');
    }

    public static function expressionNeedsNullable(): Query
    {
        return Query::inline(EmptyArgs::class, TotalRow::class, 'select 1 + 1 as total from users');
    }

    public static function expressionWithComment(): Query
    {
        return Query::inline(EmptyArgs::class, TotalRow::class, 'select 1 + 1 as total from users -- notnull: total');
    }

    public static function declaredTypeMismatch(): Query
    {
        return Query::inline(EmptyArgs::class, NameAsLongRow::class, 'select name from users');
    }

    public static function nowLint(): Command
    {
        return Command::inline(EmptyArgs::class, 'update users set updated_at = current_timestamp');
    }
}
