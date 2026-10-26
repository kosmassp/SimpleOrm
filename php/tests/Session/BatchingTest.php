<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Session;

use InvalidArgumentException;
use PDOException;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use SimpleOrm\Dialect\SqliteDialect;
use SimpleOrm\Errors\SimpleOrmException;
use SimpleOrm\Session\BatchingMode;
use SimpleOrm\Session\Db;
use SimpleOrm\Session\DbOptions;
use SimpleOrm\Session\TrackingMode;
use SimpleOrm\Tests\Sample\Models\Role;
use SimpleOrm\Tests\Sample\Models\Transaction;
use SimpleOrm\Tests\Sample\Models\UserTransactionTotal;
use SimpleOrm\Tests\Support\ConformanceClock;
use SimpleOrm\Tests\Support\ConformanceSeed;
use SimpleOrm\Tests\Support\ObservedConnectionDialect;
use SimpleOrm\Tests\Support\SampleDatabase;
use SimpleOrm\Tests\Support\SetDisplayNameArgs;
use SimpleOrm\Tests\Support\TempDatabase;

/**
 * Statement batching (Level 4 item 3, ADR-0044, spec/session.md "Batching") on a real temp-file SQLite database seeded
 * from `conformance/fixtures/seed.json` — the PHP-specific half the shared `batch-cases`/`flush-cases` cannot pin: the
 * option and its argument error, that SQLite is sequential (`batchingInEffect()` false) yet reuses one prepared statement
 * per distinct text, the explicit batch's lifetime (`TX-003`), its transactions, the dialect's parameter cap in the plan,
 * and a failed batched flush. The grouping rules themselves are pinned data-driven by `Conformance\BatchCasesTest`.
 * A second, plain session on the same file observes what reached the database.
 */
final class BatchingTest extends TestCase
{
    private TempDatabase $fixture;

    private ObservedConnectionDialect $dialect;

    private ?Db $db = null;

    private ?Db $observer = null;

    /** @var list<int> the sizes of the messages the session reported (its test hook) */
    private array $messages = [];

    protected function setUp(): void
    {
        $this->fixture = TempDatabase::create();
    }

    protected function tearDown(): void
    {
        $this->observer?->close();
        $this->db?->close();
        $this->fixture->delete();
    }

    /** A fresh fixture database and no open session: for a test that opens more than one configuration. */
    private function reset(): void
    {
        $this->tearDown();
        $this->db = null;
        $this->observer = null;
        $this->messages = [];
        $this->setUp();
    }

    private function open(
        BatchingMode $batching = BatchingMode::Auto,
        TrackingMode $tracking = TrackingMode::Untracked,
        int $maxStatementsPerMessage = 100,
        ?int $parameterCap = null,
    ): Db {
        $this->dialect = new ObservedConnectionDialect(new SqliteDialect(), $parameterCap);
        $db = SampleDatabase::open($this->fixture, new DbOptions(
            $this->dialect,
            tracking: $tracking,
            clock: ConformanceClock::fixed(),
            batching: $batching,
            maxStatementsPerMessage: $maxStatementsPerMessage,
        ));
        ConformanceSeed::insert($this->fixture);
        $db->observeMessages(function (int $size): void {
            $this->messages[] = $size;
        });
        $this->observer = Db::open($this->fixture->connectionString(), new DbOptions(new SqliteDialect()));
        $this->db = $db;

        return $db;
    }

    private function pdo(): \SimpleOrm\Tests\Support\CountingPdo
    {
        return $this->dialect->connection ?? throw new \LogicException('open() first');
    }

    private static function role(string $name): Role
    {
        $role = new Role();
        $role->name = $name;

        return $role;
    }

    private function roleCount(): int
    {
        return count($this->observer?->queryAll(Role::class) ?? []);
    }

    private static function code(callable $action): string
    {
        try {
            $action();
        } catch (SimpleOrmException $e) {
            return $e->errorCode;
        }

        self::fail('expected a SimpleOrmException, none was thrown');
    }

    // --- the option -------------------------------------------------------------------------------

