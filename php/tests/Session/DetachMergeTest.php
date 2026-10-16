<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Session;

use DateTimeImmutable;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use ReflectionProperty;
use SimpleOrm\Dialect\SqliteDialect;
use SimpleOrm\Errors\SimpleOrmException;
use SimpleOrm\Metadata\Attributes\Column;
use SimpleOrm\Metadata\Attributes\Key;
use SimpleOrm\Metadata\Attributes\Table;
use SimpleOrm\Metadata\ColumnType;
use SimpleOrm\Session\Db;
use SimpleOrm\Session\DbOptions;
use SimpleOrm\Session\TrackingMode;
use SimpleOrm\Tests\Sample\Models\Address;
use SimpleOrm\Tests\Sample\Models\DailySales;
use SimpleOrm\Tests\Sample\Models\Transaction;
use SimpleOrm\Tests\Sample\Models\TransactionDetail;
use SimpleOrm\Tests\Sample\Models\User;
use SimpleOrm\Tests\Sample\Models\UserActivityReport;
use SimpleOrm\Tests\Sample\Models\UserProfile;
use SimpleOrm\Tests\Sample\Models\UserRole;
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

    #[Test]
    public function detaching_a_tracked_owner_cancels_nothing_it_scheduled_and_its_children_still_take_its_key(): void
    {
        $order = $this->db->get(Transaction::class, 1);
        $child = new TransactionDetail();
        $child->description = 'Cascaded child';
        $child->quantity = 1;
        $child->unitPrice = Decimal::of('2.50');
        $child->createdAtUtc = SampleDatabase::seedTime();
        (new ReflectionProperty(Transaction::class, 'details'))->setValue($order, [$child]);

        $this->db->persist($order);   // tracked: schedules nothing for itself, cascades to the loaded child
        $this->db->detach($order);

        self::assertFalse($this->db->isTracked($order));
        self::assertTrue($this->db->hasPendingWork(), 'the child the owner scheduled stays scheduled');
        $summary = $this->db->flush();
        self::assertSame([1, 0, 0], [$summary->inserted, $summary->updated, $summary->deleted]);
        self::assertSame(1, $child->transactionId, 'the child still took its detached owner\'s key');
        self::assertTrue($this->db->isTracked($child));
    }

    #[Test]
    public function a_dropped_removal_leaves_the_removals_its_cascade_scheduled(): void
    {
        $order = $this->db->get(Transaction::class, 1);
        $child = new TransactionDetail();
        $child->transactionId = 1;
        $child->description = 'Child';
        $child->quantity = 1;
        $child->unitPrice = Decimal::of('2.50');
        $child->createdAtUtc = SampleDatabase::seedTime();
        $this->db->insert($child);
        (new ReflectionProperty(Transaction::class, 'details'))->setValue($order, [$child]);

        $this->db->remove($order);   // Transaction.details cascades remove: both removals are scheduled
        $this->db->detach($order);

        $summary = $this->db->flush();
        self::assertSame([0, 0, 1], [$summary->inserted, $summary->updated, $summary->deleted]);
        self::assertCount(1, $this->observer->queryAll(Transaction::class), 'the detached owner\'s removal was dropped');
        self::assertCount(0, $this->observer->queryAll(TransactionDetail::class), 'only the child\'s removal flushed');
    }

    #[Test]
    public function detach_is_by_instance_never_by_key(): void
    {
        $ada = $this->db->get(User::class, 1);
        $sameKey = self::copyOfAda();

        self::assertSame('UOW-001', $this->code(fn () => $this->db->detach($sameKey)));
        self::assertTrue($this->db->isTracked($ada), 'the tracked instance for that key is untouched');
    }

    #[Test]
    public function merge_needs_a_table_backed_keyed_entity_at_merge_time(): void
    {
        self::assertSame('CRUD-003', $this->code(fn () => $this->db->merge(new UserTransactionTotal())));
        self::assertSame('CRUD-003', $this->code(fn () => $this->db->merge(new DailySales())));
        self::assertSame('CRUD-003', $this->code(fn () => $this->db->merge(new UserActivityReport())));
    }

    #[Test]
    public function an_explicit_zero_generated_key_and_an_empty_client_guid_are_unset_keys_but_a_natural_zero_key_is_a_key(): void
    {
        $zero = self::copyOfAda();
        $zero->id = 0;
        self::assertSame('CRUD-002', $this->code(fn () => $this->db->merge($zero)), 'an explicit 0 is not a key value');

        $this->db->createTable(MergeGuidDoc::class);
        $emptyGuid = new MergeGuidDoc();
        $emptyGuid->id = '';
        $emptyGuid->title = 'unsaved';
        self::assertSame('CRUD-002', $this->code(fn () => $this->db->merge($emptyGuid)), 'an empty client-generated GUID is not a key');

        $link = new UserRole();
        $link->userId = 0;
        $link->roleId = 0;
        self::assertSame('CRUD-001', $this->code(fn () => $this->db->merge($link)), 'a natural (0, 0) key is a key: the row is read and not found');
    }

    #[Test]
    public function merging_the_sessions_own_instance_is_a_no_op_before_any_key_check(): void
    {
        $ada = $this->db->get(User::class, 1);
        $ada->name = 'Edited';
        $ada->id = 0;   // no longer a key value, yet still the session's own instance

        self::assertSame($ada, $this->db->merge($ada));
        self::assertSame('Edited', $ada->name);
    }

    #[Test]
    public function a_tracked_target_scheduled_for_removal_still_takes_the_values_and_audit_columns_merge_like_any_value(): void
    {
        $grace = $this->db->get(User::class, 2);
        $this->db->remove($grace);

        $stamp = new DateTimeImmutable('2020-01-02T03:04:05Z');
        $copy = self::copyOfAda();
        $copy->id = 2;
        $copy->name = 'Merged';
        $copy->email = 'grace@example.com';
        $copy->updatedAtUtc = $stamp;
        $merged = $this->db->merge($copy);

        self::assertSame($grace, $merged);
        self::assertSame('Merged', $grace->name);
        self::assertEquals($stamp, $grace->updatedAtUtc, 'audit columns merge like any non-key value');
        self::assertTrue($this->db->hasPendingWork(), 'the removal stays scheduled');
    }

    #[Test]
    public function merge_copies_an_owned_type_member_by_member_so_nothing_is_aliased(): void
    {
        $profile = $this->db->get(UserProfile::class, 1);
        $original = $profile->address;
        self::assertNotNull($original);

        $copy = new UserProfile();
        $copy->id = 1;
        $copy->userId = 1;
        $copy->createdAtUtc = SampleDatabase::seedTime();
        $copy->address = new Address();
        $copy->address->street = '2 High St';
        $copy->address->city = 'Leeds';
        $merged = $this->db->merge($copy);

        self::assertSame($profile, $merged);
        self::assertNotSame($copy->address, $profile->address, 'the copy\'s Address is not shared with the session\'s instance');
        self::assertSame(['Leeds', '2 High St'], [$profile->address->city, $profile->address->street]);
        $copy->address->city = 'Mutated afterwards';
        self::assertSame('Leeds', $profile->address->city);

        $absent = new UserProfile();
        $absent->id = 1;
        $absent->userId = 1;
        $this->db->merge($absent);
        self::assertNull($profile->address, 'a copy with no address clears the session\'s one');
    }
}

/** @internal test fixture for the client-GUID key strategy on merge — not a Sample model (CODING-STANDARD §7). */
#[Table('merge_guid_docs')]
final class MergeGuidDoc
{
    #[Key]
    #[Column(type: ColumnType::Guid)]
    public string $id;

    #[Column]
    public string $title;
}
