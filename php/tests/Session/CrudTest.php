<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Session;

use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use SimpleOrm\Errors\ConcurrencyException;
use SimpleOrm\Errors\SimpleOrmException;
use SimpleOrm\Metadata\Attributes\Column;
use SimpleOrm\Metadata\Attributes\Generated;
use SimpleOrm\Metadata\Attributes\Key;
use SimpleOrm\Metadata\Attributes\Table;
use SimpleOrm\Metadata\ColumnType;
use SimpleOrm\Query\Criteria;
use SimpleOrm\Session\Db;
use SimpleOrm\Tests\Sample\Models\MonthlySalesTotal;
use SimpleOrm\Tests\Sample\Models\Role;
use SimpleOrm\Tests\Sample\Models\Transaction;
use SimpleOrm\Tests\Sample\Models\TransactionStatus;
use SimpleOrm\Tests\Sample\Models\User;
use SimpleOrm\Tests\Sample\Models\UserRole;
use SimpleOrm\Tests\Sample\Models\UserTransactionTotal;
use SimpleOrm\Tests\Support\SampleDatabase;
use SimpleOrm\Tests\Support\TempDatabase;
use SimpleOrm\Types\Decimal;

/**
 * Generated Update/Delete, optimistic concurrency, and the client-GUID key
 * strategy (§7.14-16). Mirrors dotnet's `CrudTests` + `SampleDomainTests`'
 * DDL/read-only-source scenarios.
 */
