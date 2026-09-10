<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Session;

use PDO;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use SimpleOrm\Dialect\Dialect;
use SimpleOrm\Dialect\SqliteDialect;
use SimpleOrm\Errors\SimpleOrmException;
use SimpleOrm\Metadata\ColumnType;
use SimpleOrm\Metadata\EntityMap;
use SimpleOrm\Metadata\PropertyMap;
use SimpleOrm\Metadata\RelationshipKind;
use SimpleOrm\Metadata\RelationshipMap;
use SimpleOrm\Query\SelectAst;
use SimpleOrm\Session\Db;
use SimpleOrm\Session\DbLoading;
use SimpleOrm\Session\DbOptions;
use SimpleOrm\Tests\Sample\Models\Role;
use SimpleOrm\Tests\Sample\Models\Transaction;
use SimpleOrm\Tests\Sample\Models\User;
use SimpleOrm\Tests\Sample\Models\UserProfile;
use SimpleOrm\Tests\Sample\Models\UserRole;
use SimpleOrm\Tests\Support\SampleDatabase;
use SimpleOrm\Tests\Support\TempDatabase;
use SimpleOrm\Types\Decimal;

/**
 * Explicit/batch relationship loading (Level 2 milestone 3, ADR-0021,
 * spec/loading.md): `Db::load()`/`Db::loadEach()` against
 * {@see \SimpleOrm\Session\DbLoading}, one query per navigation (two for
 * many-to-many), chunked past the parameter budget, matching key/FK tuples by
 * value — never by string tokens.
 */
