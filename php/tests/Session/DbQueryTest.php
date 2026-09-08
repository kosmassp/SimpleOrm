<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Session;

use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use SimpleOrm\Errors\SimpleOrmException;
use SimpleOrm\Mapping\JsonTypeHandler;
use SimpleOrm\Session\Db;
use SimpleOrm\Session\DbOptions;
use SimpleOrm\Session\EmptyArgs;
use SimpleOrm\Session\Query;
use SimpleOrm\Tests\Sample\Models\Transaction;
use SimpleOrm\Tests\Sample\Models\TransactionDetail;
use SimpleOrm\Tests\Sample\Models\TransactionStatus;
use SimpleOrm\Tests\Sample\Models\User;
use SimpleOrm\Tests\Session\Fixtures\DetailLine;
use SimpleOrm\Tests\Session\Fixtures\TransactionWithDetails;
use SimpleOrm\Tests\Session\Fixtures\TransactionsByUserArgs;
use SimpleOrm\Tests\Session\Fixtures\UserEmailRow;
use SimpleOrm\Tests\Support\SampleDatabase;
use SimpleOrm\Tests\Support\TempDatabase;
use SimpleOrm\Types\Decimal;

/**
 * The registry surface (spec/session.md): `query`/`querySingle`/
 * `querySingleOrDefault`/`stream`, entity and scalar/DTO results sharing one
 * pipeline, and the §7.10 JSON-nesting handler. Mirrors dotnet's
 * `DbQueryTests` and `JsonNestingTests`.
 */
