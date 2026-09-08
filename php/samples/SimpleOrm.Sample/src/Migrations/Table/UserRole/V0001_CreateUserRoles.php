<?php

declare(strict_types=1);

namespace SimpleOrm\Sample\Migrations\Table\UserRole;

use SimpleOrm\Migrations\TableActions;
use SimpleOrm\Migrations\TableMigration;
use SimpleOrm\Sample\Models\Tables\UserRole;

/** Frozen to literal SQL when V0005 changed the table (ADR-0013/0016): the shape user_roles had at V0001. */
final class V0001_CreateUserRoles extends TableMigration
{
    public function entityClass(): string
    {
        return UserRole::class;
    }

    public function action(TableActions $actions): void
    {
        $actions->sql(<<<'SQL'
            create table if not exists user_roles (
                user_id     INTEGER NOT NULL,
                role_id     INTEGER NOT NULL,
                created_at  TEXT NOT NULL,
                updated_at  TEXT,
                primary key (user_id, role_id)
            ) STRICT
            SQL);
        $actions->sql('create index if not exists ix_user_roles_role_id on user_roles (role_id)');
    }
}
