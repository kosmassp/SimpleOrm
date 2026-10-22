<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Dialect;

use DateTimeImmutable;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use ReflectionProperty;
use SimpleOrm\Dialect\SqlServerDialect;
use SimpleOrm\Errors\ConcurrencyException;
use SimpleOrm\Errors\SimpleOrmException;
use SimpleOrm\Migrations\MigrationRunner;
use SimpleOrm\Migrations\MigrationSet;
use SimpleOrm\Migrations\SqlVersion;
use SimpleOrm\Migrations\SqlVersionStep;
use SimpleOrm\Query\Criteria;
use SimpleOrm\Query\FetchMode;
use SimpleOrm\Query\SortOrder;
use SimpleOrm\Session\Command;
use SimpleOrm\Session\Db;
use SimpleOrm\Session\DbOptions;
use SimpleOrm\Session\TrackingMode;
use SimpleOrm\Tests\Dialect\Fixtures\SqlServer\BadSqlServerRegistry;
use SimpleOrm\Tests\Dialect\Fixtures\SqlServer\ByIdArgs;
use SimpleOrm\Tests\Dialect\Fixtures\SqlServer\CompChild;
use SimpleOrm\Tests\Dialect\Fixtures\SqlServer\CompParent;
use SimpleOrm\Tests\Dialect\Fixtures\SqlServer\GuidDoc;
use SimpleOrm\Tests\Dialect\Fixtures\SqlServer\LegacyStamp;
use SimpleOrm\Tests\Dialect\Fixtures\SqlServer\ReservedOrder;
use SimpleOrm\Tests\Dialect\Fixtures\SqlServer\SqlServerRegistry;
use SimpleOrm\Tests\Dialect\Fixtures\Triggers\ProbeLog;
use SimpleOrm\Tests\Dialect\Fixtures\Triggers\TriggerProbe;
use SimpleOrm\Tests\Dialect\Fixtures\Triggers\TriggerProbeAfter;
use SimpleOrm\Tests\Sample\Models\Role;
use SimpleOrm\Tests\Sample\Models\RoleChange;
use SimpleOrm\Tests\Sample\Models\Transaction;
use SimpleOrm\Tests\Sample\Models\TransactionDetail;
use SimpleOrm\Tests\Sample\Models\User;
use SimpleOrm\Tests\Sample\Models\UserProfile;
use SimpleOrm\Tests\Sample\Models\UserRole;
use SimpleOrm\Tests\Support\ConformanceClock;
use SimpleOrm\Tests\Support\BytesRoundTrip;
use SimpleOrm\Tests\Support\SampleDatabase;
use SimpleOrm\Tests\Support\SqlServerDatabase;
use SimpleOrm\Tests\Support\TriggerDefinitions;
use SimpleOrm\Types\Decimal;
use SimpleOrm\Validation\SchemaGuard;

/**
 * The SQL Server live leg (ADR-0024, spec/dialects.md "Testing contract"): every test runs against a **real server**
 * in a database of its own (`simpleorm_test_<hex>`), and the whole class skips where no server answers
 * (`SIMPLEORM_SQLSERVER` = a `sqlsrv:`/`odbc:` PDO DSN reaching `master`; default LocalDB through `pdo_sqlsrv`).
 * Nothing is mocked. The tests need only what the dialect needs: the sample schema is created from metadata.
 */
final class SqlServerLiveTest extends TestCase
{
    private ?SqlServerDatabase $server = null;

    private ?Db $db = null;

    /** @var list<Db> */
    private array $extraSessions = [];

    protected function setUp(): void
    {
        SqlServerDatabase::skipUnlessAvailable();
        $this->server = SqlServerDatabase::create();
    }

    protected function tearDown(): void
    {
        foreach ([...$this->extraSessions, $this->db] as $session) {
            $session?->close();
        }

        $this->extraSessions = [];
        $this->db = null;
        $this->server?->drop();
        $this->server = null;
    }