    #[Test]
    public function a_session_defaults_to_no_batching_and_a_hundred_statements_per_message(): void
    {
        $options = new DbOptions(new SqliteDialect());

        self::assertSame(BatchingMode::Off, $options->batching);
        self::assertSame(100, $options->maxStatementsPerMessage);
    }

    #[Test]
    public function a_message_size_below_one_is_an_argument_error_before_anything_opens(): void
    {
        $this->expectException(InvalidArgumentException::class);

        try {
            Db::open($this->fixture->connectionString(), new DbOptions(new SqliteDialect(), maxStatementsPerMessage: 0));
        } finally {
            self::assertFileDoesNotExist($this->fixture->path);   // a failed open leaves nothing behind
        }
    }

    #[Test]
    public function batching_is_not_in_effect_on_sqlite_whatever_the_option(): void
    {
        self::assertFalse($this->open(BatchingMode::Auto)->batchingInEffect());
        $this->reset();

        self::assertFalse($this->open(BatchingMode::Off)->batchingInEffect());
    }

    // --- the sequential realization ---------------------------------------------------------------

    #[Test]
    public function an_explicit_batch_under_auto_prepares_one_statement_per_distinct_text(): void
    {
        $db = $this->open();
        $this->pdo()->prepared = [];

        $batch = $db->batch();
        foreach (['a', 'b', 'c', 'd', 'e'] as $name) {
            $batch->insert(self::role($name));
        }

        $result = $batch->send();

        self::assertSame([1, 1, 1, 1, 1], $result->affected);
        self::assertSame(1, $result->messages);
        self::assertSame([5], $this->messages);
        self::assertSame(1, $this->pdo()->preparedStartingWith('insert into roles'));
        self::assertSame(8, $this->roleCount());
    }

    #[Test]
    public function the_same_batch_under_off_is_the_level_zero_to_three_session_one_statement_at_a_time(): void
    {
        $db = $this->open(BatchingMode::Off);
        $this->pdo()->prepared = [];

        $batch = $db->batch();
        foreach (['a', 'b', 'c'] as $name) {
            $batch->insert(self::role($name));
        }

        $result = $batch->send();

        self::assertSame([1, 1, 1], $result->affected);
        self::assertSame(3, $result->messages);
        self::assertSame([1, 1, 1], $this->messages);
        self::assertSame(3, $this->pdo()->preparedStartingWith('insert into roles'));
    }

    #[Test]
    public function a_flush_under_auto_reuses_the_prepared_statement_and_under_off_does_not(): void
    {
        foreach ([[BatchingMode::Auto, 1], [BatchingMode::Off, 3]] as [$mode, $expectedPrepares]) {
            $this->reset();
            $db = $this->open($mode, TrackingMode::Tracked);
            $this->pdo()->prepared = [];
            foreach (['a', 'b', 'c'] as $name) {
                $db->persist(self::role($name));
            }

            self::assertSame(3, $db->flush()->inserted);
            self::assertSame($expectedPrepares, $this->pdo()->preparedStartingWith('insert into roles'), $mode->value);
        }
    }

    // --- the explicit batch -----------------------------------------------------------------------

    #[Test]
    public function a_batch_is_spent_by_its_send_and_by_its_disposal(): void
    {
        $db = $this->open();

        $sent = $db->batch();
        $sent->insert(self::role('a'));
        $sent->send();
        self::assertSame('TX-003', self::code(static fn () => $sent->insert(self::role('b'))));
        self::assertSame('TX-003', self::code(static fn () => $sent->send()));

        $disposed = $db->batch();
        $disposed->insert(self::role('c'));
        $disposed->dispose();
        self::assertSame('TX-003', self::code(static fn () => $disposed->delete(Role::class, 1)));
        self::assertSame('TX-003', self::code(static fn () => $disposed->send()));
        self::assertSame(4, $this->roleCount(), 'a batch disposed unsent is dropped silently: only "a" was ever written');
    }

