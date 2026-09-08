<?php

declare(strict_types=1);

namespace SimpleOrm\Sample\Migrations\Table\Transaction;

use SimpleOrm\Migrations\TableActions;
use SimpleOrm\Migrations\TableMigration;
use SimpleOrm\Sample\Models\Tables\Transaction;

/** Frozen to literal SQL when V0003 changed the table (ADR-0013/0016): the shape transactions had at V0001. */
final class V0001_CreateTransactions extends TableMigration
{
    public function entityClass(): string
    {
        return Transaction::class;
    }

    public function action(TableActions $actions): void
    {
        $actions->sql(<<<'SQL'
            create table if not exists transactions (
                id          INTEGER PRIMARY KEY,
                user_id     INTEGER NOT NULL,
                status      TEXT NOT NULL,
                amount      TEXT NOT NULL,
                version     INTEGER NOT NULL,
                created_at  TEXT NOT NULL,
                updated_at  TEXT
            ) STRICT
            SQL);
        $actions->sql('create index if not exists ix_transactions_user_id on transactions (user_id)');
        $actions->sql('create index if not exists ix_transactions_status_created on transactions (status, created_at desc)');
    }
}