    private function session(?DbOptions $options = null): Db
    {
        return Db::open($this->server?->connectionString() ?? '', $options ?? new DbOptions(new SqlServerDialect()));
    }

    /** A session with the whole sample schema created from metadata (tables, their indexes, no view). */
    private function openSample(?DbOptions $options = null): Db
    {
        $db = $this->session($options);
        $this->db = $db;
        foreach ([User::class, Role::class, UserRole::class, UserProfile::class, Transaction::class, TransactionDetail::class] as $type) {
            $db->createTable($type);
        }

        return $db;
    }

    private function openBare(): Db
    {
        $this->db = $this->session();

        return $this->db;
    }

    /** @param class-string $entityType */
    private static function rowCount(Db $db, string $entityType): int
    {
        return count($db->queryAll($entityType));
    }

    private static function navigate(object $owner, string $navigation, mixed $value): void
    {
        (new ReflectionProperty($owner::class, $navigation))->setValue($owner, $value);
    }

    private static function scalar(Db $db, string $sql): mixed
    {
        return $db->connection()->query($sql)->fetchColumn();
    }

    // --- keys and temporals -----------------------------------------------------------------------

    #[Test]
    public function an_identity_key_is_written_back_and_a_datetimeoffset_reads_back_as_utc(): void
    {
        $db = $this->openSample();

        $ada = SampleDatabase::insertUser($db, 'Ada', 'ada@example.com');
        $grace = SampleDatabase::insertUser($db, 'Grace', 'grace@example.com');
        self::assertGreaterThan(0, $ada->id);
        self::assertGreaterThan($ada->id, $grace->id);

        $loaded = $db->get(User::class, $ada->id);
        self::assertEquals(SampleDatabase::seedTime(), $loaded->createdAtUtc);
        self::assertSame(0, $loaded->createdAtUtc->getOffset());

        // A non-UTC instant is converted before it is stored; it reads back as the same instant, in UTC.
        $offset = new User();
        $offset->name = 'Offset';
        $offset->email = 'offset@example.com';
        $offset->createdAtUtc = new DateTimeImmutable('2026-08-28T12:30:00+03:00');
        $db->insert($offset);

        $reloaded = $db->get(User::class, $offset->id);
        self::assertEquals(new DateTimeImmutable('2026-08-28T09:30:00Z'), $reloaded->createdAtUtc);
        self::assertSame(0, $reloaded->createdAtUtc->getOffset());
        self::assertNull($reloaded->updatedAtUtc);
    }

    /**
     * ADR-0040 add.1: bytes bind as PDO::PARAM_LOB, never a string into varbinary. Its last write,
     * a NULL into varbinary(max), is also the probe for the null-typing ruling waiting for a SQL
     * Server engine (spec/dialects.md): an untyped null may arrive as nvarchar and refuse (257).
     */
    #[Test]
    public function bytes_round_trip_through_crud_criteria_and_registry_queries(): void
    {
        BytesRoundTrip::run($this->openSample());
    }

    // --- triggers (Level 4, ADR-0042) --------------------------------------------------------------

    #[Test]
    public function the_declared_trigger_is_created_from_metadata_fires_once_per_updated_row_and_reads_back_as_its_rendering(): void
    {
        $db = $this->openSample();
        $db->createTable(RoleChange::class);
        $db->createTriggers(Role::class);

        SampleDatabase::insertRole($db, 'first');
        SampleDatabase::insertRole($db, 'second');
        self::assertSame(0, self::rowCount($db, RoleChange::class), 'an insert fires nothing: the trigger is on update');

        $db->connection()->exec('update roles set is_deleted = 0');   // one statement, two rows: the trigger body is set-based over `inserted`
        self::assertSame(2, self::rowCount($db, RoleChange::class));

        // Replace semantics: created again, it is still one trigger firing once per row.
        $db->createTriggers(Role::class);
        $db->connection()->exec('update roles set is_deleted = 0');
        self::assertSame(4, self::rowCount($db, RoleChange::class));

        $dialect = $db->options()->dialect;
        self::assertSame(
            TriggerDefinitions::rendered($dialect, $db->maps()->load(Role::class)),
            TriggerDefinitions::live($db->connection(), $dialect, 'trg_roles_audit'),
            'sys.sql_modules keeps the text as submitted: equal to the rendering after normalization',
        );
    }

