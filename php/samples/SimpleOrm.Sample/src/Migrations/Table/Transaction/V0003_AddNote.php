<?php

declare(strict_types=1);

namespace SimpleOrm\Sample\Migrations\Table\Transaction;

use SimpleOrm\Migrations\TableActions;
use SimpleOrm\Migrations\TableMigration;
use SimpleOrm\Sample\Models\Tables\Transaction;

final class V0003_AddNote extends TableMigration
{
    public function entityClass(): string
    {
        return Transaction::class;
    }

    public function action(TableActions $actions): void
    {
        $actions->addColumn('note', 'TEXT');
    }
}
