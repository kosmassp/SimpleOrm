<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Dialect;

use DateTimeImmutable;
use DateTimeZone;
use PDO;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use ReflectionProperty;
use SimpleOrm\Dialect\PostgresDialect;
use SimpleOrm\Errors\ConcurrencyException;
use SimpleOrm\Errors\SchemaValidationException;
use SimpleOrm\Errors\SimpleOrmException;
use SimpleOrm\Metadata\EntityMapLoader;
use SimpleOrm\Migrations\MigrationRunner;
use SimpleOrm\Migrations\MigrationSet;
use SimpleOrm\Migrations\MigrationState;
use SimpleOrm\Migrations\SqlVersion;
use SimpleOrm\Migrations\SqlVersionStep;
use SimpleOrm\Query\Criteria;
use SimpleOrm\Query\FetchMode;
use SimpleOrm\Query\SortOrder;
use SimpleOrm\Session\Db;
use SimpleOrm\Session\DbOptions;
use SimpleOrm\Session\TrackingMode;
use SimpleOrm\Tests\Dialect\Fixtures\Postgres\BadPostgresRegistry;
use SimpleOrm\Tests\Dialect\Fixtures\Postgres\BrokenSqlRegistry;
use SimpleOrm\Tests\Dialect\Fixtures\Postgres\CalendarRow;
use SimpleOrm\Tests\Dialect\Fixtures\Postgres\CompChild;
use SimpleOrm\Tests\Dialect\Fixtures\Postgres\CompParent;
use SimpleOrm\Tests\Dialect\Fixtures\Postgres\IdsArgs;
use SimpleOrm\Tests\Dialect\Fixtures\Postgres\LegacyStamp;
use SimpleOrm\Tests\Dialect\Fixtures\Postgres\NameCount;
use SimpleOrm\Tests\Dialect\Fixtures\Postgres\NamesArgs;
use SimpleOrm\Tests\Dialect\Fixtures\Postgres\OrderRow;
use SimpleOrm\Tests\Dialect\Fixtures\Postgres\PostgresRegistry;
use SimpleOrm\Tests\Dialect\Fixtures\Postgres\StampsArgs;
use SimpleOrm\Tests\Dialect\Fixtures\Triggers\ProbeLog;
use SimpleOrm\Tests\Dialect\Fixtures\Triggers\TriggerProbe;
use SimpleOrm\Tests\Sample\Models\Role;
use SimpleOrm\Tests\Sample\Models\RoleChange;
use SimpleOrm\Tests\Sample\Models\Transaction;
use SimpleOrm\Tests\Sample\Models\TransactionDetail;
use SimpleOrm\Tests\Sample\Models\TransactionStatus;
use SimpleOrm\Tests\Sample\Models\User;
use SimpleOrm\Tests\Sample\Models\UserProfile;
use SimpleOrm\Tests\Sample\Models\UserRole;
use SimpleOrm\Tests\Support\LiveServer;
use SimpleOrm\Tests\Support\PostgresDatabase;
use SimpleOrm\Tests\Support\BytesRoundTrip;
use SimpleOrm\Tests\Support\SampleDatabase;
use SimpleOrm\Tests\Support\TriggerDefinitions;
use SimpleOrm\Validation\SchemaGuard;
use SimpleOrm\Types\Decimal;

/**
 * The PostgreSQL leg (ADR-0025, spec/dialects.md "Testing contract"): a **real
 * server**, never a stand-in, skipped as a group where none answers. One
 * `simpleorm_test_<hex>` database per test class, dropped afterwards; the
 * `public` schema is rebuilt before every test so tests cannot see each other.
 * Run with `composer test-live` (`pdo_pgsql` loaded).
 */
final class PostgresLiveTest extends TestCase
{
    private static ?PostgresDatabase $database = null;

    /** @var list<Db> sessions to close before the next schema reset */
    private array $sessions = [];

    /** @var list<PDO> raw connections to release before the next schema reset */
    private array $connections = [];

    public static function setUpBeforeClass(): void
    {
        if (PostgresDatabase::available()) {
            self::$database = PostgresDatabase::create();
        }
    }

