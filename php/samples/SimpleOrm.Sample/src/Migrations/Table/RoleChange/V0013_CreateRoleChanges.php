<?php

declare(strict_types=1);

namespace SimpleOrm\Sample\Migrations\Table\RoleChange;

use SimpleOrm\Migrations\TableActions;
use SimpleOrm\Migrations\TableMigration;
use SimpleOrm\Sample\Models\Tables\RoleChange;

/** The audit table's creation, rendered from metadata (an object's initial create, frozen by the checksum). No down: the runner derives the rollback from the snapshots (ADR-0018). */
final class V0013_CreateRoleChanges extends TableMigration
{
    public function entityClass(): string
    {
        return RoleChange::class;
    }

    public function action(TableActions $actions): void
    {
        $actions->createTable();
    }
}
