<?php

declare(strict_types=1);

namespace SimpleOrm\Sample\Migrations\Table\User;

use SimpleOrm\Migrations\TableActions;
use SimpleOrm\Migrations\TableMigration;
use SimpleOrm\Sample\Models\Tables\User;

/**
 * A real change migration: literal column spec (frozen forever), with the
 * per-action post hook backfilling existing rows. Data work rides the same
 * version atomicity as the DDL. No hand-written down (ADR-0016/0018): the
 * rollback derives from the versioned schema snapshots.
 */
final class V0002_AddDisplayName extends TableMigration
{
    public function entityClass(): string
    {
        return User::class;
    }

    public function action(TableActions $actions): void
    {
        $actions
            ->addColumn('display_name', 'TEXT')
            ->post('update users set display_name = name');
    }
}