    public static function tearDownAfterClass(): void
    {
        self::$database?->drop();
        self::$database = null;
    }

    protected function setUp(): void
    {
        PostgresDatabase::skipUnlessAvailable();

        $admin = LiveServer::connect(self::database()->connectionString());
        $admin->exec('drop schema public cascade; create schema public');
    }

    protected function tearDown(): void
    {
        foreach ($this->sessions as $db) {
            $db->close();
        }

        $this->sessions = [];
        $this->connections = [];
    }

    private static function database(): PostgresDatabase
    {
        return self::$database ?? throw new \LogicException('the fixture database was not created');
    }

    /** A session on the fixture database with the sample tables (no view: its SQL is SQLite's). */
    private function open(?DbOptions $options = null): Db
    {
        $db = Db::open(self::database()->connectionString(), $options ?? new DbOptions(new PostgresDialect()));
        $this->sessions[] = $db;

        foreach ([User::class, Role::class, UserRole::class, UserProfile::class, Transaction::class, TransactionDetail::class] as $entity) {
            $db->createTable($entity);
        }

        return $db;
    }

    private function raw(): PDO
    {
        $connection = (new PostgresDialect())->createConnection(self::database()->connectionString());
        $this->connections[] = $connection;

        return $connection;
    }

    private static function utc(string $iso): DateTimeImmutable
    {
        return new DateTimeImmutable($iso, new DateTimeZone('UTC'));
    }

    /** @param callable():mixed $action */
    private static function codeOf(callable $action): string
    {
        try {
            $action();
        } catch (SimpleOrmException $exception) {
            return $exception->errorCode;
        }

        self::fail('expected a SimpleOrmException');
    }

    // --- generated keys and temporals ---------------------------------------------------------

    #[Test]
    public function an_identity_key_is_written_back_and_a_timestamptz_reads_back_as_utc(): void
    {
        $db = $this->open();

        $first = SampleDatabase::insertUser($db, 'Ada', 'ada@example.com');
        $second = SampleDatabase::insertUser($db, 'Grace', 'grace@example.com');

        self::assertGreaterThan(0, $first->id);
        self::assertSame($first->id + 1, $second->id);

        $read = $db->get(User::class, $first->id);
        self::assertInstanceOf(User::class, $read);
        self::assertSame(0, $read->createdAtUtc->getOffset());
        self::assertSame('2026-08-28T09:30:00Z', $read->createdAtUtc->format('Y-m-d\TH:i:s\Z'));
        self::assertNull($read->updatedAtUtc);
    }

    /** ADR-0040 add.1: bytes bind as PDO::PARAM_LOB — under PARAM_STR a bytea payload parses as escaped text. */
    #[Test]
    public function bytes_round_trip_through_crud_criteria_and_registry_queries(): void
    {
        BytesRoundTrip::run($this->open());
    }

    // --- triggers (Level 4, ADR-0042) --------------------------------------------------------------

    #[Test]
    public function the_declared_trigger_is_created_from_metadata_fires_once_per_updated_row_and_reads_back_as_its_rendering(): void
    {
        $db = $this->open();
        $db->createTable(RoleChange::class);
        $db->createTriggers(Role::class);

        SampleDatabase::insertRole($db, 'first');
        SampleDatabase::insertRole($db, 'second');
        self::assertSame(0, self::rows($db, 'role_changes'), 'an insert fires nothing: the trigger is on update');

        $db->connection()->exec('update roles set is_deleted = false');   // one statement, two rows
        self::assertSame(2, self::rows($db, 'role_changes'));
        $names = $db->connection()->query('select role_name from role_changes order by role_name')->fetchAll(PDO::FETCH_COLUMN);
        self::assertSame(['first', 'second'], $names);
        self::assertSame(0, $db->queryAll(RoleChange::class)[0]->changedAt->getOffset(), '{now} is a timestamptz: it reads back marked');

        // Replace semantics: created again, it is still one trigger firing once per row.
        $db->createTriggers(Role::class);
        $db->connection()->exec('update roles set is_deleted = false');
        self::assertSame(4, self::rows($db, 'role_changes'));

        $dialect = $db->options()->dialect;
        self::assertSame(
            TriggerDefinitions::rendered($dialect, $db->maps()->load(Role::class)),
            TriggerDefinitions::live($db->connection(), $dialect, 'trg_roles_audit'),
            'the catalog reconstruction equals the rendering after normalization',
        );
    }

