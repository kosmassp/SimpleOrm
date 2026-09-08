<?php

declare(strict_types=1);

namespace SimpleOrm\Sample\Migrations\Table\TransactionDetail;

use SimpleOrm\Migrations\TableActions;
use SimpleOrm\Migrations\TableMigration;
use SimpleOrm\Sample\Models\Tables\TransactionDetail;

/** Metadata-rendered create (ADR-0013): legal because transaction_details never changes after V0001. */
final class V0001_CreateTransactionDetails extends TableMigration
{
    public function entityClass(): string
    {
        return TransactionDetail::class;
    }

    public function action(TableActions $actions): void
    {
        $actions->createTable();
    }
}
