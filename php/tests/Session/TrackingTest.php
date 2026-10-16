<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Session;

use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use SimpleOrm\Dialect\SqliteDialect;
use SimpleOrm\Errors\ConcurrencyException;
use SimpleOrm\Errors\SimpleOrmException;
use SimpleOrm\Mapping\JsonTypeHandler;
use SimpleOrm\Mapping\TypeHandlerRegistry;
use SimpleOrm\Query\Criteria;
use SimpleOrm\Query\FetchMode;
use SimpleOrm\Session\Command;
use SimpleOrm\Session\Db;
use SimpleOrm\Session\DbOptions;
use SimpleOrm\Session\EmptyArgs;
use SimpleOrm\Session\Query;
use SimpleOrm\Session\TrackingMode;
use SimpleOrm\Tests\Sample\Models\Transaction;
use SimpleOrm\Tests\Sample\Models\User;
use SimpleOrm\Tests\Sample\Models\UserProfile;
use SimpleOrm\Tests\Sample\Models\UserRole;
use SimpleOrm\Tests\Session\Fixtures\Document;
use SimpleOrm\Tests\Session\Fixtures\Settings;
use SimpleOrm\Tests\Support\ConformanceSeed;
use SimpleOrm\Tests\Support\RecordingUpdateDialect;
use SimpleOrm\Tests\Support\SampleDatabase;
use SimpleOrm\Tests\Support\TempDatabase;
use SimpleOrm\Types\Decimal;

/**
 * Identity map and snapshot change tracking (Level 3 M1, ADR-0034 add.1, spec/session.md
 * "Tracking") on a real temp-file SQLite database seeded from `conformance/fixtures/seed.json`.
 * The tracked session writes through a {@see RecordingUpdateDialect} so what an update was asked
 * to render is observable; a second, untracked session on the same file verifies what reached the
 * database. The shared behaviors are also pinned data-driven by `Conformance\TrackingCasesTest`.
 */
final class TrackingTest extends TestCase
{
    private TempDatabase $fixture;

    private RecordingUpdateDialect $recorder;

    private Db $db;

    /** @var list<Db> sessions opened beside the main one, closed at teardown */
    private array $others = [];

    protected function setUp(): void
    {
        $this->fixture = TempDatabase::create();
        $this->recorder = new RecordingUpdateDialect(new SqliteDialect());
        $this->db = SampleDatabase::open($this->fixture, new DbOptions($this->recorder, tracking: TrackingMode::Tracked));
        ConformanceSeed::insert($this->fixture);
    }

    protected function tearDown(): void
    {
        foreach ($this->others as $other) {
            $other->close();
        }

        $this->db->close();
        $this->fixture->delete();
    }