    #[Test]
    public function a_multi_event_narrowed_before_declaration_fires_on_insert_and_on_an_update_of_its_column_only(): void
    {
        $db = $this->open();
        $db->createTable(ProbeLog::class);
        $db->createTable(TriggerProbe::class);
        $db->createTriggers(TriggerProbe::class);

        $probe = new TriggerProbe();
        $probe->name = 'one';
        $db->insert($probe);
        self::assertSame(1, self::rows($db, 'probe_logs'), 'the insert fires the before trigger');

        $db->connection()->exec("update trigger_probes set note = 'n'");   // not the narrowed column
        self::assertSame(1, self::rows($db, 'probe_logs'));

        $db->connection()->exec("update trigger_probes set name = 'two'");
        self::assertSame(2, self::rows($db, 'probe_logs'));
        self::assertSame('two', $db->get(TriggerProbe::class, $probe->id)->name, 'the before trigger returned the row: the write went through');

        $dialect = $db->options()->dialect;
        self::assertSame(
            TriggerDefinitions::rendered($dialect, $db->maps()->load(TriggerProbe::class)),
            TriggerDefinitions::live($db->connection(), $dialect, 'trg_probe_before'),
        );
    }

    #[Test]
    public function instead_of_on_a_table_is_ddl005_before_anything_is_created(): void
    {
        $db = $this->open();
        $dialect = $db->options()->dialect;
        $trigger = new \SimpleOrm\Metadata\TriggerDefinition('trg_io', \SimpleOrm\Metadata\TriggerTiming::InsteadOf, [\SimpleOrm\Metadata\TriggerEvent::Delete], 'select 1;');

        self::assertSame('DDL-005', self::codeOf(static fn () => $dialect->createTriggerSql('roles', false, $trigger)));
        self::assertCount(2, $dialect->createTriggerSql('some_view', true, $trigger), 'on a view it renders: the function and the trigger');
    }

    private static function rows(Db $db, string $table): int
    {
        return (int) $db->connection()->query('select count(*) from ' . $db->options()->dialect->quoteIdentifier($table))->fetchColumn();
    }

    #[Test]
    public function a_session_time_zone_offset_with_minutes_still_reads_back_as_the_same_instant(): void
    {
        $db = $this->open();
        SampleDatabase::insertUser($db, 'Ada', 'ada@example.com');

        $db->connection()->exec("set time zone 'Asia/Kolkata'");   // text arrives as ...+05:30
        $users = $db->queryAll(User::class);

        self::assertSame(
            SampleDatabase::seedTime()->getTimestamp(),
            $users[0]->createdAtUtc->getTimestamp(),
        );
    }

    #[Test]
    public function a_temporal_criteria_comparison_runs_against_timestamptz(): void
    {
        $db = $this->open();
        $old = SampleDatabase::insertUser($db, 'Old', 'old@example.com');
        $new = new User();
        $new->name = 'New';
        $new->email = 'new@example.com';
        $new->createdAtUtc = self::utc('2026-09-15T12:00:00Z');
        $db->insert($new);

        $recent = $db->from(User::class)
            ->where(Criteria::ge('createdAtUtc', self::utc('2026-09-01T00:00:00Z')))
            ->toList();
        $older = $db->from(User::class)
            ->where(Criteria::lt('createdAtUtc', self::utc('2026-09-01T00:00:00Z')))
            ->toList();

        self::assertSame([$new->id], array_map(static fn (User $u): int => $u->id, $recent));
        self::assertSame([$old->id], array_map(static fn (User $u): int => $u->id, $older));
    }