    #[Test]
    public function a_multi_event_narrowed_after_declaration_fires_on_insert_and_on_an_update_of_its_column_only(): void
    {
        $db = $this->openBare();
        $db->createTable(ProbeLog::class);
        $db->createTable(TriggerProbeAfter::class);
        $db->createTriggers(TriggerProbeAfter::class);

        $probe = new TriggerProbeAfter();
        $probe->name = 'one';
        $db->insert($probe);
        self::assertSame(1, self::rowCount($db, ProbeLog::class), 'the insert fires the trigger');

        $db->connection()->exec("update trigger_probe_afters set note = N'n'");   // not the narrowed column: the prologue returns
        self::assertSame(1, self::rowCount($db, ProbeLog::class));

        $db->connection()->exec("update trigger_probe_afters set name = N'two'");
        self::assertSame(2, self::rowCount($db, ProbeLog::class));

        $dialect = $db->options()->dialect;
        self::assertSame(
            TriggerDefinitions::rendered($dialect, $db->maps()->load(TriggerProbeAfter::class)),
            TriggerDefinitions::live($db->connection(), $dialect, 'trg_probe_after'),
        );
    }

    #[Test]
    public function a_before_trigger_is_ddl005_before_anything_is_created(): void
    {
        $db = $this->openBare();

        try {
            $db->createTriggers(TriggerProbe::class);
            self::fail('expected DDL-005: SQL Server has no BEFORE triggers');
        } catch (SimpleOrmException $e) {
            self::assertSame('DDL-005', $e->errorCode);
        }
    }

    #[Test]
    public function a_client_guid_key_round_trips_through_uniqueidentifier(): void
    {
        $db = $this->openBare();
        $db->createTable(GuidDoc::class);

        $doc = new GuidDoc();
        $doc->title = 'spec v1';
        $db->insert($doc);
        self::assertMatchesRegularExpression('/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/', $doc->id);

        $loaded = $db->get(GuidDoc::class, $doc->id);
        self::assertSame($doc->id, $loaded->id);   // SQL Server spells GUIDs in upper case; the converter folds them
        self::assertSame('spec v1', $loaded->title);

        $doc->title = 'spec v2';
        $db->update($doc);
        self::assertSame('spec v2', $db->get(GuidDoc::class, $doc->id)->title);
    }

    // --- criteria paging ----------------------------------------------------------------------------

    #[Test]
    public function criteria_paging_works_with_and_without_order_by_and_binds_where_then_limit_then_offset(): void
    {
        $db = $this->openSample();
        $users = [];
        foreach (['U1', 'U2', 'U3', 'U4', 'U5'] as $name) {
            $users[] = SampleDatabase::insertUser($db, $name, strtolower($name) . '@example.com');
        }

        $ids = static fn (array $rows): array => array_map(static fn (User $u): int => $u->id, $rows);

        self::assertSame(
            [$users[1]->id, $users[2]->id],
            $ids($db->from(User::class)->orderBy('id')->limit(2)->offset(1)->toList()),
        );
        self::assertSame(
            [$users[0]->id, $users[1]->id],
            $ids($db->from(User::class)->orderBy('id')->limit(2)->toList()),
        );

        // No orderings: the dialect adds `order by (select null)`, so the paging clause is legal.
        self::assertCount(2, $db->from(User::class)->limit(2)->toList());
        self::assertCount(2, $db->from(User::class)->offset(3)->toList());
        self::assertCount(1, $db->from(User::class)->limit(1)->offset(4)->toList());

        // The placeholders bind WHERE first, then limit, then offset, though SQL Server writes the offset first.
        $page = $db->from(User::class)
            ->where(
                Criteria::ge('createdAtUtc', SampleDatabase::seedTime()),
                Criteria::in('id', $ids($users)),
            )
            ->orderBy('id', SortOrder::Desc)
            ->limit(2)
            ->offset(1)
            ->toList();
        self::assertSame([$users[3]->id, $users[2]->id], $ids($page));
    }

