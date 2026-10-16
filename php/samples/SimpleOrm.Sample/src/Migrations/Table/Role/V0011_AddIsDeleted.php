<?php

declare(strict_types=1);

namespace SimpleOrm\Sample\Migrations\Table\Role;

use SimpleOrm\Migrations\TableActions;
use SimpleOrm\Migrations\TableMigration;
use SimpleOrm\Sample\Models\Tables\Role;

/**
 * The #[SoftDelete] flag of Role (spec/hooks.md): NOT NULL with a literal default so existing rows
 * read as not deleted. No down — the runner derives the rollback from the snapshots (ADR-0018).
 */
final class V0011_AddIsDeleted extends TableMigration
{
    public function entityClass(): string
    {
        return Role::class;
    }

    public function action(TableActions $actions): void
    {
        $actions->addColumn('is_deleted', 'INTEGER', nullable: false, defaultSql: '0');
    }
}
