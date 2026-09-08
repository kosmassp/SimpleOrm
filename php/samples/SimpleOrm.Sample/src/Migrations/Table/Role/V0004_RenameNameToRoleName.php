<?php

declare(strict_types=1);

namespace SimpleOrm\Sample\Migrations\Table\Role;

use SimpleOrm\Migrations\TableActions;
use SimpleOrm\Migrations\TableMigration;
use SimpleOrm\Sample\Models\Tables\Role;

/**
 * A rename as a first-class action (never inferable by a differ): existing rows,
 * including the V0001 'admin' seed, keep their data.
 */
final class V0004_RenameNameToRoleName extends TableMigration
{
    public function entityClass(): string
    {
        return Role::class;
    }

    public function action(TableActions $actions): void
    {
        $actions->renameColumn('name', 'role_name');
    }
}
