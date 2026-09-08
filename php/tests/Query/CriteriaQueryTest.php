<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Query;

use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use SimpleOrm\Errors\SimpleOrmException;
use SimpleOrm\Query\Criteria;
use SimpleOrm\Query\SortOrder;
use SimpleOrm\Session\Db;
use SimpleOrm\Tests\Sample\Models\DailySales;
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
 * ADR-0012: key reads (`get`/`getOrDefault`) and the criteria query core
 * (`Db::from()`). Mirrors dotnet's `CriteriaAndKeyReadTests`.
 */
final class CriteriaQueryTest extends TestCase
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

    /** ADR-0020 null semantics, against a real database. */
    #[Test]
    public function null_criteria_render_is_null_and_actually_match(): void
    {
        $ada = SampleDatabase::insertUser($this->db, 'AdaNull', 'ada-null@example.com');
        $grace = SampleDatabase::insertUser($this->db, 'GraceNull', 'grace-null@example.com');
        $grace->displayName = 'Countess';
        $this->db->update($grace);

        // Eq(null) renders IS NULL — it finds the row a '= NULL' comparison would silently miss.
        $unset = $this->db->from(User::class)
            ->where(Criteria::eq('displayName', null), Criteria::like('name', '%Null'))
            ->toList();
        self::assertCount(1, $unset);
        self::assertSame($ada->id, $unset[0]->id);

        $set = $this->db->from(User::class)
            ->where(Criteria::ne('displayName', null), Criteria::like('name', '%Null'))
            ->toList();
        self::assertCount(1, $set);
        self::assertSame($grace->id, $set[0]->id);

        // Ordered comparison with null and null-in-IN are refused, not rendered.
        try {
            $this->db->from(User::class)->where(Criteria::gt('id', null))->toList();
            self::fail('expected QRY-007');
        } catch (SimpleOrmException $e) {
            self::assertSame('QRY-007', $e->errorCode);
        }

        try {
            $this->db->from(User::class)->where(Criteria::in('displayName', ['x', null]))->toList();
            self::fail('expected QRY-007');
        } catch (SimpleOrmException $e) {
            self::assertSame('QRY-007', $e->errorCode);
        }
    }

    #[Test]
    public function get_by_key_reads_and_misses_with_codes(): void
    {
        $ada = SampleDatabase::insertUser($this->db, 'Ada', 'ada@example.com');

        self::assertSame('Ada', $this->db->get(User::class, $ada->id)->name);

        try {
            $this->db->get(User::class, 999_999);
            self::fail('expected CRUD-001');
        } catch (SimpleOrmException $e) {
            self::assertSame('CRUD-001', $e->errorCode);
        }

        self::assertNull($this->db->getOrDefault(User::class, 999_999));
    }

    #[Test]
    public function composite_keys_pass_lists_and_validate_shape(): void
    {
        $ada = SampleDatabase::insertUser($this->db, 'Ada', 'ada@example.com');
        $role = new Role();
        $role->name = 'admin';
        $role->createdAtUtc = SampleDatabase::seedTime();
        $this->db->insert($role);

        $link = new UserRole();
        $link->userId = $ada->id;
        $link->roleId = $role->id;
        $link->createdAtUtc = SampleDatabase::seedTime();
        $this->db->insert($link);

        $loaded = $this->db->get(UserRole::class, [$ada->id, $role->id]);
        self::assertSame($role->id, $loaded->roleId);

        try {
            $this->db->get(UserRole::class, $ada->id);
            self::fail('expected CRUD-002 (arity)');
        } catch (SimpleOrmException $e) {
            self::assertSame('CRUD-002', $e->errorCode);
        }

        try {
            $this->db->get(User::class, 'not a key');
            self::fail('expected CRUD-002 (type)');
        } catch (SimpleOrmException $e) {
            self::assertSame('CRUD-002', $e->errorCode);
        }

        try {
            $this->db->get(DailySales::class, 1);
            self::fail('expected QRY-005 (keyless statement)');
        } catch (SimpleOrmException $e) {
            self::assertSame('QRY-005', $e->errorCode);
        }
    }

    #[Test]
    public function owner_shape_or_and_in_with_implicit_and(): void
    {
        $ada = SampleDatabase::insertUser($this->db, 'Ada', 'ada@example.com');
        SampleDatabase::insertUser($this->db, 'Grace', 'grace@example.com');
        SampleDatabase::insertUser($this->db, 'Edsger', 'edsger@example.com');

        // (id = ada OR name IN ('Grace','Nope')) AND createdAtUtc >= seed-1day
        $users = $this->db->from(User::class)
            ->where(
                Criteria::or(
                    Criteria::eq('id', $ada->id),
                    Criteria::in('name', ['Grace', 'Nope']),
                ),
                Criteria::ge('createdAtUtc', SampleDatabase::seedTime()->modify('-1 day')),
            )
            ->orderBy('name')
            ->toList();

        self::assertSame(['Ada', 'Grace'], array_map(static fn (User $u): string => $u->name, $users));
    }

    #[Test]
    public function ordering_paging_null_checks_and_like(): void
    {
        SampleDatabase::insertUser($this->db, 'Ada', 'ada@example.com');
        SampleDatabase::insertUser($this->db, 'Grace', 'grace@example.com');
        SampleDatabase::insertUser($this->db, 'Edsger', 'edsger@example.com');

        $page = $this->db->from(User::class)
            ->orderBy('name', SortOrder::Desc)
            ->limit(2)
            ->offset(1)
            ->toList();
        self::assertSame(['Edsger', 'Ada'], array_map(static fn (User $u): string => $u->name, $page));

        $fresh = $this->db->from(User::class)->where(Criteria::isNull('updatedAtUtc'))->toList();
        self::assertCount(3, $fresh);

        $gr = $this->db->from(User::class)->where(Criteria::like('name', 'Gr%'))->toList();
        self::assertCount(1, $gr);
        self::assertSame('Grace', $gr[0]->name);

        $none = $this->db->from(User::class)->where(Criteria::in('id', []))->toList();
        self::assertSame([], $none);
    }

    #[Test]
    public function unknown_property_is_qry006_and_statements_are_qry005(): void
    {
        try {
            $this->db->from(User::class)->where(Criteria::eq('nope', 1))->toList();
            self::fail('expected QRY-006');
        } catch (SimpleOrmException $e) {
            self::assertSame('QRY-006', $e->errorCode);
        }

        try {
            $this->db->from(DailySales::class);
            self::fail('expected QRY-005');
        } catch (SimpleOrmException $e) {
            self::assertSame('QRY-005', $e->errorCode);
        }
    }

    #[Test]
    public function negative_limit_or_offset_is_qry008(): void
    {
        try {
            $this->db->from(User::class)->limit(-1)->toList();
            self::fail('expected QRY-008');
        } catch (SimpleOrmException $e) {
            self::assertSame('QRY-008', $e->errorCode);
        }
    }

    #[Test]
    public function criteria_work_on_views_too(): void
    {
        $ada = SampleDatabase::insertUser($this->db, 'Ada', 'ada@example.com');
        SampleDatabase::insertUser($this->db, 'Grace', 'grace@example.com');

        $transaction = new Transaction();
        $transaction->userId = $ada->id;
        $transaction->status = TransactionStatus::Completed;
        $transaction->amount = Decimal::of('10');
        $transaction->createdAtUtc = SampleDatabase::seedTime();
        $this->db->insert($transaction);

        $busy = $this->db->from(UserTransactionTotal::class)
            ->where(Criteria::gt('transactionCount', 0))
            ->toList();

        self::assertCount(1, $busy);
        self::assertSame('Ada', $busy[0]->userName);

        $viaKey = $this->db->get(UserTransactionTotal::class, $ada->id);   // keyed view read
        self::assertSame(1, $viaKey->transactionCount);
    }
}