final class LoadingTest extends TestCase
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
    public function explicit_load_fills_a_many_to_one_navigation_from_its_foreign_key(): void
    {
        $user = SampleDatabase::insertUser($this->db, 'Ada', 'ada@example.com');
        $transaction = $this->insertTransaction($user->id, '19.99');

        self::assertNull($transaction->user);
        $this->db->load($transaction, 'user');

        self::assertNotNull($transaction->user);
        self::assertSame($user->id, $transaction->user->id);
    }

    #[Test]
    public function explicit_load_fills_a_one_to_one_navigation_or_null(): void
    {
        $withProfile = SampleDatabase::insertUser($this->db, 'Ada', 'ada@example.com');
        $withoutProfile = SampleDatabase::insertUser($this->db, 'Grace', 'grace@example.com');
        $this->insertProfile($withProfile->id, 'First programmer');

        $this->db->loadEach([$withProfile, $withoutProfile], 'profile');

        self::assertNotNull($withProfile->profile);
        self::assertSame('First programmer', $withProfile->profile->bio);
        self::assertNull($withoutProfile->profile);
    }

    #[Test]
    public function explicit_batch_load_fills_a_one_to_many_navigation_ordered_by_target_key(): void
    {
        $user = SampleDatabase::insertUser($this->db, 'Ada', 'ada@example.com');
        $other = SampleDatabase::insertUser($this->db, 'Grace', 'grace@example.com');
        $first = $this->insertTransaction($user->id, '1.00');
        $second = $this->insertTransaction($user->id, '2.00');

        $this->db->loadEach([$user, $other], 'transactions');

        self::assertSame([$first->id, $second->id], array_map(static fn (Transaction $t): int => $t->id, $user->transactions));
        self::assertSame([], $other->transactions);
    }

    #[Test]
    public function explicit_batch_load_fills_a_many_to_many_navigation_via_the_link(): void
    {
        $user = SampleDatabase::insertUser($this->db, 'Ada', 'ada@example.com');
        $other = SampleDatabase::insertUser($this->db, 'Grace', 'grace@example.com');
        $admin = $this->insertRole('admin');
        $auditor = $this->insertRole('auditor');
        $this->insertUserRole($user->id, $admin->id);
        $this->insertUserRole($user->id, $auditor->id);

        $this->db->loadEach([$user, $other], 'roles');

        self::assertSame(
            [$admin->id, $auditor->id],
            array_map(static fn (Role $r): int => $r->id, $user->roles),
        );
        self::assertSame([], $other->roles);
    }

    #[Test]
    public function owners_sharing_a_many_to_one_target_share_the_same_instance(): void
    {
        $user = SampleDatabase::insertUser($this->db, 'Ada', 'ada@example.com');
        $first = $this->insertTransaction($user->id, '1.00');
        $second = $this->insertTransaction($user->id, '2.00');

        $this->db->loadEach([$first, $second], 'user');

        self::assertNotNull($first->user);
        self::assertSame($first->user, $second->user);
    }

    #[Test]
    public function a_many_to_many_navigation_shares_the_same_target_instance_across_owners(): void
    {
        $userA = SampleDatabase::insertUser($this->db, 'Ada', 'ada@example.com');
        $userB = SampleDatabase::insertUser($this->db, 'Grace', 'grace@example.com');
        $shared = $this->insertRole('shared-role');
        $this->insertUserRole($userA->id, $shared->id);
        $this->insertUserRole($userB->id, $shared->id);

        $this->db->loadEach([$userA, $userB], 'roles');

        self::assertCount(1, $userA->roles);
        self::assertCount(1, $userB->roles);
        self::assertSame($userA->roles[0], $userB->roles[0]);
    }

    #[Test]
    public function a_dead_link_loads_as_null(): void
    {
        // No foreign-key constraint is emitted (§7.14/ADR-0011): a FK pointing at
        // no row is a legitimate database state, not an insert-time error.
        $transaction = $this->insertTransaction(999_999, '19.99');

        $this->db->load($transaction, 'user');

        self::assertNull($transaction->user);
    }

    #[Test]
    public function a_one_to_one_navigation_matched_by_more_than_one_row_is_rel_002(): void
    {
        // The sample's real 1:1 (User.profile) carries a unique index that makes
        // this state impossible to seed; a hand-built RelationshipMap against
        // Transaction.userId (never unique) drifts on purpose to exercise the
        // engine's own REL-002 check.
        $user = SampleDatabase::insertUser($this->db, 'Ada', 'ada@example.com');
        $this->insertTransaction($user->id, '1.00');
        $this->insertTransaction($user->id, '2.00');
        // 'profile' is a real declared navigation on User (so reflection finds
        // it); the REL-002 duplicate check fires while grouping query results,
        // before any value is written to it, so redirecting it to a
        // deliberately non-unique target is safe here.
        $bogus = new RelationshipMap('profile', RelationshipKind::OneToOne, Transaction::class, ['userId']);

        try {
            (new DbLoading($this->db))->loadEach($this->db->maps()->load(User::class), $bogus, [$user], null);
            self::fail('REL-002 expected');
        } catch (SimpleOrmException $e) {
            self::assertSame('REL-002', $e->errorCode);
        }
    }

    #[Test]
    public function an_unmapped_foreign_key_property_name_is_rel_003(): void
    {
        $bogus = new RelationshipMap('bogus', RelationshipKind::OneToMany, Transaction::class, ['noSuchProperty']);
        $user = SampleDatabase::insertUser($this->db, 'Ada', 'ada@example.com');

        try {
            (new DbLoading($this->db))->loadEach($this->db->maps()->load(User::class), $bogus, [$user], null);
            self::fail('REL-003 expected');
        } catch (SimpleOrmException $e) {
            self::assertSame('REL-003', $e->errorCode);
        }
    }

    #[Test]
    public function a_foreign_key_arity_mismatch_against_the_target_key_is_rel_003(): void
    {
        // UserRole's key is composite (userId, roleId); one declared FK part can never correlate to it.
        $bogus = new RelationshipMap('bogus', RelationshipKind::ManyToOne, UserRole::class, ['userId']);
        $transaction = $this->insertTransaction(1, '1.00');

        try {
            (new DbLoading($this->db))->loadEach($this->db->maps()->load(Transaction::class), $bogus, [$transaction], null);
            self::fail('REL-003 expected');
        } catch (SimpleOrmException $e) {
            self::assertSame('REL-003', $e->errorCode);
        }
    }

    #[Test]
    public function a_many_to_many_navigation_without_a_link_type_is_rel_003(): void
    {
        $bogus = new RelationshipMap('bogus', RelationshipKind::ManyToMany, Role::class, []);
        $user = SampleDatabase::insertUser($this->db, 'Ada', 'ada@example.com');

        try {
            (new DbLoading($this->db))->loadEach($this->db->maps()->load(User::class), $bogus, [$user], null);
            self::fail('REL-003 expected');
        } catch (SimpleOrmException $e) {
            self::assertSame('REL-003', $e->errorCode);
        }
    }

    #[Test]
    public function loaded_but_empty_reads_as_an_empty_array_never_null(): void
    {
        $withTransaction = SampleDatabase::insertUser($this->db, 'Ada', 'ada@example.com');
        $withoutTransaction = SampleDatabase::insertUser($this->db, 'Grace', 'grace@example.com');
        $this->insertTransaction($withTransaction->id, '1.00');

        $this->db->loadEach([$withTransaction, $withoutTransaction], 'transactions');

        self::assertSame([], $withoutTransaction->transactions);
    }

    #[Test]
    public function loading_replaces_the_navigations_guard_so_a_further_read_never_throws(): void
    {
        $user = SampleDatabase::insertUser($this->db, 'Ada', 'ada@example.com');
        $read = $this->db->from(User::class)->single();

        try {
            $read->transactions;
            self::fail('REL-004 expected before loading');
        } catch (SimpleOrmException $e) {
            self::assertSame('REL-004', $e->errorCode);
        }

        $this->db->load($read, 'transactions');

        self::assertSame([], $read->transactions);
    }

    #[Test]
    public function loading_chunks_past_the_parameter_budget_one_query_per_chunk(): void
    {
        $countingDialect = new CountingSelectDialect(new SqliteDialect());
        $fixture = TempDatabase::create();
        $db = Db::open($fixture->connectionString(), new DbOptions($countingDialect));
        try {
            $db->createTable(User::class);
            $db->createTable(Role::class);
            $db->createTable(UserRole::class);

            $roleCount = DbLoading::LOAD_CHUNK_SIZE + 1;   // 501: forces exactly two chunks on the target hop
            $tx = $db->begin();
            $user = new User();
            $user->name = 'Ada';
            $user->email = 'ada@example.com';
            $user->createdAtUtc = SampleDatabase::seedTime();
            $db->insert($user);

            $roles = [];
            for ($i = 0; $i < $roleCount; $i++) {
                $role = new Role();
                $role->name = "role-{$i}";
                $role->createdAtUtc = SampleDatabase::seedTime();
                $db->insert($role);
                $roles[] = $role;

                $link = new UserRole();
                $link->userId = $user->id;
                $link->roleId = $role->id;
                $link->createdAtUtc = SampleDatabase::seedTime();
                $db->insert($link);
            }

            $tx->commit();

            $before = $countingDialect->selectCount;
            $db->load($user, 'roles');
            $queriesIssued = $countingDialect->selectCount - $before;

            self::assertCount($roleCount, $user->roles);
            // Hop 1 (link rows for the one owner): one query. Hop 2 (targets by
            // key): 501 distinct target keys chunk into ceil(501/500) = 2
            // queries (spec/loading.md: "chunked at 500 owners per query").
            self::assertSame(3, $queriesIssued);
        } finally {
            $db->close();
            $fixture->delete();
        }
    }

    // --- fixture helpers ---------------------------------------------------------

    private function insertTransaction(int $userId, string $amount): Transaction
    {
        $transaction = new Transaction();
        $transaction->userId = $userId;
        $transaction->amount = Decimal::of($amount);
        $transaction->createdAtUtc = SampleDatabase::seedTime();
        $this->db->insert($transaction);

        return $transaction;
    }

    private function insertRole(string $name): Role
    {
        $role = new Role();
        $role->name = $name;
        $role->createdAtUtc = SampleDatabase::seedTime();
        $this->db->insert($role);

        return $role;
    }

    private function insertUserRole(int $userId, int $roleId): UserRole
    {
        $link = new UserRole();
        $link->userId = $userId;
        $link->roleId = $roleId;
        $link->createdAtUtc = SampleDatabase::seedTime();
        $this->db->insert($link);

        return $link;
    }

    private function insertProfile(int $userId, string $bio): UserProfile
    {
        $profile = new UserProfile();
        $profile->userId = $userId;
        $profile->bio = $bio;
        $profile->createdAtUtc = SampleDatabase::seedTime();
        $this->db->insert($profile);

        return $profile;
    }
}

