<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Session;

use PDOException;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use ReflectionProperty;
use SimpleOrm\Dialect\SqliteDialect;
use SimpleOrm\Errors\MappingException;
use SimpleOrm\Errors\SimpleOrmException;
use SimpleOrm\Metadata\Attributes\Cascade;
use SimpleOrm\Metadata\Attributes\Column;
use SimpleOrm\Metadata\Attributes\ForeignKey;
use SimpleOrm\Metadata\Attributes\Generated;
use SimpleOrm\Metadata\Attributes\Ignore;
use SimpleOrm\Metadata\Attributes\Key;
use SimpleOrm\Metadata\Attributes\ManyToMany;
use SimpleOrm\Metadata\Attributes\ManyToOne;
use SimpleOrm\Metadata\Attributes\OneToOne;
use SimpleOrm\Metadata\Attributes\Owned;
use SimpleOrm\Metadata\Attributes\Table;
use SimpleOrm\Metadata\CascadeOperation;
use SimpleOrm\Metadata\EntityMapJson;
use SimpleOrm\Metadata\EntityMapLoader;
use SimpleOrm\Session\Db;
use SimpleOrm\Session\DbOptions;
use SimpleOrm\Session\TrackingMode;
use SimpleOrm\Tests\Sample\Models\Address;
use SimpleOrm\Tests\Sample\Models\Role;
use SimpleOrm\Tests\Sample\Models\Transaction;
use SimpleOrm\Tests\Sample\Models\TransactionDetail;
use SimpleOrm\Tests\Sample\Models\User;
use SimpleOrm\Tests\Sample\Models\UserRole;
use SimpleOrm\Tests\Sample\Models\UserTransactionTotal;
use SimpleOrm\Tests\Support\ConformanceSeed;
use SimpleOrm\Tests\Support\RecordingUpdateDialect;
use SimpleOrm\Tests\Support\SampleDatabase;
use SimpleOrm\Tests\Support\TempDatabase;
use SimpleOrm\Types\Decimal;

/**
 * The unit of work (Level 3 M2, ADR-0034 §7c, spec/unit-of-work.md) on a real temp-file SQLite
 * database seeded from `conformance/fixtures/seed.json`: cascade declarations (`MAP-025`, the
 * export), cycles (`UOW-004`), a failed flush keeping its schedule, cancelled inserts, one-to-one
 * cascades, and the immediate operations beside the unit of work. The shared behaviors are pinned
 * data-driven by `Conformance\FlushCasesTest`. A second, untracked session on the same file
 * observes what reached the database.
 */
final class UnitOfWorkTest extends TestCase
{
    private TempDatabase $fixture;

    private RecordingUpdateDialect $recorder;

    private Db $db;

    private Db $observer;

    protected function setUp(): void
    {
        $this->fixture = TempDatabase::create();
        $this->recorder = new RecordingUpdateDialect(new SqliteDialect());
        $this->db = SampleDatabase::open($this->fixture, new DbOptions($this->recorder, tracking: TrackingMode::Tracked));
        $this->db->createTable(Node::class);
        $this->db->createTable(Account::class);
        $this->db->createTable(AccountSettings::class);
        $this->db->createTable(Item::class);
        $this->db->createTable(CycleA::class);
        $this->db->createTable(CycleB::class);
        ConformanceSeed::insert($this->fixture);
        $this->observer = Db::open($this->fixture->connectionString(), new DbOptions(new SqliteDialect()));
    }

    protected function tearDown(): void
    {
        $this->observer->close();
        $this->db->close();
        $this->fixture->delete();
    }