    // --- reserved words ---------------------------------------------------------------------------------

    #[Test]
    public function reserved_word_identifiers_work_end_to_end(): void
    {
        $db = $this->openBare();
        $db->createTable(ReservedOrder::class);   // create table [order] ([id] …, [desc] …, [group] …)

        $row = new ReservedOrder();
        $row->description = 'first';
        $row->grouping = 'g1';
        $db->insert($row);
        self::assertGreaterThan(0, $row->id);

        $row->description = 'second';
        $db->update($row);
        self::assertSame('second', $db->get(ReservedOrder::class, $row->id)->description);

        $found = $db->from(ReservedOrder::class)->where(Criteria::eq('description', 'second'))->orderBy('description')->single();
        self::assertSame($row->id, $found->id);

        $found->grouping = 'g2';
        $db->updateOnly($found, ['grouping']);
        self::assertSame('g2', $db->get(ReservedOrder::class, $row->id)->grouping);

        self::assertCount(1, $db->queryAll(ReservedOrder::class));
        $db->delete(ReservedOrder::class, $row->id);
        self::assertSame([], $db->queryAll(ReservedOrder::class));
    }

    // --- concurrency and transactions ------------------------------------------------------------------------

    #[Test]
    public function the_version_column_guards_update_and_delete(): void
    {
        $db = $this->openSample();
        $ada = SampleDatabase::insertUser($db, 'Ada', 'ada@example.com');
        $first = SampleDatabase::insertTransaction($db, $ada, '10');
        $stale = $db->get(Transaction::class, $first->id);
        self::assertSame(0, $first->version);

        $first->amount = Decimal::of('12');
        $db->update($first);
        self::assertSame(1, $first->version);

        $stale->amount = Decimal::of('99');   // still version 0
        try {
            $db->update($stale);
            self::fail('expected CRUD-010');
        } catch (ConcurrencyException $e) {
            self::assertSame('CRUD-010', $e->errorCode);
        }

        try {
            $db->delete(Transaction::class, $stale);   // the version-checked form
            self::fail('expected CRUD-010');
        } catch (ConcurrencyException $e) {
            self::assertSame('CRUD-010', $e->errorCode);
        }

        self::assertTrue($db->get(Transaction::class, $first->id)->amount->equals(Decimal::of('12')));
        $db->delete(Transaction::class, $first);
        self::assertNull($db->getOrDefault(Transaction::class, $first->id));
    }