/**
 * Wraps {@see SqliteDialect}, counting `selectSql` calls — a test-local way to
 * count round trips issued by the loading engine without mocking the database
 * (CODING-STANDARD §7 forbids mocks/fakes for the database itself; this
 * decorates the dialect seam, a real object, not a stand-in for SQLite).
 */
final class CountingSelectDialect implements Dialect
{
    public int $selectCount = 0;

    public function __construct(private readonly Dialect $inner)
    {
    }

    public function createConnection(string $connectionString): PDO
    {
        return $this->inner->createConnection($connectionString);
    }

    public function quoteIdentifier(string $identifier): string
    {
        return $this->inner->quoteIdentifier($identifier);
    }

    public function createTableSql(EntityMap $map): string
    {
        return $this->inner->createTableSql($map);
    }

    public function createIndexSql(EntityMap $map): array
    {
        return $this->inner->createIndexSql($map);
    }

    public function createViewSql(EntityMap $map): string
    {
        return $this->inner->createViewSql($map);
    }

    public function insertSql(EntityMap $map): string
    {
        return $this->inner->insertSql($map);
    }

    public function updateSql(EntityMap $map): string
    {
        return $this->inner->updateSql($map);
    }

    public function updateOnlySql(EntityMap $map, array $properties): string
    {
        return $this->inner->updateOnlySql($map, $properties);
    }

