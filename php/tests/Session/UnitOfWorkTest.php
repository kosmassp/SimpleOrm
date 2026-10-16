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
use SimpleOrm\Metadata\Attributes\Key;
use SimpleOrm\Metadata\Attributes\ManyToMany;
use SimpleOrm\Metadata\Attributes\ManyToOne;
use SimpleOrm\Metadata\Attributes\OneToOne;
use SimpleOrm\Metadata\Attributes\Table;
use SimpleOrm\Metadata\CascadeOperation;
use SimpleOrm\Metadata\EntityMapJson;
use SimpleOrm\Metadata\EntityMapLoader;
use SimpleOrm\Session\Db;
use SimpleOrm\Session\DbOptions;
use SimpleOrm\Session\TrackingMode;
use SimpleOrm\Tests\Sample\Models\Role;
use SimpleOrm\Tests\Sample\Models\Transaction;
use SimpleOrm\Tests\Sample\Models\TransactionDetail;
use SimpleOrm\Tests\Sample\Models\User;
use SimpleOrm\Tests\Sample\Models\UserRole;
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

    // --- the unit of work needs tracking ------------------------------------------------------

    #[Test]
    public function has_pending_work_on_an_untracked_session_is_uow_003(): void
    {
        self::assertSame('UOW-003', $this->code(fn () => $this->observer->hasPendingWork()));
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