    #[Test]
    public function queuing_does_no_io_and_checks_nothing_but_the_send_refuses_what_the_immediate_call_would(): void
    {
        $db = $this->open();
        $this->pdo()->prepared = [];

        $batch = $db->batch();
        $batch->insert(new UserTransactionTotal());   // a view: read-only, CRUD-003 for the immediate insert too
        $batch->delete(Role::class, 'not a key of the right type');

        self::assertSame([], $this->pdo()->prepared, 'queuing does no I/O');
        self::assertSame('CRUD-003', self::code(static fn () => $batch->send()));
        self::assertSame('TX-003', self::code(static fn () => $batch->send()), 'a failed send spends the batch too');
    }

    #[Test]
    public function an_empty_send_begins_no_transaction_and_a_send_runs_in_one_of_its_own(): void
    {
        $db = $this->open();

        $empty = $db->batch()->send();
        self::assertSame([], $empty->affected);
        self::assertSame(0, $empty->messages);
        self::assertSame(0, $this->pdo()->begun);

        $batch = $db->batch();
        $batch->insert(self::role('a'));
        $batch->send();

        self::assertSame(1, $this->pdo()->begun);
        self::assertFalse($db->connection()->inTransaction(), 'the send committed the transaction it began');
        self::assertSame(4, $this->roleCount());
    }

    #[Test]
    public function a_send_inside_the_callers_transaction_enlists_in_it_and_leaves_it_open(): void
    {
        $db = $this->open();

        $scope = $db->begin();
        $batch = $db->batch();
        $batch->insert(self::role('a'));
        $batch->send();

        self::assertSame(1, $this->pdo()->begun, 'no second transaction');
        self::assertTrue($db->connection()->inTransaction());
        self::assertSame(3, $this->roleCount(), 'uncommitted work is invisible to another session');

        $scope->rollback();
        self::assertSame(3, $this->roleCount());
    }

    #[Test]
    public function a_failed_send_inside_the_callers_transaction_leaves_the_rows_that_ran_for_the_caller_to_roll_back(): void
    {
        $db = $this->open();
        $role = self::role('a');
        $stale = $db->get(Transaction::class, 1);
        $fresh = $db->get(Transaction::class, 1);
        $fresh->note = 'first';
        $db->updateOnly($fresh, ['note']);   // the row is at version 1; $stale still says 0
        $stale->note = 'second';

        $scope = $db->begin();
        $batch = $db->batch();
        $batch->insert($role);
        $batch->updateOnly($stale, ['note']);

        self::assertSame('CRUD-010', self::code(static fn () => $batch->send()));
        self::assertTrue($db->connection()->inTransaction(), 'the caller\'s transaction is the caller\'s to end');
        self::assertFalse(isset($role->id), 'the insert\'s generated key was undone in memory');
        // The sequential realization stops at the failure, but the insert before it had run: its row is in the
        // caller's transaction until the caller rolls it back (spec/session.md "Failure").
        self::assertSame(4, (int) $db->connection()->query('select count(*) from roles')->fetchColumn());

        $scope->rollback();
        self::assertSame(3, $this->roleCount());
    }

    #[Test]
    public function a_registry_command_counts_its_rows_and_a_bad_one_fails_the_whole_send(): void
    {
        $db = $this->open();
        $command = SetDisplayNameArgs::command();

        $batch = $db->batch();
        $batch->execute($command, new SetDisplayNameArgs(1, 'Ada'));
        $batch->execute($command, new SetDisplayNameArgs(999, 'Nobody'));
        $result = $batch->send();

        self::assertSame([1, 0], $result->affected);
        self::assertSame([2], $this->messages);
        self::assertSame('Ada', $db->get(\SimpleOrm\Tests\Sample\Models\User::class, 1)->displayName);

        $role = self::role('a');
        $failing = $db->batch();
        $failing->insert($role);
        $failing->execute($command, new class () {
            public int $id = 1;   // no property for @DisplayName: PRM-001
        });

        self::assertSame('PRM-001', self::code(static fn () => $failing->send()));
        self::assertFalse(isset($role->id));
        self::assertSame(3, $this->roleCount());
    }

    // --- the plan: rule 1a's key clause, size and parameter caps ----------------------------------