final class CrudTest extends TestCase
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

    private static function newTransaction(int $userId): Transaction
    {
        $transaction = new Transaction();
        $transaction->userId = $userId;
        $transaction->status = TransactionStatus::Pending;
        $transaction->amount = Decimal::of('10');
        $transaction->createdAtUtc = SampleDatabase::seedTime();

        return $transaction;
    }

    #[Test]
    public function update_writes_the_full_row_by_key(): void
    {
        $ada = SampleDatabase::insertUser($this->db, 'Ada', 'ada@example.com');

        $ada->name = 'Ada Lovelace';
        $ada->updatedAtUtc = SampleDatabase::seedTime();
        $this->db->update($ada);

        $loaded = $this->db->get(User::class, $ada->id);
        self::assertSame('Ada Lovelace', $loaded->name);
        self::assertEquals(SampleDatabase::seedTime(), $loaded->updatedAtUtc);
    }

    #[Test]
    public function update_of_missing_row_is_crud001(): void
    {
        $ghost = new User();
        $ghost->id = 999_999;
        $ghost->name = 'Ghost';
        $ghost->email = 'ghost@example.com';
        $ghost->createdAtUtc = SampleDatabase::seedTime();

        try {
            $this->db->update($ghost);
            self::fail('expected CRUD-001');
        } catch (SimpleOrmException $e) {
            self::assertSame('CRUD-001', $e->errorCode);
        }
    }

    #[Test]
    public function versioned_update_increments_and_detects_conflicts(): void
    {
        $ada = SampleDatabase::insertUser($this->db, 'Ada', 'ada@example.com');
        $this->db->insert(self::newTransaction($ada->id));

        $first = $this->db->from(Transaction::class)->where(Criteria::eq('userId', $ada->id))->single();
        $stale = $this->db->get(Transaction::class, $first->id);
        self::assertSame(0, $first->version);

        $first->status = TransactionStatus::Completed;
        $this->db->update($first);
        self::assertSame(1, $first->version);   // bumped in memory (§7.16)

        $first->amount = Decimal::of('12');
        $this->db->update($first);   // sequential updates keep working
        self::assertSame(2, $first->version);

        $stale->amount = Decimal::of('99');   // still version 0
        try {
            $this->db->update($stale);
            self::fail('expected CRUD-010');
        } catch (ConcurrencyException $e) {
            self::assertSame('CRUD-010', $e->errorCode);
        }

        $current = $this->db->get(Transaction::class, $first->id);
        self::assertTrue($current->amount->equals(Decimal::of('12')));   // the stale write changed nothing
        self::assertSame(2, $current->version);
    }

    #[Test]
    public function update_only_writes_the_listed_columns_and_nothing_else(): void
    {
        $ada = SampleDatabase::insertUser($this->db, 'Ada', 'ada@example.com');

        $ada->name = 'Ada Lovelace';
        $ada->email = 'changed@example.com';   // set in memory, not listed
        $this->db->updateOnly($ada, ['name']);

        $loaded = $this->db->get(User::class, $ada->id);
        self::assertSame('Ada Lovelace', $loaded->name);
        self::assertSame('ada@example.com', $loaded->email);   // the unlisted column was not written
    }

    #[Test]
    public function update_only_keeps_row_level_concurrency_and_bumps_the_version(): void
    {
        $ada = SampleDatabase::insertUser($this->db, 'Ada', 'ada@example.com');
        $this->db->insert(self::newTransaction($ada->id));
        $first = $this->db->from(Transaction::class)->where(Criteria::eq('userId', $ada->id))->single();
        $stale = $this->db->get(Transaction::class, $first->id);

        $first->amount = Decimal::of('12');
        $this->db->updateOnly($first, ['amount']);
        self::assertSame(1, $first->version);

        $stale->status = TransactionStatus::Completed;   // a disjoint column, still version 0
        try {
            $this->db->updateOnly($stale, ['status']);
            self::fail('expected CRUD-010');
        } catch (ConcurrencyException $e) {
            self::assertSame('CRUD-010', $e->errorCode);
        }

        $current = $this->db->get(Transaction::class, $first->id);
        self::assertTrue($current->amount->equals(Decimal::of('12')));
        self::assertSame(TransactionStatus::Pending, $current->status);
        self::assertSame(1, $current->version);

        $ghost = new User();
        $ghost->id = 999_999;
        $ghost->name = 'Ghost';
        $ghost->email = 'ghost@example.com';
        $ghost->createdAtUtc = SampleDatabase::seedTime();
        try {
            $this->db->updateOnly($ghost, ['name']);
            self::fail('expected CRUD-001');
        } catch (SimpleOrmException $e) {
            self::assertSame('CRUD-001', $e->errorCode);
        }
    }

    #[Test]
    public function update_only_validates_the_list_before_writing(): void
    {
        $ada = SampleDatabase::insertUser($this->db, 'Ada', 'ada@example.com');
        $this->db->insert(self::newTransaction($ada->id));
        $tx = $this->db->from(Transaction::class)->where(Criteria::eq('userId', $ada->id))->single();

        $codeOf = function (object $entity, array $properties): string {
            try {
                $this->db->updateOnly($entity, $properties);
            } catch (SimpleOrmException $e) {
                return $e->errorCode;
            }

            return 'no error';
        };

        self::assertSame('CRUD-005', $codeOf($ada, ['nope']));
        self::assertSame('CRUD-005', $codeOf($ada, ['created_at']));   // property names, not column names
        self::assertSame('CRUD-006', $codeOf($ada, ['id']));
        self::assertSame('CRUD-006', $codeOf($tx, ['version']));
        self::assertSame('CRUD-007', $codeOf($ada, []));
        self::assertSame('CRUD-007', $codeOf($ada, ['name', 'name']));

        $readOnly = new UserTransactionTotal();
        self::assertSame('CRUD-003', $codeOf($readOnly, ['userName']));

        self::assertSame('Ada', $this->db->get(User::class, $ada->id)->name);
    }

    #[Test]
    public function delete_by_key_and_versioned_delete_by_entity(): void
    {
        $ada = SampleDatabase::insertUser($this->db, 'Ada', 'ada@example.com');

        $this->db->delete(User::class, $ada->id);
        self::assertNull($this->db->getOrDefault(User::class, $ada->id));

        try {
            $this->db->delete(User::class, $ada->id);
            self::fail('expected CRUD-001');
        } catch (SimpleOrmException $e) {
            self::assertSame('CRUD-001', $e->errorCode);
        }

        // Version-checked delete: a stale entity may not delete the row.
        $grace = SampleDatabase::insertUser($this->db, 'Grace', 'grace@example.com');
        $this->db->insert(self::newTransaction($grace->id));
        $tx = $this->db->from(Transaction::class)->where(Criteria::eq('userId', $grace->id))->single();
        $staleTx = $this->db->get(Transaction::class, $tx->id);

        $tx->status = TransactionStatus::Cancelled;
        $this->db->update($tx);   // bumps the row to version 1

        try {
            $this->db->delete(Transaction::class, $staleTx);
            self::fail('expected CRUD-010');
        } catch (ConcurrencyException $e) {
            self::assertSame('CRUD-010', $e->errorCode);
        }

        $this->db->delete(Transaction::class, $tx);   // fresh version deletes fine
        self::assertNull($this->db->getOrDefault(Transaction::class, $tx->id));
    }

    #[Test]
    public function composite_key_delete_by_tuple(): void
    {
        $ada = SampleDatabase::insertUser($this->db, 'Ada', 'ada@example.com');
        $role = new Role();
        $role->name = 'auditor';
        $role->createdAtUtc = SampleDatabase::seedTime();
        $this->db->insert($role);

        $link = new UserRole();
        $link->userId = $ada->id;
        $link->roleId = $role->id;
        $link->createdAtUtc = SampleDatabase::seedTime();
        $this->db->insert($link);

        $this->db->delete(UserRole::class, [$ada->id, $role->id]);
        self::assertNull($this->db->getOrDefault(UserRole::class, [$ada->id, $role->id]));
    }

    #[Test]
    public function writes_guard_readonly_sources(): void
    {
        $total = new UserTransactionTotal();

        try {
            $this->db->insert($total);
            self::fail('expected CRUD-003 (insert)');
        } catch (SimpleOrmException $e) {
            self::assertSame('CRUD-003', $e->errorCode);
        }

        try {
            $this->db->update($total);
            self::fail('expected CRUD-003 (update)');
        } catch (SimpleOrmException $e) {
            self::assertSame('CRUD-003', $e->errorCode);
        }

        try {
            $this->db->delete(UserTransactionTotal::class, 1);
            self::fail('expected CRUD-003 (delete)');
        } catch (SimpleOrmException $e) {
            self::assertSame('CRUD-003', $e->errorCode);
        }
    }

    #[Test]
    public function materialized_view_creation_on_sqlite_is_ddl002(): void
    {
        try {
            $this->db->createView(MonthlySalesTotal::class);
            self::fail('expected DDL-002');
        } catch (SimpleOrmException $e) {
            self::assertSame('DDL-002', $e->errorCode);
        }
    }

    #[Test]
    public function client_guid_key_strategy_round_trips(): void
    {
        $this->db->createTable(GuidDocFixture::class);

        $doc = new GuidDocFixture();
        $doc->title = 'spec';
        self::assertFalse(isset($doc->id));
        $this->db->insert($doc);
        self::assertNotSame('', $doc->id);   // client-assigned on insert

        $loaded = $this->db->get(GuidDocFixture::class, $doc->id);
        self::assertSame('spec', $loaded->title);

        $loaded->title = 'spec v2';
        $this->db->update($loaded);
        self::assertSame('spec v2', $this->db->get(GuidDocFixture::class, $doc->id)->title);

        $this->db->delete(GuidDocFixture::class, $doc->id);
        self::assertNull($this->db->getOrDefault(GuidDocFixture::class, $doc->id));
    }
}

/** @internal test fixture for the client-GUID key strategy — not a Sample model (CODING-STANDARD §7). */
#[Table('guid_docs')]
final class GuidDocFixture
{
    #[Key]
    #[Column(type: ColumnType::Guid)]
    public string $id;

    #[Column]
    public string $title;
}