    /** @param class-string $entityType */
    private function rowsOf(string $entityType): int
    {
        return count($this->observer->queryAll($entityType));
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

    /** Assigns a `private(set)` navigation the way the library does: through reflection. */
    private static function navigate(object $owner, string $navigation, mixed $value): void
    {
        (new ReflectionProperty($owner::class, $navigation))->setValue($owner, $value);
    }

    private static function newUser(string $name, string $email): User
    {
        $user = new User();
        $user->name = $name;
        $user->email = $email;
        $user->createdAtUtc = SampleDatabase::seedTime();

        return $user;
    }

    private static function newDetail(string $description): TransactionDetail
    {
        $detail = new TransactionDetail();
        $detail->description = $description;
        $detail->quantity = 1;
        $detail->unitPrice = Decimal::of('2.50');
        $detail->createdAtUtc = SampleDatabase::seedTime();

        return $detail;
    }

    private static function newTransaction(): Transaction
    {
        $transaction = new Transaction();
        $transaction->userId = 1;
        $transaction->amount = Decimal::of('5');
        $transaction->createdAtUtc = SampleDatabase::seedTime();

        return $transaction;
    }

    private static function newNode(string $label): Node
    {
        $node = new Node();
        $node->label = $label;

        return $node;
    }

    // --- cascade declarations (MAP-025) and the export ----------------------------------------

    #[Test]
    public function a_cascade_on_a_column_is_map025(): void
    {
        self::assertContains('MAP-025', $this->codesOf(Map025ColumnFixture::class));
    }

    #[Test]
    public function a_cascade_on_a_many_to_many_navigation_is_map025(): void
    {
        self::assertContains('MAP-025', $this->codesOf(Map025ManyToManyFixture::class));
    }

    /** @return list<string> */
    private function codesOf(string $entityType): array
    {
        try {
            (new EntityMapLoader())->load($entityType);
            self::fail("{$entityType} was expected to fail loading");
        } catch (MappingException $e) {
            return array_map(static fn ($error) => $error->code, $e->errors);
        }
    }

    #[Test]
    public function the_export_carries_a_cascade_only_where_one_is_declared(): void
    {
        $loader = new EntityMapLoader();
        $account = json_decode(EntityMapJson::export($loader->load(Account::class), $loader), true, flags: JSON_THROW_ON_ERROR);
        $settings = json_decode(EntityMapJson::export($loader->load(AccountSettings::class), $loader), true, flags: JSON_THROW_ON_ERROR);

        self::assertSame(['persist', 'remove'], $account['relationships'][0]['cascade']);
        self::assertArrayNotHasKey('cascade', $settings['relationships'][0]);
    }

    // --- cycles (UOW-004) ----------------------------------------------------------------------

    #[Test]
    public function a_cycle_among_scheduled_inserts_is_uow_004_before_any_sql_and_keeps_the_schedule(): void
    {
        $a = self::newNode('a');
        $b = self::newNode('b');
        self::navigate($a, 'parent', $b);
        self::navigate($b, 'parent', $a);
        $this->db->persist($a);
        $this->db->persist($b);
        $this->recorder->statements = [];

        self::assertSame('UOW-004', $this->code(fn () => $this->db->flush()));
        self::assertSame([], $this->recorder->statements);
        self::assertTrue($this->db->hasPendingWork());
        self::assertSame(0, $this->rowsOf(Node::class));

        $this->db->remove($a);
        $this->db->remove($b);
        self::assertFalse($this->db->hasPendingWork());
    }

    #[Test]
    public function the_same_chain_without_the_cycle_inserts_the_parent_first_and_flows_its_key(): void
    {
        $root = self::newNode('root');
        $middle = self::newNode('middle');
        $leaf = self::newNode('leaf');
        self::navigate($leaf, 'parent', $middle);
        self::navigate($middle, 'parent', $root);

        $this->db->persist($leaf);
        $this->db->persist($middle);
        $this->db->persist($root);
        $summary = $this->db->flush();

        self::assertSame(3, $summary->inserted);
        self::assertLessThan($middle->id, $root->id);
        self::assertLessThan($leaf->id, $middle->id);
        self::assertSame($root->id, $middle->parentId);
        self::assertSame($middle->id, $leaf->parentId);
        self::assertSame(3, $this->rowsOf(Node::class));
    }

    // --- failure: the flush owns its transaction ---------------------------------------------------

    #[Test]
    public function a_failed_flush_applies_nothing_keeps_the_schedule_and_the_rest_flushes_once_the_bad_insert_is_cancelled(): void
    {
        $good = self::newUser('Linus', 'linus@example.com');
        $duplicate = self::newUser('Impostor', 'ada@example.com');   // users.email is unique
        $this->db->persist($good);
        $this->db->persist($duplicate);

        try {
            $this->db->flush();
            self::fail('the unique index on users.email should refuse the second insert');
        } catch (PDOException) {
            // the provider's own exception propagates (CODING-STANDARD §4)
        }

        self::assertFalse($this->db->connection()->inTransaction());
        self::assertSame(2, $this->rowsOf(User::class));
        self::assertTrue($this->db->hasPendingWork());

        $this->db->remove($duplicate);
        $summary = $this->db->flush();

        self::assertSame(1, $summary->inserted);
        self::assertFalse($this->db->hasPendingWork());
        self::assertSame(3, $this->rowsOf(User::class));
    }

    #[Test]
    public function a_failed_flush_inside_the_callers_transaction_leaves_that_transaction_open(): void
    {
        $scope = $this->db->begin();
        $this->db->persist(self::newUser('Impostor', 'ada@example.com'));

        try {
            $this->db->flush();
            self::fail('the unique index on users.email should refuse the insert');
        } catch (PDOException) {
            // expected
        }

        self::assertTrue($this->db->connection()->inTransaction());
        self::assertTrue($this->db->hasPendingWork());
        $scope->rollback();
    }

    // --- cascades: persist ------------------------------------------------------------------------

    #[Test]
    public function removing_a_scheduled_parent_cancels_the_inserts_its_cascade_scheduled(): void
    {
        $transaction = self::newTransaction();
        self::navigate($transaction, 'details', [self::newDetail('Widget'), self::newDetail('Gadget')]);

        $this->db->persist($transaction);
        self::assertTrue($this->db->hasPendingWork());

        $this->db->remove($transaction);
        $summary = $this->db->flush();

        self::assertFalse($this->db->hasPendingWork());
        self::assertSame([0, 0, 0], [$summary->inserted, $summary->updated, $summary->deleted]);
        self::assertSame(1, $this->rowsOf(Transaction::class));
        self::assertSame(0, $this->rowsOf(TransactionDetail::class));
    }

    #[Test]
    public function a_child_the_user_persisted_directly_survives_its_cancelled_parent(): void
    {
        $detail = self::newDetail('Widget');
        $transaction = self::newTransaction();
        self::navigate($transaction, 'details', [$detail]);

        $this->db->persist($detail);
        $this->db->persist($transaction);
        $this->db->remove($transaction);

        self::assertTrue($this->db->hasPendingWork());   // the detail is still scheduled
        $this->db->remove($detail);
        self::assertFalse($this->db->hasPendingWork());
    }

    #[Test]
    public function a_refusal_anywhere_in_a_cascade_schedules_nothing(): void
    {
        $transaction = $this->db->get(Transaction::class, 1);
        // A loaded collection holding an instance the session never saw: the cascaded remove of it is UOW-001.
        self::navigate($transaction, 'details', [new TransactionDetail()]);

        self::assertSame('UOW-001', $this->code(fn () => $this->db->remove($transaction)));
        self::assertFalse($this->db->hasPendingWork());
    }

    // --- cascades: one-to-one -------------------------------------------------------------------------

    #[Test]
    public function a_one_to_one_cascade_persists_and_removes_the_single_target(): void
    {
        $account = new Account();
        $account->name = 'Ada';
        $settings = new AccountSettings();
        $settings->theme = 'dark';
        self::navigate($account, 'settings', $settings);

        $this->db->persist($account);
        $inserted = $this->db->flush();

        self::assertSame(2, $inserted->inserted);
        self::assertSame($account->id, $settings->accountId);
        self::assertTrue($this->db->isTracked($settings));

        $this->db->remove($account);
        $this->recorder->statements = [];
        $deleted = $this->db->flush();

        self::assertSame(2, $deleted->deleted);
        self::assertSame(
            [['op' => 'delete', 'entity' => 'AccountSettings'], ['op' => 'delete', 'entity' => 'Account']],
            $this->recorder->statements,
        );
        self::assertSame(0, $this->rowsOf(Account::class));
        self::assertSame(0, $this->rowsOf(AccountSettings::class));
    }

    #[Test]
    public function a_null_singular_navigation_cascades_nothing(): void
    {
        $account = new Account();
        $account->name = 'Grace';
        $this->db->persist($account);
        self::assertSame(1, $this->db->flush()->inserted);

        $this->db->remove($account);
        $summary = $this->db->flush();

        self::assertSame(1, $summary->deleted);
        self::assertSame(0, $this->rowsOf(Account::class));
    }

    // --- the Clarifications, pinned (ADR-0036) ---------------------------------------------------------

    /** @return list<string> every write the dialect rendered since the last reset, as `op Entity` */
    private function ops(): array
    {
        return array_map(
            static fn (array $statement): string => $statement['op'] . ' ' . $statement['entity'],
            $this->recorder->statements,
        );
    }

    #[Test]
    public function a_cascade_naming_no_operation_is_map025(): void
    {
        self::assertContains('MAP-025', $this->codesOf(Map025EmptyFixture::class));
    }

    #[Test]
    public function a_cascade_on_an_ignored_property_an_owned_navigation_or_an_owned_member_is_map025(): void
    {
        self::assertContains('MAP-025', $this->codesOf(Map025IgnoredFixture::class));
        self::assertContains('MAP-025', $this->codesOf(Map025OwnedNavigationFixture::class));
        self::assertContains('MAP-025', $this->codesOf(Map025OwnedMemberFixture::class));
    }

    #[Test]
    public function has_pending_work_on_an_untracked_session_is_false_never_uow_003(): void
    {
        self::assertFalse($this->observer->hasPendingWork());
    }

    #[Test]
    public function the_instances_own_shape_is_refused_before_anything_else(): void
    {
        $row = $this->db->queryAll(UserTransactionTotal::class)[0];

        self::assertSame('CRUD-003', $this->code(fn () => $this->db->persist($row)));
        self::assertSame('CRUD-003', $this->code(fn () => $this->db->remove($row)));
        self::assertSame('CRUD-003', $this->code(fn () => $this->db->remove(new UserTransactionTotal())));
        self::assertFalse($this->db->hasPendingWork());
    }

    #[Test]
    public function a_dirty_tracked_read_only_instance_refuses_crud003_at_flush_and_the_flush_is_undone(): void
    {
        $row = $this->db->queryAll(UserTransactionTotal::class)[0];
        $row->userName = 'changed';
        $fresh = self::newUser('Linus', 'linus@example.com');
        $this->db->persist($fresh);

        self::assertSame('CRUD-003', $this->code(fn () => $this->db->flush()));

        self::assertFalse($this->db->isTracked($fresh));
        self::assertTrue($this->db->hasPendingWork());
        self::assertTrue($this->db->isDirty($row));
        self::assertSame(2, $this->rowsOf(User::class));
    }

    // --- a failed flush undoes its in-memory effects --------------------------------------------------

    #[Test]
    public function a_failed_flush_puts_inserts_updates_and_the_schedule_back_as_they_were(): void
    {
        $ada = $this->db->get(User::class, 1);
        $fine = self::newUser('Linus', 'linus@example.com');
        $duplicate = self::newUser('Impostor', 'ada@example.com');   // users.email is unique
        $this->db->persist($fine);
        $ada->name = 'Ada Lovelace';
        $this->db->persist($duplicate);

        try {
            $this->db->flush();
            self::fail('the unique index on users.email should refuse the second insert');
        } catch (PDOException) {
            // the provider's own exception propagates (CODING-STANDARD §4)
        }

        self::assertFalse($this->db->isTracked($fine));
        self::assertFalse((new ReflectionProperty(User::class, 'id'))->isInitialized($fine), 'the generated key is reset to unset');
        self::assertSame(['name'], $this->db->changesOf($ada));
        self::assertSame(2, $this->rowsOf(User::class));

        $this->db->remove($duplicate);
        $summary = $this->db->flush();

        self::assertSame([1, 1, 0], [$summary->inserted, $summary->updated, $summary->deleted]);
        self::assertTrue($this->db->isTracked($fine));
        self::assertSame(3, $this->rowsOf(User::class));
    }

    #[Test]
    public function a_failed_flush_undoes_an_update_and_a_delete_that_ran(): void
    {
        $stale = $this->db->get(Transaction::class, 1);
        $this->db->load($stale, 'details');
        // Another session bumps the row's version: the tracked copy is stale.
        $elsewhere = $this->observer->get(Transaction::class, 1);
        $elsewhere->note = 'elsewhere';
        $this->observer->update($elsewhere);

        $mine = self::newTransaction();
        $this->db->insert($mine);
        $mine->note = 'local';
        $ada = $this->db->get(User::class, 1);
        $ada->name = 'Ada Lovelace';
        $this->db->remove($stale);

        self::assertSame('CRUD-010', $this->code(fn () => $this->db->flush()), 'a delete is version-checked');

        self::assertSame(0, $mine->version, 'the version an update bumped is put back');
        self::assertSame(['note'], $this->db->changesOf($mine));
        self::assertSame(['name'], $this->db->changesOf($ada));
        self::assertTrue($this->db->isTracked($stale), 'the delete that failed leaves its instance tracked');
        self::assertTrue($this->db->hasPendingWork());
        self::assertSame('Ada', $this->observer->get(User::class, 1)->name);
    }

    // --- cancelling a scheduled insert -----------------------------------------------------------------

    #[Test]
    public function a_child_reached_by_two_scheduled_owners_survives_the_cancellation_of_one(): void
    {
        $shared = self::newDetail('shared');
        $first = self::newTransaction();
        $second = self::newTransaction();
        self::navigate($first, 'details', [$shared]);
        self::navigate($second, 'details', [$shared]);
        $this->db->persist($first);
        $this->db->persist($second);

        $this->db->remove($first);
        $summary = $this->db->flush();

        self::assertSame(2, $summary->inserted);
        self::assertFalse($this->db->isTracked($first));
        self::assertSame($second->id, $shared->transactionId);

        // With every owner cancelled the child goes too.
        $orphan = self::newDetail('orphan');
        $a = self::newTransaction();
        $b = self::newTransaction();
        self::navigate($a, 'details', [$orphan]);
        self::navigate($b, 'details', [$orphan]);
        $this->db->persist($a);
        $this->db->persist($b);
        $this->db->remove($a);
        $this->db->remove($b);

        self::assertFalse($this->db->hasPendingWork());
    }

    #[Test]
    public function cancelling_an_owner_cancels_the_parent_its_many_to_one_cascade_scheduled(): void
    {
        $item = new Item();
        self::navigate($item, 'node', self::newNode('cascaded'));
        $this->db->persist($item);
        $this->db->remove($item);

        self::assertFalse($this->db->hasPendingWork());

        $kept = self::newNode('kept');
        $other = new Item();
        self::navigate($other, 'node', $kept);
        $this->db->persist($kept);   // persisted by name: it stays
        $this->db->persist($other);
        $this->db->remove($other);
        $summary = $this->db->flush();

        self::assertSame(1, $summary->inserted);
        self::assertTrue($this->db->isTracked($kept));
        self::assertFalse($this->db->isTracked($other));
    }

    #[Test]
    public function persisting_a_tracked_owner_still_cascades_to_a_new_child(): void
    {
        $transaction = $this->db->get(Transaction::class, 1);
        $detail = self::newDetail('Widget');
        self::navigate($transaction, 'details', [$detail]);

        $this->db->persist($transaction);
        $summary = $this->db->flush();

        self::assertSame(1, $summary->inserted);
        self::assertSame(1, $detail->transactionId);
        self::assertTrue($this->db->isTracked($detail));
    }

    #[Test]
    public function removing_an_instance_already_scheduled_for_removal_is_a_no_op(): void
    {
        $transaction = $this->db->get(Transaction::class, 1);
        $this->db->load($transaction, 'details');
        $this->db->remove($transaction);

        // A stranger now sits in the loaded collection; a second remove does not walk the cascade again.
        self::navigate($transaction, 'details', [self::newDetail('stranger')]);
        $this->db->remove($transaction);
        $summary = $this->db->flush();

        self::assertSame(1, $summary->deleted);
    }

    // --- work done immediately, and the empty flush ----------------------------------------------------------

    #[Test]
    public function work_done_immediately_is_dropped_not_done_twice(): void
    {
        $scheduled = self::newUser('Linus', 'linus@example.com');
        $this->db->persist($scheduled);
        $this->db->insert($scheduled);
        $removed = $this->db->get(User::class, 2);
        $this->db->remove($removed);
        $this->db->delete(User::class, $removed);

        $this->recorder->statements = [];
        $summary = $this->db->flush();

        self::assertSame([0, 0, 0], [$summary->inserted, $summary->updated, $summary->deleted]);
        self::assertSame([], $this->recorder->statements);
        self::assertFalse($this->db->hasPendingWork());
        self::assertSame(2, $this->rowsOf(User::class), 'Ada and Linus: Grace deleted, Linus written once');
    }

    #[Test]
    public function an_empty_flush_is_the_zero_summary_and_renders_nothing(): void
    {
        $this->recorder->statements = [];
        $summary = $this->db->flush();

        self::assertSame([0, 0, 0], [$summary->inserted, $summary->updated, $summary->deleted]);
        self::assertSame([], $this->recorder->statements);
        self::assertFalse($this->db->connection()->inTransaction());
    }

    // --- order -------------------------------------------------------------------------------------------------

    #[Test]
    public function types_in_a_dependency_cycle_share_a_rank_so_the_schedule_decides(): void
    {
        foreach ([[CycleA::class, CycleB::class], [CycleB::class, CycleA::class]] as $order) {
            $instances = [CycleA::class => new CycleA(), CycleB::class => new CycleB()];
            foreach ($order as $type) {
                $this->db->persist($instances[$type]);
            }

            $this->recorder->statements = [];
            self::assertSame(2, $this->db->flush()->inserted);

            $short = array_map(static fn (string $type): string => 'insert ' . substr($type, strrpos($type, '\\') + 1), $order);
            self::assertSame($short, $this->ops(), 'unrelated instances of two types in a cycle insert in schedule order');
        }
    }

    #[Test]
    public function updates_follow_the_latest_registration_and_count_statements_not_instances(): void
    {
        $ada = $this->db->get(User::class, 1);
        $grace = $this->db->get(User::class, 2);
        // Ada is deleted and inserted again: her registration is now the latest.
        $this->db->delete(User::class, $ada);
        $this->db->insert($ada);
        $grace->name = 'Grace Hopper';
        $ada->email = 'ada@new.example.com';

        $this->recorder->statements = [];
        $summary = $this->db->flush();

        self::assertSame(2, $summary->updated);
        self::assertSame(
            [
                ['op' => 'update', 'entity' => 'User', 'columns' => ['name']],
                ['op' => 'update', 'entity' => 'User', 'columns' => ['email']],
            ],
            $this->recorder->statements,
            'Grace (name) before Ada (email): registration order is the latest registration',
        );

        // A difference in the version alone is clean: no statement, nothing counted.
        $transaction = $this->db->get(Transaction::class, 1);
        $transaction->version += 5;
        $this->recorder->statements = [];

        self::assertSame(0, $this->db->flush()->updated);
        self::assertSame([], $this->recorder->statements);
    }

    // --- keys, edges, identity -------------------------------------------------------------------------------------

    #[Test]
    public function keys_flow_as_they_are_and_a_self_reference_is_not_a_cycle(): void
    {
        $node = self::newNode('n');
        $node->parentId = 99;
        self::navigate($node, 'parent', self::newNode('stranger'));   // untracked and unscheduled: its key is unset
        $this->db->persist($node);

        self::assertSame(1, $this->db->flush()->inserted);
        self::assertNull($node->parentId, 'a null key part is copied as it is, over the stale value');

        $loop = self::newNode('loop');
        self::navigate($loop, 'parent', $loop);
        $this->db->persist($loop);

        self::assertSame(1, $this->db->flush()->inserted);
    }

    #[Test]
    public function an_untracked_instance_whose_key_another_holds_is_uow_002_at_flush(): void
    {
        $held = $this->db->get(UserRole::class, [1, 1]);
        $second = new UserRole();
        $second->userId = 1;
        $second->roleId = 1;
        $this->db->persist($second);   // persist cannot tell; the insert's identity gate does

        self::assertSame('UOW-002', $this->code(fn () => $this->db->flush()));
        self::assertTrue($this->db->hasPendingWork());
        self::assertFalse($this->db->isTracked($second));
        self::assertTrue($this->db->isTracked($held));
    }

    // --- immediate operations beside the unit of work ----------------------------------------------------

    #[Test]
    public function persisting_a_tracked_instance_schedules_nothing(): void
    {
        $user = $this->db->get(User::class, 1);

        $this->db->persist($user);

        self::assertFalse($this->db->hasPendingWork());
    }

    #[Test]
    public function an_immediate_update_leaves_nothing_for_the_flush(): void
    {
        $user = $this->db->get(User::class, 1);
        $user->name = 'Ada Lovelace';
        $this->db->update($user);
        $this->recorder->statements = [];

        $summary = $this->db->flush();

        self::assertSame([0, 0, 0], [$summary->inserted, $summary->updated, $summary->deleted]);
        self::assertSame([], $this->recorder->statements);
    }

    #[Test]
    public function an_immediate_insert_beside_a_scheduled_one_is_not_flushed_twice(): void
    {
        $immediate = self::newUser('Linus', 'linus@example.com');
        $this->db->insert($immediate);
        $this->db->persist(self::newUser('Dennis', 'dennis@example.com'));

        $summary = $this->db->flush();

        self::assertSame(1, $summary->inserted);
        self::assertSame(4, $this->rowsOf(User::class));
    }

    #[Test]
    public function a_dirty_instance_flushes_as_a_column_list_update_after_the_inserts(): void
    {
        $user = $this->db->get(User::class, 1);
        $user->name = 'Ada Lovelace';
        $this->db->persist(self::newUser('Linus', 'linus@example.com'));
        $this->recorder->statements = [];

        $summary = $this->db->flush();

        self::assertSame([1, 1, 0], [$summary->inserted, $summary->updated, $summary->deleted]);
        self::assertSame(
            [
                ['op' => 'insert', 'entity' => 'User'],
                ['op' => 'update', 'entity' => 'User', 'columns' => ['name']],
            ],
            $this->recorder->statements,
        );
    }
}

/** A self-referencing entity: `parent` is a many-to-one to its own type, so two nodes can reference each other (`UOW-004`). */
#[Table('uow_nodes')]
final class Node
{
    #[Key]
    #[Generated]
    #[Column]
    public int $id;