    #[Test]
    public function a_markerless_timestamp_column_refuses_val_020_at_read_time(): void
    {
        $db = $this->open();
        $db->connection()->exec(
            'create table legacy_stamps (id bigint generated by default as identity primary key, stamped_at timestamp without time zone not null)',
        );
        $db->connection()->exec("insert into legacy_stamps (stamped_at) values ('2026-08-28 09:30:00')");

        self::assertSame('VAL-020', self::codeOf(static fn () => $db->queryAll(LegacyStamp::class)));
    }

    // --- array parameters -------------------------------------------------------------------

    #[Test]
    public function an_array_parameter_binds_as_one_parameter_filled_and_empty(): void
    {
        $db = $this->open();
        $ada = SampleDatabase::insertUser($db, 'Ada', 'ada@example.com');
        SampleDatabase::insertUser($db, 'Grace', 'grace@example.com');
        $linus = SampleDatabase::insertUser($db, 'Linus', 'linus@example.com');

        $rows = $db->query(PostgresRegistry::usersByIds(), new IdsArgs([$ada->id, $linus->id]));
        self::assertSame(['ada@example.com', 'linus@example.com'], array_map(static fn ($r): string => $r->email, $rows));

        self::assertSame([], $db->query(PostgresRegistry::usersByIds(), new IdsArgs([])));
    }

    #[Test]
    public function string_elements_with_quotes_commas_braces_and_backslashes_survive_the_literal(): void
    {
        $db = $this->open();
        $names = ['O"Neil', 'a,b', '{braced}', 'back\\slash', 'NULL', ' padded ', ''];
        foreach ($names as $i => $name) {
            SampleDatabase::insertUser($db, $name, "u{$i}@example.com");
        }

        $rows = $db->query(PostgresRegistry::usersByNames(), new NamesArgs($names));

        self::assertCount(count($names), $rows);
        self::assertSame([], $db->query(PostgresRegistry::usersByNames(), new NamesArgs(['no such name'])));
    }

    #[Test]
    public function a_temporal_collection_binds_as_a_timestamptz_array(): void
    {
        $db = $this->open();
        SampleDatabase::insertUser($db, 'Ada', 'ada@example.com');

        $hit = $db->query(PostgresRegistry::usersCreatedAt(), new StampsArgs([SampleDatabase::seedTime(), self::utc('2001-01-01T00:00:00Z')]));
        $miss = $db->query(PostgresRegistry::usersCreatedAt(), new StampsArgs([self::utc('2001-01-01T00:00:00Z')]));

        self::assertCount(1, $hit);
        self::assertSame([], $miss);
    }

    // --- paging, identifiers, concurrency, transactions --------------------------------------------

    #[Test]
    public function criteria_paging_works_with_and_without_an_order_by(): void
    {
        $db = $this->open();
        foreach (['a', 'b', 'c', 'd', 'e'] as $name) {
            SampleDatabase::insertUser($db, $name, "{$name}@example.com");
        }

        $ordered = $db->from(User::class)->orderBy('name', SortOrder::Desc)->limit(2)->offset(1)->toList();
        self::assertSame(['d', 'c'], array_map(static fn (User $u): string => $u->name, $ordered));

        self::assertCount(2, $db->from(User::class)->limit(2)->toList());
        self::assertCount(3, $db->from(User::class)->offset(2)->toList());   // `offset @c0` with no limit
        self::assertCount(2, $db->from(User::class)->limit(5)->offset(3)->toList());
    }

    #[Test]
    public function reserved_word_identifiers_work_end_to_end(): void
    {
        $db = $this->open();
        $db->createTable(OrderRow::class);

        $row = new OrderRow();
        $row->description = 'first';
        $row->selection = 'x';
        $db->insert($row);
        $other = new OrderRow();
        $other->description = 'second';
        $db->insert($other);

        $row->description = 'first, revised';
        $db->update($row);
        $db->updateOnly($other, ['selection']);

        $found = $db->from(OrderRow::class)->where(Criteria::eq('description', 'first, revised'))->orderBy('description')->toList();
        self::assertCount(1, $found);
        self::assertSame($row->id, $found[0]->id);
        self::assertSame('x', $db->get(OrderRow::class, $row->id)->selection);

        $db->delete(OrderRow::class, $other->id);
        self::assertCount(1, $db->queryAll(OrderRow::class));
    }

