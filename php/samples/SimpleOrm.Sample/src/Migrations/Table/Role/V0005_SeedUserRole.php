<?php

declare(strict_types=1);

namespace SimpleOrm\Sample\Migrations\Table\Role;

use SimpleOrm\Migrations\MigrationSql;
use SimpleOrm\Migrations\TableActions;
use SimpleOrm\Migrations\TableMigration;
use SimpleOrm\Sample\Models\Tables\Role;

/**
 * A data-only step: no DDL, just rows; the same mechanism, the same atomicity.
 * The derived rollback (ADR-0018) sees an unchanged schema and reverts nothing,
 * which is correct for structure. The seeded row is data, so the preDown hook
 * carries its removal (data work is always hook territory, never derived).
 */
final class V0005_SeedUserRole extends TableMigration
{
    public function entityClass(): string
    {
        return Role::class;
    }

    public function action(TableActions $actions): void
    {
        $actions->sql("insert into roles (role_name, created_at) values ('user', '2026-08-29T00:00:00.0000000Z')");
    }

    public function preDown(MigrationSql $sql): void
    {
        $sql->sql("delete from roles where role_name = 'user'");
    }
}
