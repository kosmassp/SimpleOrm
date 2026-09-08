<?php

declare(strict_types=1);

namespace SimpleOrm\Sample\Migrations\Table\UserRole;

use SimpleOrm\Migrations\TableActions;
use SimpleOrm\Migrations\TableMigration;
use SimpleOrm\Sample\Models\Tables\UserRole;

final class V0005_AddGrantedBy extends TableMigration
{
    public function entityClass(): string
    {
        return UserRole::class;
    }

    public function action(TableActions $actions): void
    {
        $actions->addColumn('granted_by', 'TEXT');
    }
}
