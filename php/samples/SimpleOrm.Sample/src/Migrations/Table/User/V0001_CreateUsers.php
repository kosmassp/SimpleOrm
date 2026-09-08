<?php

declare(strict_types=1);

namespace SimpleOrm\Sample\Migrations\Table\User;

use SimpleOrm\Migrations\TableActions;
use SimpleOrm\Migrations\TableMigration;
use SimpleOrm\Sample\Models\Tables\User;

/**
 * Frozen to literal SQL (ADR-0013): metadata-rendered creates are only safe while
 * the object never changes again. V0002 adds `display_name`, so V0001 must stay
 * the shape users had *then*, not whatever the entity looks like today. This is
 * the freeze the diff generator performs automatically when it emits a
 * follow-up migration for an object.
 */
final class V0001_CreateUsers extends TableMigration
{
    public function entityClass(): string
    {
        return User::class;
    }

    public function action(TableActions $actions): void
    {
        $actions->sql(<<<'SQL'
            create table if not exists users (
                id          INTEGER PRIMARY KEY,
                name        TEXT NOT NULL,
                email       TEXT NOT NULL,
                created_at  TEXT NOT NULL,
                updated_at  TEXT
            ) STRICT
            SQL);
        $actions->sql('create unique index if not exists ix_users_email on users (email)');
    }
}
