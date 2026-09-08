<?php

declare(strict_types=1);

namespace SimpleOrm\Sample\Repositories;

use DateTimeImmutable;
use SimpleOrm\Query\Criteria;
use SimpleOrm\Sample\Models\Statements\DailySales;
use SimpleOrm\Sample\Models\Statements\DailySalesArgs;
use SimpleOrm\Sample\Models\Tables\Transaction;
use SimpleOrm\Sample\Models\Tables\TransactionStatus;
use SimpleOrm\Session\Db;

/**
 * Transaction repository: criteria reads, the statement entity executed by type,
 * and a read-modify-write through the generated update that participates in
 * optimistic concurrency (§7.16).
 *
 * @extends Repository<Transaction>
 */
final class TransactionRepository extends Repository
{
    public function __construct(Db $db)
    {
        parent::__construct($db, Transaction::class);
    }

    /** @return list<Transaction> */
    public function getByUser(int $userId): array
    {
        /** @var list<Transaction> $transactions */
        $transactions = $this->query()->where(Criteria::eq('userId', $userId))->orderBy('id')->toList();

        return $transactions;
    }

    /** @return list<Transaction> */
    public function getByStatus(TransactionStatus $status): array
    {
        /** @var list<Transaction> $transactions */
        $transactions = $this->query()->where(Criteria::eq('status', $status))->orderBy('id')->toList();

        return $transactions;
    }

    /** @return list<DailySales> */
    public function getDailySales(DateTimeImmutable $sinceUtc): array
    {
        /** @var list<DailySales> $rows */
        $rows = $this->db->statement(DailySales::class, new DailySalesArgs($sinceUtc));

        return $rows;
    }

    /** Read-modify-write through the generated update: a stale version throws `CRUD-010` (§7.16). */
    public function setStatus(int $id, TransactionStatus $status, DateTimeImmutable $nowUtc): void
    {
        $transaction = $this->get($id);
        $transaction->status = $status;
        $transaction->updatedAtUtc = $nowUtc;
        $this->update($transaction);
    }
}