    #[Test]
    public function two_instances_of_one_row_do_not_share_a_message_but_unset_keys_and_other_keys_do(): void
    {
        $db = $this->open();
        $first = $db->get(\SimpleOrm\Tests\Sample\Models\User::class, 1);
        $copy = $db->get(\SimpleOrm\Tests\Sample\Models\User::class, 1);
        $other = $db->get(\SimpleOrm\Tests\Sample\Models\User::class, 2);
        $first->displayName = 'one';
        $copy->displayName = 'two';
        $other->displayName = 'three';

        $batch = $db->batch();
        $batch->updateOnly($first, ['displayName']);
        $batch->updateOnly($other, ['displayName']);
        $batch->insert(self::role('a'));   // an unset generated key is no key
        $batch->insert(self::role('b'));
        $batch->updateOnly($copy, ['displayName']);   // the same type and key as $first: it waits for the message holding it

        $batch->send();

        self::assertSame([4, 1], $this->messages);
        self::assertSame('two', $this->observer?->get(\SimpleOrm\Tests\Sample\Models\User::class, 1)->displayName);
    }

    #[Test]
    public function a_delete_by_key_compares_its_key_argument(): void
    {
        $db = $this->open();
        $role = $db->get(Role::class, 2);
        $role->name = 'renamed';

        $batch = $db->batch();
        $batch->delete(Role::class, 2);   // the soft delete: a flag-setting update of key 2
        $batch->updateOnly($role, ['name']);   // another instance of the same row

        $batch->send();

        self::assertSame([1, 1], $this->messages);
    }

    // --- the plan: size and parameter caps --------------------------------------------------------

    #[Test]
    public function the_message_size_cuts_the_plan(): void
    {
        $db = $this->open(maxStatementsPerMessage: 2);

        $batch = $db->batch();
        foreach (['a', 'b', 'c', 'd', 'e'] as $name) {
            $batch->insert(self::role($name));
        }

        self::assertSame(3, $batch->send()->messages);
        self::assertSame([2, 2, 1], $this->messages);
    }

    #[Test]
    public function the_dialects_parameter_cap_cuts_the_plan_and_a_statement_over_it_is_a_message_of_its_own(): void
    {
        // A role insert binds four parameters (name, is_deleted, created_at, updated_at).
        $db = $this->open(parameterCap: 9);
        $batch = $db->batch();
        foreach (['a', 'b', 'c', 'd', 'e'] as $name) {
            $batch->insert(self::role($name));
        }

        $batch->send();
        self::assertSame([2, 2, 1], $this->messages, 'two statements fit in nine parameters, a third would take twelve');
        $this->reset();

        $db = $this->open(parameterCap: 3);
        $batch = $db->batch();
        foreach (['a', 'b', 'c'] as $name) {
            $batch->insert(self::role($name));
        }

        $batch->send();
        self::assertSame([1, 1, 1], $this->messages, 'a statement alone over the cap is a message of its own');
    }

    // --- failure ----------------------------------------------------------------------------------

    #[Test]
    public function a_batched_flush_that_fails_undoes_its_effects_keeps_the_schedule_and_flushes_again_once_fixed(): void
    {
        $db = $this->open(BatchingMode::Auto, TrackingMode::Tracked);
        $first = self::role('reader');
        $second = self::role('reader');   // the unique index on roles.name refuses the second insert
        $db->persist($first);
        $db->persist($second);

        $failure = null;
        try {
            $db->flush();
        } catch (PDOException $e) {
            $failure = $e;
        }

        self::assertNotNull($failure, 'a provider error propagates unchanged');
        self::assertFalse(isset($first->id), 'the key the first insert wrote is undone');
        self::assertFalse($db->isTracked($first));
        self::assertTrue($db->hasPendingWork(), 'the schedule is kept');
        self::assertSame(3, $this->roleCount(), 'the flush\'s own transaction rolled back');

        $second->name = 'writer';
        $summary = $db->flush();

        self::assertSame(2, $summary->inserted);
        self::assertSame(5, $this->roleCount());
    }
}