    #[Column]
    public string $label;

    #[Column]
    #[ForeignKey(Node::class)]
    public ?int $parentId = null;

    #[ManyToOne('parentId')]
    public private(set) ?Node $parent = null;
}

/** The owner of a one-to-one that cascades both operations (the FK lives on {@see AccountSettings}). */
#[Table('uow_accounts')]
final class Account
{
    #[Key]
    #[Generated]
    #[Column]
    public int $id;

    #[Column]
    public string $name;

    #[OneToOne('accountId')]
    #[Cascade(CascadeOperation::Persist, CascadeOperation::Remove)]
    public private(set) ?AccountSettings $settings = null;
}

#[Table('uow_account_settings')]
final class AccountSettings
{
    #[Key]
    #[Generated]
    #[Column]
    public int $id;

    #[Column]
    #[ForeignKey(Account::class)]
    public int $accountId;

    #[Column]
    public string $theme;

    #[ManyToOne('accountId')]
    public private(set) ?Account $account = null;
}

#[Table('map025_columns')]
final class Map025ColumnFixture
{
    #[Key]
    #[Generated]
    #[Column]
    public int $id;

    // A column is not a navigation.
    #[Column]
    #[Cascade(CascadeOperation::Persist)]
    public string $name = '';
}

#[Table('map025_many_to_many')]
final class Map025ManyToManyFixture
{
    #[Key]
    #[Generated]
    #[Column]
    public int $id;

