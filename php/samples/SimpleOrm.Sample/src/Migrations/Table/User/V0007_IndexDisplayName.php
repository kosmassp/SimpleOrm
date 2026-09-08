<?php

declare(strict_types=1);

namespace SimpleOrm\Sample\Migrations\Table\User;

use SimpleOrm\Migrations\TableActions;
use SimpleOrm\Migrations\TableMigration;
use SimpleOrm\Sample\Models\Tables\User;

/** DDL plus a data step in one version: index the column, then backfill the gaps. */
final class V0007_IndexDisplayName extends TableMigration
{
    public function entityClass(): string
    {
        return User::class;
    }

    public function action(TableActions $actions): void
    {
        $actions->sql('create index if not exists ix_users_display_name on users (display_name)');
        $actions->sql('update users set display_name = name where display_name is null');
    }
}
