<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Session;

use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use ReflectionProperty;
use SimpleOrm\Dialect\SqliteDialect;
use SimpleOrm\Errors\SimpleOrmException;
use SimpleOrm\Session\Db;
use SimpleOrm\Session\DbOptions;
use SimpleOrm\Session\TrackingMode;
use SimpleOrm\Tests\Sample\Models\Transaction;
use SimpleOrm\Tests\Sample\Models\TransactionDetail;
use SimpleOrm\Tests\Sample\Models\User;
use SimpleOrm\Tests\Sample\Models\UserTransactionTotal;
use SimpleOrm\Tests\Support\ConformanceClock;
use SimpleOrm\Tests\Support\ConformanceSeed;
use SimpleOrm\Tests\Support\SampleDatabase;
use SimpleOrm\Tests\Support\TempDatabase;
use SimpleOrm\Types\Decimal;

/**
 * Detach and merge (Level 3 M3, ADR-0034 §7c, spec/unit-of-work.md "Detach and merge") on a real temp-file
 * SQLite database seeded from `conformance/fixtures/seed.json`: both need a tracked session (`UOW-003`),
 * detaching a scheduled parent cancels what its persist cascade scheduled, a detached instance's pending
 * changes never flush, and merge copies values — never navigations — onto the session's instance. The
 * shared behaviors are pinned data-driven by `Conformance\MergeCasesTest`.
 */
final class DetachMergeTest extends TestCase
{
    private TempDatabase $fixture;

    private Db $db;

    private Db $observer;

    protected function setUp(): void
    {
        $this->fixture = TempDatabase::create();
        $this->db = SampleDatabase::open(
            $this->fixture,
            new DbOptions(new SqliteDialect(), tracking: TrackingMode::Tracked, clock: ConformanceClock::fixed()),
        );
        ConformanceSeed::insert($this->fixture);
        $this->observer = Db::open($this->fixture->connectionString(), new DbOptions(new SqliteDialect()));
    }

    protected function tearDown(): void
    {
        $this->observer->close();
        $this->db->close();
        $this->fixture->delete();
    }

    private function code(callable $action): string
    {
        try {
            $action();
        } catch (SimpleOrmException $e) {
            return $e->errorCode;
        }

        self::fail('expected a SimpleOrmException, none was thrown');
    }

    private static function copyOfAda(): User
    {
        $copy = new User();
        $copy->id = 1;
        $copy->name = 'Ada Lovelace';
        $copy->email = 'ada@example.com';
        $copy->createdAtUtc = SampleDatabase::seedTime();

        return $copy;
    }

    #[Test]
    public function detach_and_merge_need_a_tracked_session(): void
    {
        $scratch = TempDatabase::create();
        $untracked = SampleDatabase::open($scratch, new DbOptions(new SqliteDialect()));
        try {
            self::assertSame('UOW-003', $this->code(static fn () => $untracked->detach(new User())));
            self::assertSame('UOW-003', $this->code(static fn () => $untracked->merge(new User())));
        } finally {
            $untracked->close();
            $scratch->delete();
        }
    }

    #[Test]
    public function detaching_a_scheduled_parent_cancels_its_cascaded_inserts(): void
    {
        $detail = new TransactionDetail();
        $detail->description = 'Widget';
        $detail->quantity = 1;
        $detail->unitPrice = Decimal::of('2.50');
        $detail->createdAtUtc = SampleDatabase::seedTime();
        $parent = new Transaction();
        $parent->userId = 1;
        $parent->amount = Decimal::of('5');
        $parent->createdAtUtc = SampleDatabase::seedTime();
        (new ReflectionProperty(Transaction::class, 'details'))->setValue($parent, [$detail]);

        $this->db->persist($parent);
        self::assertTrue($this->db->hasPendingWork());
        $this->db->detach($parent);

        self::assertFalse($this->db->hasPendingWork());
        $summary = $this->db->flush();
        self::assertSame([0, 0, 0], [$summary->inserted, $summary->updated, $summary->deleted]);
        self::assertCount(1, $this->observer->queryAll(Transaction::class));
    }

    #[Test]
    public function a_detached_instances_changes_never_flush(): void
    {
        $user = $this->db->get(User::class, 1);
        $user->name = 'Changed locally';
        $this->db->detach($user);

        self::assertSame(0, $this->db->flush()->updated);
        self::assertSame('Ada', $this->observer->get(User::class, 1)->name);
    }

    #[Test]
    public function detaching_an_instance_whose_key_was_changed_forgets_that_instance_only(): void
    {
        $ada = $this->db->get(User::class, 1);
        $ada->id = 7;   // a tracked key is identity: the session still knows it under key 1
        $this->db->detach($ada);

        self::assertFalse($this->db->isTracked($ada));
        $again = $this->db->get(User::class, 1);
        self::assertNotSame($ada, $again);
        self::assertSame('Ada', $again->name);
    }

    #[Test]
    public function merge_copies_values_but_leaves_a_loaded_navigation_alone(): void
    {
        $tracked = $this->db->get(User::class, 1);
        $this->db->load($tracked, 'transactions');
        self::assertCount(1, $tracked->transactions);

        $merged = $this->db->merge(self::copyOfAda());

        self::assertSame($tracked, $merged);
        self::assertSame('Ada Lovelace', $tracked->name);
        self::assertCount(1, $tracked->transactions, 'the copy carries no transactions, and navigations are not merged');
        self::assertSame(['name'], $this->db->changesOf($tracked));
    }

    #[Test]
    public function a_property_the_copy_never_initialized_is_left_as_it_is_on_the_session_instance(): void
    {
        $tracked = $this->db->get(User::class, 1);
        $copy = new User();
        $copy->id = 1;
        $copy->name = 'Ada Lovelace';
        $copy->email = 'ada@example.com';   // createdAtUtc is never set on the copy

        $this->db->merge($copy);

        self::assertEquals(SampleDatabase::seedTime(), $tracked->createdAtUtc);
        self::assertSame(['name'], $this->db->changesOf($tracked));
    }

    #[Test]
    public function the_copy_stays_untracked_and_merging_the_sessions_own_instance_returns_it(): void
    {
        $copy = self::copyOfAda();
        $merged = $this->db->merge($copy);

        self::assertFalse($this->db->isTracked($copy));
        self::assertTrue($this->db->isTracked($merged));
        self::assertSame($merged, $this->db->merge($merged));
    }

    #[Test]
    public function merge_refuses_what_has_no_row_no_key_or_no_write_path(): void
    {
        $ghost = self::copyOfAda();
        $ghost->id = 99;
        self::assertSame('CRUD-001', $this->code(fn () => $this->db->merge($ghost)));

        $unsaved = new User();
        self::assertSame('CRUD-002', $this->code(fn () => $this->db->merge($unsaved)));

        self::assertSame('CRUD-003', $this->code(fn () => $this->db->merge(new UserTransactionTotal())));
    }

    #[Test]
    public function merge_of_an_unknown_key_tracks_the_row_it_read(): void
    {
        $merged = $this->db->merge(self::copyOfAda());

        self::assertTrue($this->db->isTracked($merged));
        self::assertSame($merged, $this->db->get(User::class, 1));
        self::assertSame(0, $this->db->flush()->inserted);
    }
}