    // A many-to-many navigation cannot cascade: its link rows are persisted explicitly.
    #[ManyToMany(Role::class, through: UserRole::class)]
    #[Cascade(CascadeOperation::Persist)]
    public private(set) array $roles = [];
}

/** Persist-only cascade from a many-to-one: persisting the item schedules the node it references. */
#[Table('uow_items')]
final class Item
{
    #[Key]
    #[Generated]
    #[Column]
    public int $id;

    #[Column]
    #[ForeignKey(Node::class)]
    public ?int $nodeId = null;

    #[ManyToOne('nodeId')]
    #[Cascade(CascadeOperation::Persist)]
    public private(set) ?Node $node = null;
}

/** One of two types that reference each other through nullable many-to-one navigations: a metadata cycle (the type-rank fixture). */
#[Table('uow_cycle_a')]
final class CycleA
{
    #[Key]
    #[Generated]
    #[Column]
    public int $id;

    #[Column]
    #[ForeignKey(CycleB::class)]
    public ?int $peerId = null;

    #[ManyToOne('peerId')]
    public private(set) ?CycleB $peer = null;
}

#[Table('uow_cycle_b')]
final class CycleB
{
    #[Key]
    #[Generated]
    #[Column]
    public int $id;

    #[Column]
    #[ForeignKey(CycleA::class)]
    public ?int $peerId = null;