    #[Test]
    public function version_column_concurrency_guards_update_and_delete(): void
    {
        $db = $this->open();
        $user = SampleDatabase::insertUser($db, 'Ada', 'ada@example.com');
        $transaction = SampleDatabase::insertTransaction($db, $user, '10.00');
        self::assertSame(0, $transaction->version);

        $mine = $db->get(Transaction::class, $transaction->id);
        $theirs = $db->get(Transaction::class, $transaction->id);

        $mine->amount = Decimal::of('11.00');
        $db->update($mine);
        self::assertSame(1, $mine->version);

        $theirs->amount = Decimal::of('12.00');
        self::assertSame('CRUD-010', self::codeOf(static fn () => $db->update($theirs)));
        self::assertInstanceOf(ConcurrencyException::class, self::thrownBy(static fn () => $db->delete(Transaction::class, $theirs)));

        $db->delete(Transaction::class, $mine);
        self::assertSame([], $db->queryAll(Transaction::class));
    }

    private static function thrownBy(callable $action): \Throwable
    {
        try {
            $action();
        } catch (\Throwable $exception) {
            return $exception;
        }

        self::fail('expected an exception');
    }

    #[Test]
    public function a_transaction_scope_commits_and_rolls_back(): void
    {
        $db = $this->open();

        $scope = $db->begin();
        SampleDatabase::insertUser($db, 'Rolled', 'rolled@example.com');
        $scope->rollback();
        self::assertSame([], $db->queryAll(User::class));

        $scope = $db->begin();
        SampleDatabase::insertUser($db, 'Kept', 'kept@example.com');
        $scope->commit();
        self::assertCount(1, $db->queryAll(User::class));
    }

    // --- loading ------------------------------------------------------------------------------

    #[Test]
    public function explicit_batch_and_link_loading_fill_the_navigations(): void
    {
        $db = $this->open();
        $ada = SampleDatabase::insertUser($db, 'Ada', 'ada@example.com');
        $grace = SampleDatabase::insertUser($db, 'Grace', 'grace@example.com');
        SampleDatabase::insertTransaction($db, $ada, '1.00');
        SampleDatabase::insertTransaction($db, $ada, '2.00');
        $admin = SampleDatabase::insertRole($db, 'admin');
        $editor = SampleDatabase::insertRole($db, 'editor');
        SampleDatabase::insertUserRole($db, $ada, $admin);
        SampleDatabase::insertUserRole($db, $ada, $editor);
        SampleDatabase::insertUserRole($db, $grace, $editor);

        $transactions = $db->queryAll(Transaction::class);
        $db->load($transactions[0], 'user');                       // explicit, many-to-one
        self::assertSame($ada->id, $transactions[0]->user?->id);

        $users = $db->queryAll(User::class);
        $db->loadEach($users, 'transactions');                      // batch, one-to-many
        self::assertSame([2, 0], array_map(static fn (User $u): int => count($u->transactions), $users));

        $db->loadEach($users, 'roles');                             // link: two queries through user_roles
        self::assertSame([['admin', 'editor'], ['editor']], array_map(
            static fn (User $u): array => array_map(static fn (Role $r): string => $r->name, $u->roles),
            $users,
        ));
    }

    #[Test]
    public function a_subselect_include_over_a_paged_root_pages_correctly(): void
    {
        $db = $this->open();
        $users = [];
        foreach (['a', 'b', 'c'] as $name) {
            $users[$name] = SampleDatabase::insertUser($db, $name, "{$name}@example.com");
        }

        SampleDatabase::insertTransaction($db, $users['a'], '1.00');
        SampleDatabase::insertTransaction($db, $users['b'], '2.00');
        SampleDatabase::insertTransaction($db, $users['b'], '3.00');
        SampleDatabase::insertTransaction($db, $users['c'], '4.00');

        $rows = $db->from(User::class)
            ->orderBy('name')
            ->limit(2)
            ->include('transactions')
            ->fetch(FetchMode::SubSelect)
            ->toList();

        self::assertSame(['a', 'b'], array_map(static fn (User $u): string => $u->name, $rows));
        self::assertSame([1, 2], array_map(static fn (User $u): int => count($u->transactions), $rows));
    }