    public function deleteSql(EntityMap $map, bool $checkVersion): string
    {
        return $this->inner->deleteSql($map, $checkVersion);
    }

    public function limitOffsetClause(?string $limitParameter, ?string $offsetParameter): string
    {
        return $this->inner->limitOffsetClause($limitParameter, $offsetParameter);
    }

    public function selectSql(SelectAst $select, callable $bindParameter): string
    {
        $this->selectCount++;

        return $this->inner->selectSql($select, $bindParameter);
    }

    public function pagingRequiresOrderBy(): bool
    {
        return $this->inner->pagingRequiresOrderBy();
    }

    public function supportsRowValueIn(): bool
    {
        return $this->inner->supportsRowValueIn();
    }

    public function supportsArrayParameters(): bool
    {
        return $this->inner->supportsArrayParameters();
    }

    public function bindsTemporalsNatively(): bool
    {
        return $this->inner->bindsTemporalsNatively();
    }

    public function supportsMaterializedViews(): bool
    {
        return $this->inner->supportsMaterializedViews();
    }

    public function supportsProcedures(): bool
    {
        return $this->inner->supportsProcedures();
    }

    public function supportsTransactionalDdl(): bool
    {
        return $this->inner->supportsTransactionalDdl();
    }

    public function columnsInfoSql(): string
    {
        return $this->inner->columnsInfoSql();
    }

    public function viewDefinitionSql(): string
    {
        return $this->inner->viewDefinitionSql();
    }

    public function indexesInfoSql(): string
    {
        return $this->inner->indexesInfoSql();
    }

    public function isDeclaredTypeCompatible(string $declaredType, ColumnType $type): bool
    {
        return $this->inner->isDeclaredTypeCompatible($declaredType, $type);
    }

    public function storageType(PropertyMap $property): string
    {
        return $this->inner->storageType($property);
    }

    public function beginMigrationRunLock(PDO $connection): void
    {
        $this->inner->beginMigrationRunLock($connection);
    }

    public function versionTableSql(): string
    {
        return $this->inner->versionTableSql();
    }

    public function renameTableSql(string $fromName, string $toName): string
    {
        return $this->inner->renameTableSql($fromName, $toName);
    }

    public function renameColumnSql(string $table, string $fromName, string $toName): string
    {
        return $this->inner->renameColumnSql($table, $fromName, $toName);
    }

    public function addColumnSql(string $table, string $column, string $storageType, bool $nullable, ?string $defaultSql): string
    {
        return $this->inner->addColumnSql($table, $column, $storageType, $nullable, $defaultSql);
    }

    public function dropColumnSql(string $table, string $column): string
    {
        return $this->inner->dropColumnSql($table, $column);
    }

    public function dropTableSql(string $table): string
    {
        return $this->inner->dropTableSql($table);
    }

    public function dropIndexSql(string $table, string $index): string
    {
        return $this->inner->dropIndexSql($table, $index);
    }
}