    /** A second session on the same database file; untracked unless asked otherwise. */
    private function session(TrackingMode $mode = TrackingMode::Untracked, ?TypeHandlerRegistry $handlers = null): Db
    {
        $session = Db::open($this->fixture->connectionString(), new DbOptions(
            new SqliteDialect(),
            typeHandlers: $handlers ?? new TypeHandlerRegistry(),
            tracking: $mode,
        ));
        $this->others[] = $session;

        return $session;
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

    // --- the mode ----------------------------------------------------------------

    #[Test]
    public function the_default_session_is_untracked_and_materializes_fresh_instances(): void
    {
        $db = $this->session();

        $first = $db->get(User::class, 1);
        $second = $db->get(User::class, 1);

        self::assertSame(TrackingMode::Untracked, (new DbOptions(new SqliteDialect()))->tracking);
        self::assertFalse($db->isTracking());
        self::assertNotSame($first, $second);
        self::assertFalse($db->isTracked($first));
    }

    #[Test]
    public function tracking_questions_in_an_untracked_session_are_uow_001(): void
    {
        $db = $this->session();
        $user = $db->get(User::class, 1);

        self::assertSame('UOW-001', $this->code(static fn () => $db->isDirty($user)));
        self::assertSame('UOW-001', $this->code(static fn () => $db->changesOf($user)));
    }

    #[Test]
    public function a_tracked_session_refuses_questions_about_an_instance_it_does_not_hold(): void
    {
        $stranger = new User();

        self::assertTrue($this->db->isTracking());
        self::assertFalse($this->db->isTracked($stranger));
        self::assertSame('UOW-001', $this->code(fn () => $this->db->isDirty($stranger)));
    }

    // --- every read path consults the map ---------------------------------------------

    #[Test]
    public function every_read_path_returns_the_tracked_instance(): void
    {
        $tracked = $this->db->get(User::class, 1);
        $columns = 'id, name, email, display_name, created_at, updated_at';
        $registry = Query::inline(EmptyArgs::class, User::class, "select {$columns} from users where id = 1");

        self::assertSame($tracked, $this->db->getOrDefault(User::class, 1));
        self::assertSame($tracked, $this->db->query($registry, EmptyArgs::value())[0]);
        self::assertSame($tracked, $this->db->querySingle($registry, EmptyArgs::value()));
        self::assertSame($tracked, iterator_to_array($this->db->stream($registry, EmptyArgs::value()), false)[0]);
        self::assertSame($tracked, $this->db->from(User::class)->where(Criteria::eq('id', 1))->single());
        self::assertSame($tracked, $this->db->queryAll(User::class)[0]);
    }

    #[Test]
    public function a_scalar_registry_result_is_not_an_entity_and_is_never_tracked(): void
    {
        $count = Query::inline(EmptyArgs::class, 'int', 'select count(id) from users');

        self::assertSame(2, $this->db->querySingle($count, EmptyArgs::value()));
    }

    #[Test]
    #[DataProvider('fetchModes')]
    public function eager_loading_in_every_fetch_mode_returns_the_tracked_instances(FetchMode $mode): void
    {
        $user = $this->db->get(User::class, 1);
        $transaction = $this->db->get(Transaction::class, 1);

        $loaded = $this->db->from(User::class)
            ->where(Criteria::eq('id', 1))
            ->include('transactions')
            ->fetch($mode)
            ->toList();

        self::assertSame($user, $loaded[0]);
        self::assertSame($transaction, $loaded[0]->transactions[0]);
    }

    /** @return array<string, array{0: FetchMode}> */
    public static function fetchModes(): array
    {
        return [
            'MultiQuery' => [FetchMode::MultiQuery],
            'SubSelect' => [FetchMode::SubSelect],
            'Join' => [FetchMode::Join],
        ];
    }

    #[Test]
    public function explicit_loading_shares_the_tracked_instance_and_keeps_it_clean(): void
    {
        $user = $this->db->get(User::class, 1);
        $transaction = $this->db->get(Transaction::class, 1);

        $this->db->load($transaction, 'user');
        $this->db->load($user, 'transactions');

        self::assertSame($user, $transaction->user);
        self::assertSame($transaction, $user->transactions[0]);
        self::assertFalse($this->db->isDirty($user));
    }

    // --- writes ----------------------------------------------------------------------

    #[Test]
    public function a_dirty_update_writes_only_the_changed_columns_and_bumps_the_version(): void
    {
        $transaction = $this->db->get(Transaction::class, 1);
        $transaction->note = 'checked';
        $this->recorder->updates = [];

        $this->db->update($transaction);

        self::assertSame([['note', 'updatedAtUtc']], $this->recorder->updates);
        self::assertSame(1, $transaction->version);
        self::assertFalse($this->db->isDirty($transaction));

        $stored = $this->session()->get(Transaction::class, 1);
        self::assertSame('checked', $stored->note);
        self::assertSame(1, $stored->version);
    }

    #[Test]
    public function an_update_never_overwrites_a_column_the_instance_did_not_change(): void
    {
        $user = $this->db->get(User::class, 1);

        // Another session changes the email behind the tracked instance's back.
        $other = $this->session()->get(User::class, 1);
        $other->email = 'moved@example.com';
        $this->session()->update($other);

        $user->name = 'Ada Lovelace';
        $this->db->update($user);

        $stored = $this->session()->get(User::class, 1);
        self::assertSame('Ada Lovelace', $stored->name);
        self::assertSame('moved@example.com', $stored->email);
    }

    #[Test]
    public function a_clean_update_issues_no_statement_and_no_version_bump(): void
    {
        $transaction = $this->db->get(Transaction::class, 1);
        $this->recorder->updates = [];

        $this->db->update($transaction);

        self::assertSame([], $this->recorder->updates);
        self::assertSame(0, $transaction->version);
        self::assertSame(0, $this->session()->get(Transaction::class, 1)->version);
    }

    #[Test]
    public function a_stale_concurrent_update_is_still_a_concurrency_conflict_and_leaves_the_instance_dirty(): void
    {
        $transaction = $this->db->get(Transaction::class, 1);

        $rival = $this->session();
        $theirs = $rival->get(Transaction::class, 1);
        $theirs->note = 'theirs';
        $rival->update($theirs);

        $transaction->note = 'mine';
        try {
            $this->db->update($transaction);
            self::fail('expected a ConcurrencyException');
        } catch (ConcurrencyException $e) {
            self::assertSame('CRUD-010', $e->errorCode);
        }

        self::assertSame(['note'], $this->db->changesOf($transaction));

        $this->db->refresh($transaction);

        self::assertSame('theirs', $transaction->note);
        self::assertSame(1, $transaction->version);
        self::assertFalse($this->db->isDirty($transaction));
    }

    #[Test]
    public function a_decimal_that_differs_only_in_trailing_zeros_is_clean(): void
    {
        $transaction = $this->db->get(Transaction::class, 1);

        $transaction->amount = Decimal::of('19.990');
        self::assertFalse($this->db->isDirty($transaction));

        $transaction->amount = Decimal::of('10');
        self::assertSame(['amount'], $this->db->changesOf($transaction));
        $this->db->update($transaction);

        $transaction->amount = Decimal::of('10.0');
        self::assertFalse($this->db->isDirty($transaction));
    }

    #[Test]
    public function update_only_writes_the_listed_columns_and_leaves_other_changes_dirty(): void
    {
        $user = $this->db->get(User::class, 1);
        $user->name = 'Ada Lovelace';
        $user->email = 'lovelace@example.com';

        $this->db->updateOnly($user, ['name']);

        self::assertSame(['email'], $this->db->changesOf($user));
    }

    #[Test]
    public function an_update_of_an_instance_the_session_does_not_know_writes_the_full_row_and_tracks_it(): void
    {
        $user = new User();
        $user->id = 2;
        $user->name = 'Grace Hopper';
        $user->email = 'grace@example.com';
        $user->createdAtUtc = SampleDatabase::seedTime();
        $this->recorder->updates = [];

        $this->db->update($user);

        self::assertSame(['all'], $this->recorder->updates);
        self::assertTrue($this->db->isTracked($user));
        self::assertSame($user, $this->db->get(User::class, 2));
    }

    #[Test]
    public function insert_registers_the_instance_and_delete_forgets_it(): void
    {
        $user = SampleDatabase::insertUser($this->db, 'Linus', 'linus@example.com');

        self::assertTrue($this->db->isTracked($user));
        self::assertSame($user, $this->db->get(User::class, $user->id));

        $this->db->delete(User::class, $user->id);

        self::assertFalse($this->db->isTracked($user));
        self::assertSame('CRUD-001', $this->code(fn () => $this->db->get(User::class, $user->id)));
    }

    // --- identity ---------------------------------------------------------------------

    #[Test]
    public function a_changed_key_on_a_tracked_instance_is_uow_002_for_every_write(): void
    {
        $user = $this->db->get(User::class, 1);
        $user->id = 99;

        self::assertSame('UOW-002', $this->code(fn () => $this->db->update($user)));
        self::assertSame('UOW-002', $this->code(fn () => $this->db->updateOnly($user, ['name'])));
        self::assertSame('UOW-002', $this->code(fn () => $this->db->delete(User::class, $user)));
        self::assertSame('UOW-002', $this->code(fn () => $this->db->refresh($user)));
    }

    #[Test]
    public function a_second_instance_for_a_tracked_key_is_uow_002_before_any_sql(): void
    {
        $link = $this->db->get(UserRole::class, [1, 1]);

        $copy = new UserRole();
        $copy->userId = 1;
        $copy->roleId = 1;
        $copy->createdAtUtc = SampleDatabase::seedTime();
        $this->recorder->updates = [];

        self::assertSame('UOW-002', $this->code(fn () => $this->db->insert($copy)));
        self::assertSame('UOW-002', $this->code(fn () => $this->db->update($copy)));
        self::assertSame([], $this->recorder->updates);
        self::assertTrue($this->db->isTracked($link));
        self::assertFalse($this->db->isTracked($copy));
    }

    // --- refresh -----------------------------------------------------------------------

    #[Test]
    public function refresh_works_in_an_untracked_session_and_tracks_nothing(): void
    {
        $db = $this->session();
        $user = $db->get(User::class, 1);

        $other = $this->session()->get(User::class, 1);
        $other->name = 'Renamed elsewhere';
        $this->session()->update($other);

        $db->refresh($user);

        self::assertSame('Renamed elsewhere', $user->name);
        self::assertFalse($db->isTracked($user));
    }

    #[Test]
    public function refresh_of_a_row_that_is_gone_is_crud_001(): void
    {
        $user = $this->db->get(User::class, 2);
        $this->session()->delete(User::class, 2);

        self::assertSame('CRUD-001', $this->code(fn () => $this->db->refresh($user)));
    }

    #[Test]
    public function refresh_of_an_instance_whose_key_another_tracked_instance_holds_is_uow_002(): void
    {
        $held = $this->db->get(User::class, 1);
        $copy = new User();
        $copy->id = 1;
        $copy->name = 'Copy';

        self::assertSame('UOW-002', $this->code(fn () => $this->db->refresh($copy)));
        self::assertSame('Ada', $held->name);
        self::assertSame('Copy', $copy->name);
    }

    #[Test]
    public function refresh_of_an_unknown_instance_with_a_free_key_applies_the_row_without_registering_it(): void
    {
        $stranger = new User();
        $stranger->id = 2;

        $this->db->refresh($stranger);

        self::assertSame('Grace', $stranger->name);
        self::assertFalse($this->db->isTracked($stranger));
        self::assertNotSame($stranger, $this->db->get(User::class, 2));
    }

    // --- the snapshot holds the database-bound form (ADR-0035) -------------------------

    #[Test]
    public function a_json_handler_member_mutated_in_place_is_dirty_and_is_written(): void
    {
        $handlers = new TypeHandlerRegistry();
        $handlers->register(new JsonTypeHandler(Settings::class));
        $db = $this->session(TrackingMode::Tracked, $handlers);
        $db->createTable(Document::class);
        $document = new Document();
        $document->settings = new Settings();
        $db->insert($document);
        self::assertFalse($db->isDirty($document));

        $document->settings->size = 14;

        self::assertSame(['settings'], $db->changesOf($document));

        $db->update($document);
        $stored = $this->session(TrackingMode::Untracked, $handlers)->get(Document::class, $document->id);
        self::assertSame(14, $stored->settings->size);
        self::assertFalse($db->isDirty($document));

        $document->settings = new Settings('dark', 14);
        self::assertFalse($db->isDirty($document), 'a different object that binds the same JSON is clean');
    }

    #[Test]
    public function changes_of_lists_properties_in_metadata_order_whatever_the_order_of_the_edits(): void
    {
        $user = $this->db->get(User::class, 1);
        $user->updatedAtUtc = SampleDatabase::seedTime();
        $user->email = 'ada@lovelace.example';
        $user->name = 'Ada Lovelace';

        self::assertSame(['name', 'email', 'updatedAtUtc'], $this->db->changesOf($user));

        $profile = $this->db->get(UserProfile::class, 1);
        $profile->updatedAtUtc = SampleDatabase::seedTime();
        $profile->address->city = 'Paris';
        $profile->bio = 'Changed';

        self::assertSame(['bio', 'address.city', 'updatedAtUtc'], $this->db->changesOf($profile));
    }

    // --- every write passes the identity gate -------------------------------------------

    #[Test]
    public function the_entity_form_of_delete_is_gated_and_the_by_key_form_forgets_the_holder(): void
    {
        $held = $this->db->get(User::class, 1);
        $copy = new User();
        $copy->id = 1;

        self::assertSame('UOW-002', $this->code(fn () => $this->db->delete(User::class, $copy)));
        self::assertTrue($this->db->isTracked($held));

        $second = $this->db->get(User::class, 2);
        $this->db->delete(User::class, 2);

        self::assertFalse($this->db->isTracked($second));
    }

    #[Test]
    public function a_second_instance_for_a_key_held_by_a_loaded_link_row_is_uow_002(): void
    {
        $user = $this->db->get(User::class, 1);
        $this->db->loadEach([$user], 'roles');

        $copy = new UserRole();
        $copy->userId = 1;
        $copy->roleId = 1;
        $copy->createdAtUtc = SampleDatabase::seedTime();

        // The caller never received the link rows, yet the session tracks them like every keyed row.
        self::assertSame('UOW-002', $this->code(fn () => $this->db->update($copy)));
    }

    // --- the column-list update and the order of refusals --------------------------------

    #[Test]
    public function update_only_moves_the_written_columns_and_the_version_but_not_an_unwritten_change(): void
    {
        $transaction = $this->db->get(Transaction::class, 1);
        $transaction->note = 'checked';
        $transaction->amount = Decimal::of('5');

        $this->db->updateOnly($transaction, ['note']);

        self::assertSame(1, $transaction->version);
        self::assertSame(['amount'], $this->db->changesOf($transaction));
    }

    #[Test]
    public function update_only_of_an_unknown_instance_writes_but_does_not_register_it(): void
    {
        $stranger = new User();
        $stranger->id = 2;
        $stranger->name = 'Grace Hopper';

        $this->db->updateOnly($stranger, ['name']);

        self::assertFalse($this->db->isTracked($stranger));
        self::assertSame('Grace Hopper', $this->session()->get(User::class, 2)->name);
    }

    #[Test]
    public function update_only_refuses_the_list_first_then_the_navigation_then_the_identity(): void
    {
        $this->db->get(User::class, 1);
        $this->db->get(Transaction::class, 1);

        $foreign = new User();
        $foreign->id = 1;
        self::assertSame('CRUD-005', $this->code(fn () => $this->db->updateOnly($foreign, ['nope'])));
        self::assertSame('UOW-002', $this->code(fn () => $this->db->updateOnly($foreign, ['name'])));

        $foreignTransaction = new Transaction();
        $foreignTransaction->id = 1;
        $foreignTransaction->userId = 1;
        $this->db->load($foreignTransaction, 'user');
        $foreignTransaction->userId = 2;
        self::assertSame('CRUD-004', $this->code(fn () => $this->db->updateOnly($foreignTransaction, ['userId'])));
    }

    #[Test]
    public function a_full_update_checks_every_navigation_before_the_identity_gate_and_the_snapshot(): void
    {
        $grace = $this->db->get(User::class, 2);
        $transaction = $this->db->get(Transaction::class, 1);
        $this->db->load($transaction, 'user');
        self::assertSame(1, $transaction->user?->id);

        // Clean, but the navigation disagrees with the FK: refused, never silently skipped.
        (new \ReflectionProperty(Transaction::class, 'user'))->setValue($transaction, $grace);
        self::assertSame('CRUD-004', $this->code(fn () => $this->db->update($transaction)));

        // A foreign instance with the same inconsistency: CRUD-004 before UOW-002.
        $foreign = new Transaction();
        $foreign->id = 1;
        $foreign->userId = 1;
        (new \ReflectionProperty(Transaction::class, 'user'))->setValue($foreign, $grace);
        self::assertSame('CRUD-004', $this->code(fn () => $this->db->update($foreign)));
    }

    // --- reading ---------------------------------------------------------------------------

    #[Test]
    public function a_re_read_of_a_tracked_key_still_maps_the_row_so_its_errors_still_fire(): void
    {
        $user = $this->db->get(User::class, 2);
        $this->corruptUserTwo();

        self::assertSame('VAL-020', $this->code(fn () => $this->db->get(User::class, 2)));
        self::assertTrue($this->db->isTracked($user));
        self::assertFalse($this->db->isDirty($user));
    }

    #[Test]
    public function a_read_that_fails_midway_leaves_the_rows_it_resolved_registered(): void
    {
        $this->corruptUserTwo();
        $columns = 'id, name, email, display_name, created_at, updated_at';
        $query = Query::inline(EmptyArgs::class, User::class, "select {$columns} from users order by id");

        $first = null;
        try {
            foreach ($this->db->stream($query, EmptyArgs::value()) as $user) {
                $first ??= $user;
            }
            self::fail('expected the second row to fail');
        } catch (SimpleOrmException $e) {
            self::assertSame('VAL-020', $e->errorCode);
        }

        self::assertNotNull($first);
        self::assertTrue($this->db->isTracked($first));
        self::assertSame($first, $this->db->get(User::class, 1));
    }

    #[Test]
    public function loading_correlates_by_the_live_foreign_key_of_a_tracked_instance(): void
    {
        $user = $this->db->get(User::class, 1);
        $transaction = $this->db->get(Transaction::class, 1);
        $transaction->userId = 2;

        $this->db->load($user, 'transactions');

        self::assertSame([], $user->transactions);
        self::assertSame(['userId'], $this->db->changesOf($transaction));
    }

    /** A stored `created_at` without a UTC marker: every read of the row is `VAL-020`. */
    private function corruptUserTwo(): void
    {
        $this->session()->execute(
            Command::inline(EmptyArgs::class, "update users set created_at = '2026-08-28 09:30:00' where id = 2"),
            EmptyArgs::value(),
        );
    }
}