    #[Test]
    public function a_composite_key_subselect_include_renders_native_row_value_in(): void
    {
        $db = $this->open();
        $db->createTable(CompParent::class);
        $db->createTable(CompChild::class);

        foreach ([[1, 1, 'p11'], [1, 2, 'p12'], [2, 1, 'p21']] as [$a, $b, $label]) {
            $parent = new CompParent();
            $parent->aId = $a;
            $parent->bId = $b;
            $parent->label = $label;
            $db->insert($parent);
        }

        foreach ([[1, 1, 'x'], [1, 1, 'y'], [1, 2, 'z'], [2, 1, 'w']] as [$a, $b, $note]) {
            $child = new CompChild();
            $child->parentA = $a;
            $child->parentB = $b;
            $child->note = $note;
            $db->insert($child);
        }

        $rows = $db->from(CompParent::class)
            ->orderBy('aId')
            ->orderBy('bId')
            ->limit(2)
            ->include('children')
            ->fetch(FetchMode::SubSelect)
            ->toList();

        self::assertSame(['p11', 'p12'], array_map(static fn (CompParent $p): string => $p->label, $rows));
        self::assertSame([['x', 'y'], ['z']], array_map(
            static fn (CompParent $p): array => array_map(static fn (CompChild $c): string => $c->note, $p->children),
            $rows,
        ));
    }