    /**
     * ADR-0040 add.3, spec/dialects.md Clarifications 22–23: an `AFTER UPDATE, DELETE` trigger that inserts two log rows
     * without `SET NOCOUNT ON` makes the driver add them to a statement's count, so a stale version-checked write (no
     * row of its own) would pass for a success. The dialect's generated writes report their own count instead; a
     * registry command on the same session keeps the driver's (the batch ends with `set nocount off`).
     */
    #[Test]
    public function generated_writes_report_their_own_count_under_an_after_trigger_without_nocount(): void
    {
        $db = $this->openSample();
        $pdo = $db->connection();
        $ada = SampleDatabase::insertUser($db, 'Ada', 'ada@example.com');
        $transaction = SampleDatabase::insertTransaction($db, $ada, '10');
        $stale = $db->get(Transaction::class, $transaction->id);   // still version 0

        $pdo->exec('create table transaction_log (id int identity(1,1) primary key, note nvarchar(20) not null)');
        $pdo->exec(
            'create trigger trg_transactions_log on transactions after update, delete as begin '
            . "insert into transaction_log (note) values (N'first'); insert into transaction_log (note) values (N'second'); end",
        );

        try {
            $transaction->amount = Decimal::of('12');
            $db->update($transaction);
            self::assertSame(1, $transaction->version);

            $transaction->amount = Decimal::of('14');
            $db->updateOnly($transaction, ['amount']);
            self::assertSame(2, $transaction->version);

            $stale->amount = Decimal::of('99');
            try {
                $db->update($stale);
                self::fail('expected CRUD-010 from the stale update');
            } catch (ConcurrencyException $e) {
                self::assertSame('CRUD-010', $e->errorCode);
            }

            try {
                $db->delete(Transaction::class, $stale);   // the version-checked form
                self::fail('expected CRUD-010 from the stale delete');
            } catch (ConcurrencyException $e) {
                self::assertSame('CRUD-010', $e->errorCode);
            }

            self::assertSame(0, $stale->version);   // a refused write leaves the stale version untouched

            // The session setting is restored: a registry command still reports its own count.
            $touch = Command::inline(ByIdArgs::class, 'update users set display_name = display_name where id = @id');
            self::assertSame(1, $db->execute($touch, new ByIdArgs($ada->id)));

            $db->delete(Transaction::class, $transaction);
            try {
                $db->delete(Transaction::class, $transaction->id);   // by key, the row is gone
                self::fail('expected CRUD-001 from the by-key delete of a gone row');
            } catch (SimpleOrmException $e) {
                self::assertSame('CRUD-001', $e->errorCode);
            }

            // Six statements fired the trigger, two log rows each.
            self::assertSame(12, (int) self::scalar($db, 'select count(*) from transaction_log'));
        } finally {
            $pdo->exec('drop trigger if exists trg_transactions_log');
        }
    }

    #[Test]
    public function a_transaction_scope_commits_or_rolls_back(): void
    {
        $db = $this->openSample();

        $scope = $db->begin();
        SampleDatabase::insertUser($db, 'Ada', 'ada@example.com');
        $scope->rollback();
        self::assertSame(0, self::rowCount($db, User::class));

        $scope = $db->begin();
        SampleDatabase::insertUser($db, 'Grace', 'grace@example.com');
        $scope->commit();
        self::assertSame(1, self::rowCount($db, User::class));
    }

    // --- loading ---------------------------------------------------------------------------------------------------

    #[Test]
    public function explicit_batch_and_link_loading_fill_the_navigations(): void
    {
        $db = $this->openSample();
        $ada = SampleDatabase::insertUser($db, 'Ada', 'ada@example.com');
        $bob = SampleDatabase::insertUser($db, 'Bob', 'bob@example.com');
        $first = SampleDatabase::insertTransaction($db, $ada, '1.00');
        SampleDatabase::insertTransaction($db, $ada, '2.00');
        $admin = SampleDatabase::insertRole($db, 'admin');
        SampleDatabase::insertUserRole($db, $ada, $admin);

        // explicit: many-to-one
        $transaction = $db->get(Transaction::class, $first->id);
        $db->load($transaction, 'user');
        self::assertSame($ada->id, $transaction->user?->id);

        // batch: one-to-many for two owners at once; Bob's collection is loaded-but-empty
        $users = $db->from(User::class)->orderBy('id')->toList();
        $db->loadEach($users, 'transactions');
        self::assertCount(2, $users[0]->transactions);
        self::assertSame([], $users[1]->transactions);
        self::assertSame($bob->id, $users[1]->id);

        // link: many-to-many through user_roles (two queries)
        $db->loadEach($users, 'roles');
        self::assertCount(1, $users[0]->roles);
        self::assertSame($admin->id, $users[0]->roles[0]->id);
        self::assertSame([], $users[1]->roles);
    }

