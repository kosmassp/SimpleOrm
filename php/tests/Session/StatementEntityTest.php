<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Session;

use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use SimpleOrm\Errors\SimpleOrmException;
use SimpleOrm\Session\Db;
use SimpleOrm\Tests\Sample\Models\DailySales;
use SimpleOrm\Tests\Sample\Models\DailySalesArgs;
use SimpleOrm\Tests\Sample\Models\Transaction;
use SimpleOrm\Tests\Sample\Models\TransactionStatus;
use SimpleOrm\Tests\Sample\Models\User;
use SimpleOrm\Tests\Support\SampleDatabase;
use SimpleOrm\Tests\Support\TempDatabase;
use SimpleOrm\Types\Decimal;

/**
 * Statement-backed entities (ADR-0008/0010): the type IS the query — execution
 * names the result type and passes args only, with no registry entry. Mirrors
 * dotnet's `SampleDomainTests` statement scenarios.
 */
final class StatementEntityTest extends TestCase
{
    private TempDatabase $fixture;

    private Db $db;

    protected function setUp(): void
    {
        $this->fixture = TempDatabase::create();
        $this->db = SampleDatabase::open($this->fixture);
    }

    protected function tearDown(): void
    {
        $this->db->close();
        $this->fixture->delete();
    }

    private function newTransaction(int $userId, TransactionStatus $status, string $amount): Transaction
    {
        $transaction = new Transaction();
        $transaction->userId = $userId;
        $transaction->status = $status;
        $transaction->amount = Decimal::of($amount);
        $transaction->createdAtUtc = SampleDatabase::seedTime();

        return $transaction;
    }

    #[Test]
    public function statement_entity_executes_by_type_without_a_registry_entry(): void
    {
        $ada = SampleDatabase::insertUser($this->db, 'Ada', 'ada@example.com');
        $this->db->insert($this->newTransaction($ada->id, TransactionStatus::Completed, '10.00'));
        $this->db->insert($this->newTransaction($ada->id, TransactionStatus::Completed, '5.25'));

        $days = $this->db->statement(DailySales::class, new DailySalesArgs(SampleDatabase::seedTime()->modify('-1 day')));

        self::assertCount(1, $days);
        $day = $days[0];
        self::assertSame(2, $day->transactionCount);
        self::assertTrue($day->totalAmount->equals(Decimal::of('15.25')));
    }

    #[Test]
    public function statement_single_and_single_or_default_enforce_row_counts(): void
    {
        $ada = SampleDatabase::insertUser($this->db, 'Ada', 'ada@example.com');
        $this->db->insert($this->newTransaction($ada->id, TransactionStatus::Completed, '10.00'));

        $day = $this->db->statementSingle(DailySales::class, new DailySalesArgs(SampleDatabase::seedTime()->modify('-1 day')));
        self::assertSame(1, $day->transactionCount);

        $none = $this->db->statementSingleOrDefault(DailySales::class, new DailySalesArgs(SampleDatabase::seedTime()->modify('+1 day')));
        self::assertNull($none);
    }

    #[Test]
    public function stream_statement_yields_rows_lazily(): void
    {
        $ada = SampleDatabase::insertUser($this->db, 'Ada', 'ada@example.com');
        $this->db->insert($this->newTransaction($ada->id, TransactionStatus::Completed, '10.00'));

        $counts = [];
        foreach ($this->db->streamStatement(DailySales::class, new DailySalesArgs(SampleDatabase::seedTime()->modify('-1 day'))) as $day) {
            $counts[] = $day->transactionCount;
        }

        self::assertSame([1], $counts);
    }

    #[Test]
    public function statement_execution_validates_args_types_and_target_kind(): void
    {
        $wrongType = new class {
            public string $since = 'not a date';
        };

        try {
            $this->db->statement(DailySales::class, $wrongType);
            self::fail('expected PRM-012');
        } catch (SimpleOrmException $e) {
            self::assertSame('PRM-012', $e->errorCode);
        }

        try {
            $this->db->statement(User::class, new DailySalesArgs(SampleDatabase::seedTime()));
            self::fail('expected QRY-004');
        } catch (SimpleOrmException $e) {
            self::assertSame('QRY-004', $e->errorCode);
        }
    }
}