    #[ManyToOne('peerId')]
    public private(set) ?CycleA $peer = null;
}

#[Table('map025_empty')]
final class Map025EmptyFixture
{
    #[Key]
    #[Generated]
    #[Column]
    public int $id;

    // A declaration naming no operation.
    #[OneToOne('accountId')]
    #[Cascade()]
    public private(set) ?AccountSettings $settings = null;
}

#[Table('map025_ignored')]
final class Map025IgnoredFixture
{
    #[Key]
    #[Generated]
    #[Column]
    public int $id;

    // An ignored property is not a navigation either.
    #[Ignore]
    #[Cascade(CascadeOperation::Remove)]
    public string $scratch = '';
}

#[Table('map025_owned_navigation')]
final class Map025OwnedNavigationFixture
{
    #[Key]
    #[Generated]
    #[Column]
    public int $id;

    // An owned navigation is flattened into the owner's columns: nothing to cascade to.
    #[Owned]
    #[Cascade(CascadeOperation::Persist)]
    public ?Address $address = null;
}

/** An owned type whose member declares a cascade. */
#[Owned]
final class CascadingAddress
{
    #[Column]
    #[Cascade(CascadeOperation::Persist)]
    public string $city = '';
}

#[Table('map025_owned_member')]
final class Map025OwnedMemberFixture
{
    #[Key]
    #[Generated]
    #[Column]
    public int $id;

    #[Owned]
    public ?CascadingAddress $address = null;
}