    #[Test]
    public function a_subselect_include_works_over_a_paged_and_an_ordered_unpaged_root(): void
    {
        $db = $this->openSample();
        $ada = SampleDatabase::insertUser($db, 'Ada', 'ada@example.com');
        $bob = SampleDatabase::insertUser($db, 'Bob', 'bob@example.com');
        $cy = SampleDatabase::insertUser($db, 'Cy', 'cy@example.com');
        SampleDatabase::insertTransaction($db, $ada, '1.00');
        SampleDatabase::insertTransaction($db, $ada, '2.00');
        SampleDatabase::insertTransaction($db, $bob, '3.00');

        // Paged: the subquery re-evaluates the root with its `order by … offset … fetch next …` (key-tiebroken).
        $page = $db->from(User::class)->orderBy('name')->limit(2)->include('transactions')->fetch(FetchMode::SubSelect)->toList();
        self::assertSame([$ada->id, $bob->id], array_map(static fn (User $u): int => $u->id, $page));
        self::assertCount(2, $page[0]->transactions);
        self::assertCount(1, $page[1]->transactions);

        // Unpaged but ordered: SQL Server refuses ORDER BY in a subquery without TOP/OFFSET, so the renderer drops it.
        $all = $db->from(User::class)->orderBy('id')->include('transactions')->fetch(FetchMode::SubSelect)->toList();
        self::assertCount(3, $all);
        self::assertSame($cy->id, $all[2]->id);
        self::assertSame([], $all[2]->transactions);
    }

    #[Test]
    public function a_composite_key_subselect_include_goes_through_the_exists_rewrite(): void
    {
        $db = $this->openBare();
        $db->createTable(CompParent::class);
        $db->createTable(CompChild::class);

        foreach ([[1, 1], [1, 2], [2, 1]] as [$a, $b]) {
            $parent = new CompParent();
            $parent->aId = $a;
            $parent->bId = $b;
            $parent->label = "p{$a}{$b}";
            $db->insert($parent);
        }

        foreach ([[1, 1, 'c1'], [1, 1, 'c2'], [1, 2, 'c3'], [2, 1, 'c4']] as [$a, $b, $note]) {
            $child = new CompChild();
            $child->parentA = $a;
            $child->parentB = $b;
            $child->note = $note;
            $db->insert($child);
            self::assertGreaterThan(0, $child->id);
        }

        $page = $db->from(CompParent::class)->orderBy('aId')->orderBy('bId')->limit(2)
            ->include('children')->fetch(FetchMode::SubSelect)->toList();
        self::assertSame(['p11', 'p12'], array_map(static fn (CompParent $p): string => $p->label, $page));
        self::assertSame(['c1', 'c2'], array_map(static fn (CompChild $c): string => $c->note, $page[0]->children));
        self::assertSame(['c3'], array_map(static fn (CompChild $c): string => $c->note, $page[1]->children));

        $all = $db->from(CompParent::class)->orderBy('aId')->orderBy('bId')->include('children')->fetch(FetchMode::SubSelect)->toList();
        self::assertCount(3, $all);
        self::assertSame(['c4'], array_map(static fn (CompChild $c): string => $c->note, $all[2]->children));
    }

    // --- migrations -------------------------------------------------------------------------------------------------------

    /** @return array{0: SqlVersion, 1: SqlVersion} */
    private function widgetVersions(): array
    {
        $dialect = new SqlServerDialect();

        return [
            new SqlVersion(1, new SqlVersionStep(
                'widgets',
                'create widgets',
                ['create table widgets (id int not null primary key, name nvarchar(100) not null)'],
                [$dialect->dropTableSql('widgets')],
            )),
            new SqlVersion(2, new SqlVersionStep(
                'widgets',
                'add a note',
                [$dialect->addColumnSql('widgets', 'note', 'nvarchar(100)', true, null)],
                [$dialect->dropColumnSql('widgets', 'note')],
            )),
        ];
    }

