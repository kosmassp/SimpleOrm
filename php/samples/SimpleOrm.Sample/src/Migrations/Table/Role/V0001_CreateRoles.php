<?php

declare(strict_types=1);

namespace SimpleOrm\Sample\Migrations\Table\Role;

use SimpleOrm\Migrations\TableActions;
use SimpleOrm\Migrations\TableMigration;
use SimpleOrm\Sample\Models\Tables\Role;

/** Frozen to literal SQL when V0004 renamed the column (ADR-0013/0016): the shape roles had at V0001. */
final class V0001_CreateRoles extends TableMigration
{
    public function entityClass(): string
    {
        return Role::class;
    }

    public function action(TableActions $actions): void
    {
        // Post-create hook: seed data belongs to the create action, not a separate mechanism.
        $actions
            ->sql(<<<'SQL'
                create table if not exists roles (
                    id          INTEGER PRIMARY KEY,
                    name        TEXT NOT NULL,
                    created_at  TEXT NOT NULL,
                    updated_at  TEXT
                ) STRICT
                SQL)
            ->post("insert into roles (name, created_at) values ('admin', '2026-01-01T00:00:00.0000000Z')");
    }
}