    #[Test]
    public function every_fetch_mode_loads_the_same_graph(): void
    {
        $db = $this->open();
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

    // --- the calendar tokens on native columns ------------------------------------------------------

    #[Test]
    public function date_time_and_datetimeoffset_tokens_round_trip_through_their_native_columns(): void
    {
        $db = $this->open();
        $db->createTable(CalendarRow::class);

        $row = new CalendarRow();
        $row->day = new DateTimeImmutable('2026-08-28T23:30:00', new DateTimeZone('-05:00'));       // still the 28th, not the 29th in UTC
        $row->clock = new DateTimeImmutable('2026-01-01T13:30:15.250000', new DateTimeZone('+09:00'));
        $row->atOffset = new DateTimeImmutable('2026-08-28T12:00:00', new DateTimeZone('+02:00'));
        $db->insert($row);

        $stored = $db->connection()->query('select day::text, clock::text from calendar_rows')->fetch(PDO::FETCH_NUM);
        self::assertSame(['2026-08-28', '13:30:15.25'], $stored);

        $loaded = $db->get(CalendarRow::class, $row->id);
        self::assertSame('2026-08-28', $loaded->day->format('Y-m-d'));
        self::assertSame('13:30:15.250000', $loaded->clock->format('H:i:s.u'));
        self::assertSame($row->atOffset->getTimestamp(), $loaded->atOffset->getTimestamp());   // timestamptz keeps the instant, not the offset

        // The calendar values compare natively in criteria too (the parameter's type is inferred from the column).
        $matches = $db->from(CalendarRow::class)->where(Criteria::eq('day', new DateTimeImmutable('2026-08-28', new DateTimeZone('UTC'))))->toList();
        self::assertCount(1, $matches);
    }

    // --- materialized views -----------------------------------------------------------------------

    #[Test]
    public function a_materialized_view_is_created_and_read(): void
    {
        $db = $this->open();
        SampleDatabase::insertUser($db, 'Ada', 'ada1@example.com');
        SampleDatabase::insertUser($db, 'Ada', 'ada2@example.com');
        SampleDatabase::insertUser($db, 'Grace', 'grace@example.com');

        $db->createView(NameCount::class);
        $db->createView(NameCount::class);   // idempotent: `if not exists`

        $rows = $db->queryAll(NameCount::class);
        self::assertSame([['Ada', 2], ['Grace', 1]], array_map(static fn (NameCount $r): array => [$r->userName, $r->userCount], $rows));

        $kind = $db->connection()->query("select relkind from pg_class where relname = 'user_name_counts'")?->fetchColumn();
        self::assertSame('m', $kind);
    }

    // --- migrations -----------------------------------------------------------------------------------

    #[Test]
    public function migrations_run_up_rerun_and_down_under_the_advisory_lock(): void
    {
        $connection = $this->raw();
        $dialect = new PostgresDialect();
        $runner = new MigrationRunner($connection, $dialect, new EntityMapLoader(), MigrationSet::of(
            new SqlVersion(1, new SqlVersionStep(
                'pg_widgets',
                'create',
                [
                    'create table "pg_widgets" ("id" bigint generated by default as identity primary key, "name" text not null)',
                    'create index "ix_pg_widgets_name" on "pg_widgets" ("name")',
                ],
                ['drop table "pg_widgets"'],
            )),
            new SqlVersion(2, new SqlVersionStep(
                'pg_widgets',
                'add note',
                ['alter table "pg_widgets" add column "note" text'],
                ['alter table "pg_widgets" drop column "note"'],
            )),
        ));

        self::assertTrue($runner->hasPending());
        self::assertSame(2, $runner->migrate());
        self::assertSame(0, $runner->migrate());
        self::assertFalse($runner->hasPending());
        self::assertSame([MigrationState::Applied, MigrationState::Applied], array_map(static fn ($s) => $s->state, $runner->status()));

        $rows = $connection->query('select version, object, description, length(checksum) as c, execution_ms from schema_version order by version')->fetchAll();
        self::assertSame([1, 2], array_map(static fn (array $r): int => $r['version'], $rows));
        self::assertSame(['pg_widgets', 'pg_widgets'], array_column($rows, 'object'));
        self::assertSame([64, 64], array_column($rows, 'c'));
        self::assertSame(1, $connection->query("select count(*) from information_schema.columns where table_name = 'pg_widgets' and column_name = 'note'")->fetchColumn());

        self::assertSame(1, $runner->migrateDown(1));
        self::assertSame(0, $connection->query("select count(*) from information_schema.columns where table_name = 'pg_widgets' and column_name = 'note'")->fetchColumn());
        self::assertSame(1, $connection->query('select count(*) from schema_version')->fetchColumn());

        self::assertSame(1, $runner->migrateDown(0));
        self::assertNull($connection->query("select to_regclass('pg_widgets')")->fetchColumn());
        self::assertSame(0, $connection->query('select count(*) from schema_version')->fetchColumn());
    }

    #[Test]
    public function a_failing_migration_applies_nothing_because_postgres_ddl_is_transactional(): void
    {
        $connection = $this->raw();
        $runner = new MigrationRunner($connection, new PostgresDialect(), new EntityMapLoader(), MigrationSet::of(
            new SqlVersion(1, new SqlVersionStep('pg_ok', 'create', ['create table "pg_ok" ("id" bigint primary key)'], ['drop table "pg_ok"'])),
            new SqlVersion(2, new SqlVersionStep('pg_bad', 'fails', ['create table "pg_bad" ("id" no_such_type)'], [])),
        ));

        self::assertSame('MIG-021', self::codeOf(static fn () => $runner->migrate()));

        self::assertNull($connection->query("select to_regclass('pg_ok')")->fetchColumn());   // version 1 was rolled back with it
        self::assertSame(0, $connection->query("select count(*) from pg_tables where tablename = 'schema_version'")->fetchColumn());
    }

    #[Test]
    public function the_run_lock_is_exclusive_across_connections_and_released_on_rollback(): void
    {
        $dialect = new PostgresDialect();
        $holder = $this->raw();
        $other = $this->raw();
        $probe = 'select pg_try_advisory_xact_lock(1399418224, 1818578802)';

        $dialect->beginMigrationRunLock($holder);
        $other->exec('BEGIN');
        self::assertFalse((bool) $other->query($probe)->fetchColumn());
        $other->exec('ROLLBACK');

        $holder->exec('ROLLBACK');
        $other->exec('BEGIN');
        self::assertTrue((bool) $other->query($probe)->fetchColumn());
        $other->exec('ROLLBACK');
    }

    // --- tracking and the unit of work --------------------------------------------------------------------

    #[Test]
    public function a_tracked_session_flushes_a_graph_and_the_audit_stamps_land(): void
    {
        $inserted = self::utc('2026-08-29T00:00:00Z');
        $db = $this->open(new DbOptions(new PostgresDialect(), tracking: TrackingMode::Tracked, clock: static fn (): DateTimeImmutable => $inserted));

        $user = new User();
        $user->name = 'Ada';
        $user->email = 'ada@example.com';
        $db->persist($user);
        $db->flush();
        self::assertGreaterThan(0, $user->id);
        self::assertSame('2026-08-29T00:00:00Z', $user->createdAtUtc->format('Y-m-d\TH:i:s\Z'));

        $transaction = new Transaction();
        $transaction->userId = $user->id;
        $transaction->amount = Decimal::of('19.99');
        (new ReflectionProperty(Transaction::class, 'details'))->setValue($transaction, [self::detail('A'), self::detail('B')]);
        $db->persist($transaction);
        $summary = $db->flush();

        self::assertSame(3, $summary->inserted);   // the transaction and its two cascaded details

        $user->displayName = 'Countess Ada';
        $later = $db->flush();
        self::assertSame(1, $later->updated);

        $observer = Db::open(self::database()->connectionString(), new DbOptions(new PostgresDialect()));
        $this->sessions[] = $observer;
        $read = $observer->get(User::class, $user->id);
        self::assertSame('Countess Ada', $read->displayName);
        self::assertNotNull($read->updatedAtUtc);
        self::assertCount(2, $observer->queryAll(TransactionDetail::class));
        self::assertSame(TransactionStatus::Pending, $observer->get(Transaction::class, $transaction->id)->status);
        self::assertSame('19.99', (string) $observer->get(Transaction::class, $transaction->id)->amount);
    }

    private static function detail(string $description): TransactionDetail
    {
        $detail = new TransactionDetail();
        $detail->description = $description;
        $detail->quantity = 1;
        $detail->unitPrice = Decimal::of('2.50');

        return $detail;
    }

    // --- SchemaGuard -------------------------------------------------------------------------------------

    #[Test]
    public function schema_guard_validates_the_registry_and_entities_against_the_live_schema(): void
    {
        $db = $this->open();
        $db->createTable(OrderRow::class);

        SchemaGuard::validate($db, [
            PostgresRegistry::class,
            User::class,
            Role::class,
            UserRole::class,
            UserProfile::class,
            Transaction::class,
            TransactionDetail::class,
            OrderRow::class,
        ]);

        $this->addToAssertionCount(1);
    }

    #[Test]
    public function schema_guard_reports_a_val_011_mismatch_and_keeps_validating_after_it(): void
    {
        $db = $this->open();

        try {
            SchemaGuard::validate($db, [BadPostgresRegistry::class, PostgresRegistry::class, User::class]);
            self::fail('expected SchemaValidationException');
        } catch (SchemaValidationException $exception) {
            $codes = array_map(static fn ($e): string => $e->code, $exception->errors);
            self::assertContains('VAL-011', $codes);
            self::assertSame(['VAL-011'], array_values(array_unique($codes)));   // nothing spurious after it
        }
    }

    #[Test]
    public function schema_guard_survives_a_failing_statement_without_poisoning_the_rest(): void
    {
        $db = $this->open();

        try {
            SchemaGuard::validate($db, [BrokenSqlRegistry::class, PostgresRegistry::class]);
            self::fail('expected SchemaValidationException');
        } catch (SchemaValidationException $exception) {
            $codes = array_map(static fn ($e): string => $e->code, $exception->errors);
            // The two broken entries, and only them: the aborted transaction did not spill over to `fine` or to the next registry.
            self::assertSame(['VAL-001', 'VAL-001'], $codes);
        }
    }
}