    #[Test]
    public function migrations_run_up_idempotently_and_down_under_the_application_lock(): void
    {
        $db = $this->openBare();
        $dialect = new SqlServerDialect();
        $runner = new MigrationRunner($db->connection(), $dialect, $db->maps(), MigrationSet::of(...$this->widgetVersions()));

        self::assertTrue($runner->hasPending());
        self::assertSame(2, $runner->migrate());
        self::assertSame(0, $runner->migrate());   // re-run: nothing pending, the lock is taken and released again
        self::assertFalse($runner->hasPending());
        self::assertSame(2, (int) self::scalar($db, 'select count(*) from schema_version'));
        self::assertSame(1, (int) self::scalar($db, "select count(*) from sys.columns where object_id = object_id(N'widgets') and name = N'note'"));

        // A second connection takes the lock at once: the first run's transaction (and its lock) ended with COMMIT.
        $other = $this->session();
        $this->extraSessions[] = $other;
        $second = new MigrationRunner($other->connection(), $dialect, $other->maps(), MigrationSet::of(...$this->widgetVersions()));
        self::assertSame(0, $second->migrate());

        self::assertSame(1, $runner->migrateDown(1));   // the hand-written Down() override of version 2
        self::assertSame(0, (int) self::scalar($db, "select count(*) from sys.columns where object_id = object_id(N'widgets') and name = N'note'"));
        self::assertSame(1, (int) self::scalar($db, 'select count(*) from schema_version'));

        self::assertSame(1, $runner->migrateDown(0));
        self::assertNull(self::scalar($db, "select object_id(N'widgets', N'U')") ?: null);
        self::assertSame(0, (int) self::scalar($db, 'select count(*) from schema_version'));
    }

    #[Test]
    public function a_failing_migration_run_applies_nothing(): void
    {
        $db = $this->openBare();
        [$create] = $this->widgetVersions();
        $broken = new SqlVersion(2, new SqlVersionStep('widgets', 'broken', ['alter table widgets add [x] not_a_type'], []));
        $runner = new MigrationRunner($db->connection(), new SqlServerDialect(), $db->maps(), MigrationSet::of($create, $broken));

        try {
            $runner->migrate();
            self::fail('expected MIG-021');
        } catch (SimpleOrmException $e) {
            self::assertSame('MIG-021', $e->errorCode);
        }

        // DDL is transactional: neither the table nor the version table survived the rollback.
        self::assertNull(self::scalar($db, "select object_id(N'widgets', N'U')") ?: null);
        self::assertNull(self::scalar($db, "select object_id(N'schema_version', N'U')") ?: null);
    }

    #[Test]
    public function the_run_lock_is_exclusive_across_connections_and_released_on_rollback(): void
    {
        $dialect = new SqlServerDialect();
        $connectionString = $this->server?->connectionString() ?? '';
        $holder = $dialect->createConnection($connectionString);
        $other = $dialect->createConnection($connectionString);
        // applock_test answers 1 where the lock could be granted now, 0 where another owner holds it.
        $probe = "select applock_test('public', N'simpleorm:migrations', 'Exclusive', 'Transaction')";

        $dialect->beginMigrationRunLock($holder);
        $other->exec('BEGIN TRANSACTION');
        self::assertSame(0, (int) $other->query($probe)->fetchColumn());
        $other->exec('ROLLBACK');

        $holder->exec('ROLLBACK');   // the lock is transaction-owned: it dies with the transaction, never leaking past a rollback
        $other->exec('BEGIN TRANSACTION');
        self::assertSame(1, (int) $other->query($probe)->fetchColumn());
        $other->exec('ROLLBACK');

        unset($holder, $other);   // release both before tearDown drops the database
    }

    #[Test]
    public function every_fetch_mode_loads_the_same_graph(): void
    {
        $db = $this->openSample();
        $ada = SampleDatabase::insertUser($db, 'Ada', 'ada@example.com');
        $bob = SampleDatabase::insertUser($db, 'Bob', 'bob@example.com');
        $first = SampleDatabase::insertTransaction($db, $ada, '1.00');
        $second = SampleDatabase::insertTransaction($db, $ada, '2.00');
        $third = SampleDatabase::insertTransaction($db, $bob, '3.00');

        foreach ([FetchMode::MultiQuery, FetchMode::SubSelect, FetchMode::Join] as $mode) {
            $users = $db->from(User::class)->orderBy('id')->include('transactions')->fetch($mode)->toList();
            $graph = array_map(
                static fn (User $u): array => [$u->id, array_map(static fn (Transaction $t): int => $t->id, $u->transactions)],
                $users,
            );
            self::assertSame([[$ada->id, [$first->id, $second->id]], [$bob->id, [$third->id]]], $graph, $mode->name);
        }
    }