final class DbQueryTest extends TestCase
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

    #[Test]
    public function query_maps_entities_through_their_entity_map(): void
    {
        SampleDatabase::insertUser($this->db, 'Ada', 'ada@example.com');
        SampleDatabase::insertUser($this->db, 'Grace', 'grace@example.com');

        $users = $this->db->queryAll(User::class);

        self::assertCount(2, $users);
        self::assertSame('Ada', $users[0]->name);
        self::assertEquals(SampleDatabase::seedTime(), $users[0]->createdAtUtc);
        self::assertSame(0, $users[0]->createdAtUtc->getOffset());   // ISO-8601 Z round trip (§7.9 UTC rule)
        self::assertNull($users[0]->updatedAtUtc);
    }

    #[Test]
    public function scalar_and_dto_results_share_the_pipeline(): void
    {
        SampleDatabase::insertUser($this->db, 'Ada', 'ada@example.com');

        $countUsers = Query::inline(EmptyArgs::class, 'int', 'select count(id) from users');
        $count = $this->db->querySingle($countUsers, EmptyArgs::value());
        self::assertSame(1, $count);

        $emailRows = Query::inline(EmptyArgs::class, UserEmailRow::class, 'select id, email from users order by id');
        $rows = $this->db->query($emailRows, EmptyArgs::value());
        self::assertCount(1, $rows);
        self::assertSame('ada@example.com', $rows[0]->email);
    }

    #[Test]
    public function query_single_enforces_row_counts_with_codes(): void
    {
        $allUsers = Query::inline(
            EmptyArgs::class,
            User::class,
            'select id, name, email, display_name, created_at, updated_at from users order by id',
        );

        try {
            $this->db->querySingle($allUsers, EmptyArgs::value());
            self::fail('expected QRY-001');
        } catch (SimpleOrmException $e) {
            self::assertSame('QRY-001', $e->errorCode);
        }

        self::assertNull($this->db->getOrDefault(User::class, 999_999));

        SampleDatabase::insertUser($this->db, 'Ada', 'ada@example.com');
        SampleDatabase::insertUser($this->db, 'Grace', 'grace@example.com');

        try {
            $this->db->querySingle($allUsers, EmptyArgs::value());
            self::fail('expected QRY-002');
        } catch (SimpleOrmException $e) {
            self::assertSame('QRY-002', $e->errorCode);
        }
    }

    #[Test]
    public function stream_yields_rows_lazily(): void
    {
        SampleDatabase::insertUser($this->db, 'Ada', 'ada@example.com');
        SampleDatabase::insertUser($this->db, 'Grace', 'grace@example.com');

        $allUsers = Query::inline(
            EmptyArgs::class,
            User::class,
            'select id, name, email, display_name, created_at, updated_at from users order by id',
        );

        $names = [];
        foreach ($this->db->stream($allUsers, EmptyArgs::value()) as $user) {
            $names[] = $user->name;
        }

        self::assertSame(['Ada', 'Grace'], $names);
    }

    #[Test]
    public function result_column_with_no_mapped_property_is_map001(): void
    {
        SampleDatabase::insertUser($this->db, 'Ada', 'ada@example.com');
        $bad = Query::inline(
            EmptyArgs::class,
            User::class,
            'select id, name, email, created_at, updated_at, 42 as mystery from users',
        );

        try {
            $this->db->query($bad, EmptyArgs::value());
            self::fail('expected MAP-001');
        } catch (SimpleOrmException $e) {
            self::assertSame('MAP-001', $e->errorCode);
            self::assertStringContainsString('mystery', $e->getMessage());
        }
    }

    #[Test]
    public function children_nest_through_the_json_handler(): void
    {
        $registry = SampleDatabase::options()->typeHandlers;
        $registry->register(new JsonTypeHandler(DetailLine::class, list: true));
        $jsonDb = Db::open(
            $this->fixture->connectionString(),
            new DbOptions(SampleDatabase::options()->dialect, SampleDatabase::options()->mapping, $registry),
        );

        $ada = SampleDatabase::insertUser($this->db, 'Ada', 'ada@example.com');
        $tx = new Transaction();
        $tx->userId = $ada->id;
        $tx->status = TransactionStatus::Completed;
        $tx->amount = Decimal::of('15.25');
        $tx->createdAtUtc = SampleDatabase::seedTime();
        $this->db->insert($tx);

        $detail1 = new TransactionDetail();
        $detail1->transactionId = $tx->id;
        $detail1->description = 'cake';
        $detail1->quantity = 2;
        $detail1->unitPrice = Decimal::of('5.00');
        $detail1->createdAtUtc = SampleDatabase::seedTime();
        $this->db->insert($detail1);

        $detail2 = new TransactionDetail();
        $detail2->transactionId = $tx->id;
        $detail2->description = 'candle';
        $detail2->quantity = 1;
        $detail2->unitPrice = Decimal::of('5.25');
        $detail2->createdAtUtc = SampleDatabase::seedTime();
        $this->db->insert($detail2);

        $withDetails = Query::inline(TransactionsByUserArgs::class, TransactionWithDetails::class, <<<'SQL'
            select t.id,
                   t.amount,
                   (select json_group_array(json_object(
                            'description', d.description,
                            'quantity', d.quantity,
                            'unit_price', d.unit_price))
                    from transaction_details d
                    where d.transaction_id = t.id) as details
            from transactions t
            where t.user_id = @userId
            order by t.id
            SQL);

        $rows = $jsonDb->query($withDetails, new TransactionsByUserArgs($ada->id));

        self::assertCount(1, $rows);
        $row = $rows[0];
        self::assertTrue($row->amount->equals(Decimal::of('15.25')));
        self::assertCount(2, $row->details);
        self::assertSame('cake', $row->details[0]->description);
        self::assertSame(2, $row->details[0]->quantity);
        self::assertSame('candle', $row->details[1]->description);

        // A parent with no children gets an empty list, never null.
        $grace = SampleDatabase::insertUser($this->db, 'Grace', 'grace@example.com');
        $lonely = new Transaction();
        $lonely->userId = $grace->id;
        $lonely->status = TransactionStatus::Pending;
        $lonely->amount = Decimal::of('1');
        $lonely->createdAtUtc = SampleDatabase::seedTime();
        $this->db->insert($lonely);

        $lonelyRows = $jsonDb->query($withDetails, new TransactionsByUserArgs($grace->id));
        self::assertCount(1, $lonelyRows);
        self::assertSame([], $lonelyRows[0]->details);

        $jsonDb->close();
    }
}