    // --- temporals on legacy columns ------------------------------------------------------------------------------------------

    #[Test]
    public function a_markerless_datetime2_column_refuses_val020_at_read_time(): void
    {
        $db = $this->openBare();
        $db->connection()->exec('create table ss_legacy_stamps (id int identity(1,1) primary key, stamped_at datetime2 not null)');
        $db->connection()->exec("insert into ss_legacy_stamps (stamped_at) values ('2026-01-01T00:00:00')");

        try {
            $db->queryAll(LegacyStamp::class);
            self::fail('expected VAL-020');
        } catch (SimpleOrmException $e) {
            self::assertSame('VAL-020', $e->errorCode);
        }
    }

    // --- the tracked session --------------------------------------------------------------------------------------------------------

    #[Test]
    public function a_tracked_session_persists_a_graph_flushes_dirty_changes_and_stamps_the_hooks(): void
    {
        $clock = ConformanceClock::fixed();
        $db = $this->openSample(new DbOptions(new SqlServerDialect(), tracking: TrackingMode::Tracked, clock: $clock));
        $observer = $this->session();
        $this->extraSessions[] = $observer;

        $user = new User();
        $user->name = 'Ada';
        $user->email = 'ada@example.com';
        $db->persist($user);
        self::assertSame(1, $db->flush()->inserted);
        self::assertGreaterThan(0, $user->id);
        self::assertEquals($clock(), $user->createdAtUtc);   // #[CreatedAt] filled from the session clock

        $transaction = new Transaction();
        $transaction->userId = $user->id;
        $transaction->amount = Decimal::of('5');
        $detail = new TransactionDetail();
        $detail->description = 'Widget';
        $detail->quantity = 2;
        $detail->unitPrice = Decimal::of('2.50');
        self::navigate($transaction, 'details', [$detail]);   // the declared persist cascade carries the detail along

        $db->persist($transaction);
        self::assertSame(2, $db->flush()->inserted);
        self::assertSame($transaction->id, $detail->transactionId);   // the generated key flowed into the dependent

        $transaction->note = 'shipped';
        self::assertTrue($db->isDirty($transaction));
        $summary = $db->flush();
        self::assertSame(1, $summary->updated);
        self::assertSame(1, $transaction->version);
        self::assertEquals($clock(), $transaction->updatedAtUtc);   // #[UpdatedAt] stamped by the write

        $stored = $observer->get(Transaction::class, $transaction->id);
        self::assertSame('shipped', $stored->note);
        self::assertSame(1, $stored->version);
        self::assertEquals($clock(), $stored->updatedAtUtc);
        self::assertEquals($clock(), $stored->createdAtUtc);
        self::assertSame(1, self::rowCount($observer, TransactionDetail::class));
    }

    // --- SchemaGuard ------------------------------------------------------------------------------------------------------------------

    #[Test]
    public function schema_guard_validates_the_registry_and_the_entities_against_the_live_schema(): void
    {
        $db = $this->openSample();

        // The entity checks read the live columns through the dialect's introspection query (VAL-011/VAL-010/VAL-013).
        SchemaGuard::validate($db, [
            SqlServerRegistry::class,
            User::class,
            Role::class,
            UserRole::class,
            UserProfile::class,
            Transaction::class,
            TransactionDetail::class,
        ]);

        $codes = array_map(static fn ($error): string => $error->code, SchemaGuard::report($db, [BadSqlServerRegistry::class]));
        self::assertContains('VAL-001', $codes);

        // A column the table does not have is VAL-013.
        $db->connection()->exec('alter table transaction_details drop column quantity');
        $drifted = array_map(static fn ($error): string => $error->code, SchemaGuard::report($db, [TransactionDetail::class]));
        self::assertContains('VAL-013', $drifted);
    }
}
