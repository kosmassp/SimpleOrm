<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Session;

use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use RuntimeException;
use SimpleOrm\Dialect\SqliteDialect;
use SimpleOrm\Errors\SimpleOrmException;
use SimpleOrm\Parameters\BinaryParameter;
use SimpleOrm\Query\Criteria;
use SimpleOrm\Query\FetchMode;
use SimpleOrm\Session\BatchingMode;
use SimpleOrm\Session\Command;
use SimpleOrm\Session\Db;
use SimpleOrm\Session\DbOptions;
use SimpleOrm\Session\EmptyArgs;
use SimpleOrm\Session\Query;
use SimpleOrm\Session\StatementLog;
use SimpleOrm\Session\TrackingMode;
use SimpleOrm\Tests\Sample\Models\DailySales;
use SimpleOrm\Tests\Sample\Models\DailySalesArgs;
use SimpleOrm\Tests\Sample\Models\Role;
use SimpleOrm\Tests\Sample\Models\Transaction;
use SimpleOrm\Tests\Sample\Models\User;
use SimpleOrm\Tests\Sample\Models\UserTransactionTotal;
use SimpleOrm\Tests\Session\Fixtures\LoggedRegistry;
use SimpleOrm\Tests\Support\ConformanceClock;
use SimpleOrm\Tests\Support\DelegatingDialect;
use SimpleOrm\Tests\Support\SampleDatabase;
use SimpleOrm\Tests\Support\SetDisplayNameArgs;
use SimpleOrm\Tests\Support\TempDatabase;
use SimpleOrm\Validation\SchemaGuard;

/**
 * The statement log (Level 4 item 4, ADR-0045, spec/session.md "Statement log") on a real temp-file SQLite database: every
 * execution path of the session writes exactly one line per statement, named by what issued it, as the statement is sent;
 * a flush and an explicit batch write one per statement in order under `Off` and `Auto`; parameter values appear only when
 * asked; nothing is formatted when the option is unset; a statement refused before it is sent writes nothing. The migration
 * runner, SchemaGuard, the shadow and the force-sync are proved in `StatementLogToolingTest`, the sink and the CLI flag in
 * `StatementLogSinkTest`.
 */
final class StatementLogTest extends TestCase
{
    private TempDatabase $fixture;

    private ?Db $db = null;

    /** @var list<string> the lines the session wrote, in order */
    private array $lines = [];

    protected function setUp(): void
    {
        $this->fixture = TempDatabase::create();
    }

    protected function tearDown(): void
    {
        $this->db?->close();
        $this->fixture->delete();
    }

    private function open(
        TrackingMode $tracking = TrackingMode::Untracked,
        BatchingMode $batching = BatchingMode::Off,
        bool $values = false,
        ?DelegatingDialect $dialect = null,
    ): Db {
        $this->db = SampleDatabase::open($this->fixture, new DbOptions(
            $dialect ?? new SqliteDialect(),
            tracking: $tracking,
            clock: ConformanceClock::fixed(),
            batching: $batching,
            log: function (string $line): void {
                $this->lines[] = $line;
            },
            logParameterValues: $values,
        ));

        return $this->db;
    }

    /** Drops the lines written so far (the schema's own DDL); returns the session. */
    private function quiet(): Db
    {
        $this->lines = [];

        return $this->db ?? throw new \LogicException('open() first');
    }

    /** @return list<string> the names of the lines written since the last call, then cleared */
    private function names(): array
    {
        $names = array_map(static fn (string $line): string => explode(': ', $line, 2)[0], $this->lines);
        $this->lines = [];

        return $names;
    }

    private static function role(string $name): Role
    {
        $role = new Role();
        $role->name = $name;

        return $role;
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

    // --- the option ------------------------------------------------------------------------------

    #[Test]
    public function the_log_is_unset_and_values_are_off_by_default(): void
    {
        $options = new DbOptions(new SqliteDialect());

        self::assertNull($options->log);
        self::assertFalse($options->logParameterValues);
    }

    #[Test]
    public function nothing_is_formatted_when_the_log_is_unset_and_values_only_when_asked(): void
    {
        $bomb = new class implements \Stringable {
            public function __toString(): string
            {
                throw new RuntimeException('formatted');
            }
        };

        StatementLog::off()->write('x', 'select 1', ['a' => $bomb]);   // unset: nothing formatted, nothing written
        $names = new StatementLog(static function (string $line) use (&$written): void {
            $written = $line;
        });
        $names->write('x', 'select @a', ['a' => $bomb]);   // set, values off: the value is never formatted
        self::assertSame('x: select @a [@a]', $written);

        $this->expectException(RuntimeException::class);
        (new StatementLog(static function (string $line): void {
        }, true))->write('x', 'select @a', ['a' => $bomb]);
    }

    // --- one line, one name, per path ------------------------------------------------------------

    #[Test]
    public function metadata_ddl_writes_one_line_per_statement_named_by_entity_and_kind(): void
    {
        $this->open();   // the schema is created with the log already on
        $names = $this->names();

        self::assertContains('User.createTable', $names);
        self::assertContains('User.createIndex', $names);
        self::assertContains('Transaction.createIndex', $names);
        self::assertContains('UserTransactionTotal.createView', $names);
        self::assertSame(['User.createIndex' => 2], array_intersect_key(array_count_values($names), ['User.createIndex' => 1]));

        $db = $this->quiet();
        $db->createTriggers(Role::class);
        $names = $this->names();
        self::assertGreaterThanOrEqual(2, count($names));   // the drop, then the create
        self::assertSame(['Role.createTrigger'], array_values(array_unique($names)));
    }

    #[Test]
    public function every_generated_write_and_key_read_writes_one_named_line(): void
    {
        $db = $this->open();
        $db->createTriggers(Role::class);
        $this->quiet();

        $ada = SampleDatabase::insertUser($db, 'Ada', 'ada@example.com');
        self::assertSame(['User.insert'], $this->names());

        $db->get(User::class, $ada->id);
        self::assertNull($db->getOrDefault(User::class, 999));   // a miss is still a statement sent
        $db->refresh($ada);
        self::assertSame(['User.get', 'User.get', 'User.get'], $this->names());

        $ada->name = 'Ada L';
        $db->update($ada);
        self::assertSame(['User.update'], $this->names());

        $db->updateOnly($ada, ['name']);
        self::assertSame(['User.updateOnly'], $this->names());

        $db->delete(User::class, $ada->id);
        self::assertSame(['User.delete'], $this->names());

        $role = SampleDatabase::insertRole($db, 'admin');
        $this->quiet();
        $db->delete(Role::class, $role->id);   // the soft delete: the flag-setting update, named for the operation asked for
        self::assertSame(['Role.delete'], $this->names());
    }

    #[Test]
    public function the_tracked_dirty_update_is_named_update_and_a_clean_one_writes_nothing(): void
    {
        $db = $this->open(TrackingMode::Tracked);
        $ada = SampleDatabase::insertUser($db, 'Ada', 'ada@example.com');
        $this->quiet();

        $db->update($ada);   // clean: no statement
        self::assertSame([], $this->lines);

        $ada->name = 'Ada L';
        $db->update($ada);
        self::assertSame(['User.update'], $this->names());
    }

    #[Test]
    public function reads_are_named_by_the_entity_and_what_issued_them(): void
    {
        $db = $this->open();
        $ada = SampleDatabase::insertUser($db, 'Ada', 'ada@example.com');
        $admin = SampleDatabase::insertRole($db, 'admin');
        SampleDatabase::insertUserRole($db, $ada, $admin);
        SampleDatabase::insertTransaction($db, $ada, '10.00');
        $this->quiet();

        $db->from(User::class)->where(Criteria::eq('name', 'Ada'))->toList();
        self::assertSame(['User.criteria'], $this->names());

        $db->from(User::class)->include('transactions')->fetch(FetchMode::MultiQuery)->toList();
        self::assertSame(['User.criteria', 'Transaction.criteria'], $this->names());

        $db->from(User::class)->include('transactions')->fetch(FetchMode::Join)->toList();
        self::assertSame(['User.include'], $this->names());

        $users = $db->queryAll(User::class);
        self::assertSame(['User.selectAll'], $this->names());

        $db->load($users[0], 'roles');   // many-to-many: the link entity's criteria, then the target's
        self::assertSame(['UserRole.criteria', 'Role.criteria'], $this->names());

        $db->statement(DailySales::class, new DailySalesArgs(SampleDatabase::seedTime()->modify('-1 day')));
        self::assertSame(['DailySales.statement'], $this->names());

        $db->queryAll(UserTransactionTotal::class);
        self::assertSame(['UserTransactionTotal.selectAll'], $this->names());
    }

    #[Test]
    public function a_registry_entry_is_named_query_or_command_until_schema_guard_enumerates_the_registry(): void
    {
        $db = $this->open();
        $ada = SampleDatabase::insertUser($db, 'Ada', 'ada@example.com');
        $this->quiet();

        $db->querySingle(LoggedRegistry::userCount(), EmptyArgs::value());
        $db->execute(LoggedRegistry::renameUser(), new SetDisplayNameArgs($ada->id, 'A'));
        self::assertSame(['query', 'command'], $this->names());

        // The scan is where the session learns what the registry calls its entries; its own describe calls are `validate` lines.
        self::assertSame([], SchemaGuard::report($db, [LoggedRegistry::class]));
        $scan = $this->names();
        self::assertContains('validate LoggedRegistry.userCount', $scan);
        self::assertContains('validate LoggedRegistry.renameUser', $scan);

        $db->querySingle(LoggedRegistry::userCount(), EmptyArgs::value());
        foreach ($db->stream(LoggedRegistry::userCount(), EmptyArgs::value()) as $ignored) {
            // iterating is not another statement
        }

        $db->execute(LoggedRegistry::renameUser(), new SetDisplayNameArgs($ada->id, 'B'));
        self::assertSame(
            ['LoggedRegistry.userCount', 'LoggedRegistry.userCount', 'LoggedRegistry.renameUser'],
            $this->names(),
        );
    }

    #[Test]
    public function an_entry_read_from_a_file_is_named_by_its_path_when_the_registry_did_not_name_it(): void
    {
        $db = $this->open();
        $path = sys_get_temp_dir() . '/simpleorm_log_' . bin2hex(random_bytes(6)) . '.sql';
        file_put_contents($path, 'select count(id) as n from users -- notnull: n');
        $this->quiet();
        try {
            $db->querySingle(Query::fromFile(EmptyArgs::class, 'int', $path), EmptyArgs::value());
        } finally {
            unlink($path);
        }

        self::assertSame([$path], $this->names());
    }

    #[Test]
    public function a_statement_refused_before_it_is_sent_writes_nothing(): void
    {
        $db = $this->open();
        $ada = SampleDatabase::insertUser($db, 'Ada', 'ada@example.com');
        $this->quiet();

        $transaction = new Transaction();
        $transaction->userId = $ada->id + 100;
        (new \ReflectionProperty(Transaction::class, 'user'))->setValue($transaction, $ada);   // disagrees with the foreign key
        self::assertSame('CRUD-004', self::code(static fn () => $db->insert($transaction)));
        self::assertSame('CRUD-002', self::code(static fn () => $db->get(User::class, [1, 2])));
        self::assertSame('CRUD-007', self::code(static fn () => $db->updateOnly($ada, [])));
        self::assertSame(
            'PRM-001',
            self::code(static fn () => $db->execute(Command::inline(EmptyArgs::class, 'update users set name = @nope'), EmptyArgs::value())),
        );

        self::assertSame([], $this->lines);
    }

    // --- the line --------------------------------------------------------------------------------

    #[Test]
    public function a_line_is_name_sql_and_the_parameter_names_in_bind_order_and_no_values_by_default(): void
    {
        $db = $this->open();
        $this->quiet();

        SampleDatabase::insertUser($db, 'Ada', 'ada@example.com');

        self::assertCount(1, $this->lines);
        self::assertMatchesRegularExpression('/^User\.insert: insert into users \(.*\) values \(.*\) returning .* \[@name, @email.*\]$/', $this->lines[0]);
        self::assertStringNotContainsString('Ada', $this->lines[0]);
        self::assertStringNotContainsString('=', substr($this->lines[0], (int) strpos($this->lines[0], '[')));
    }

    #[Test]
    public function an_in_list_is_logged_as_expanded_and_values_appear_only_when_asked(): void
    {
        $db = $this->open(values: true);
        $db->insert(self::role("O'Brien"));
        $this->quiet();

        $db->from(Role::class)->where(Criteria::in('name', ["O'Brien", 'x']))->toList();

        self::assertCount(1, $this->lines);
        self::assertStringContainsString("[@c0 = 'O''Brien', @c1 = 'x']", $this->lines[0]);
    }

    #[Test]
    public function values_are_written_in_the_fixed_culture_neutral_form(): void
    {
        self::assertSame('null', StatementLog::value(null));
        self::assertSame('true', StatementLog::value(true));
        self::assertSame('false', StatementLog::value(false));
        self::assertSame('7', StatementLog::value(7));
        self::assertSame('1.5', StatementLog::value(1.5));
        self::assertSame("'it''s'", StatementLog::value("it's"));
        self::assertSame('[1, 2, 3]', StatementLog::value([1, 2, 3]));
        self::assertSame("['a', null]", StatementLog::value(['a', null]));
        self::assertSame('0x0102 (2 bytes)', StatementLog::value(new BinaryParameter("\x01\x02")));
        self::assertSame(
            '0x' . str_repeat('ab', 16) . ' (20 bytes)',
            StatementLog::value(new BinaryParameter(str_repeat("\xab", 20))),
        );
        self::assertSame('2026-08-28T09:30:00.0000000Z', StatementLog::value(SampleDatabase::seedTime()));
        self::assertSame('x: select @a, @b [@a = 1, @b = null]', StatementLog::line('x', 'select @a, @b', ['a' => 1, 'b' => null], true));
        self::assertSame('x: select 1', StatementLog::line('x', 'select 1', [], true));
    }

    // --- a flush and an explicit batch: one line per statement, in order --------------------------

    /** @return iterable<string, array{BatchingMode}> */
    public static function modes(): iterable
    {
        yield 'off' => [BatchingMode::Off];
        yield 'auto' => [BatchingMode::Auto];
    }

    #[Test]
    #[\PHPUnit\Framework\Attributes\DataProvider('modes')]
    public function a_flush_writes_one_line_per_statement_in_the_order_sent(BatchingMode $mode): void
    {
        $db = $this->open(TrackingMode::Tracked, $mode, values: true);
        $ada = SampleDatabase::insertUser($db, 'Ada', 'ada@example.com');
        $this->quiet();

        $a = self::role('a');
        $b = self::role('b');
        $c = self::role('c');
        $ada->name = 'Ada L';
        $db->persist($a);
        $db->persist($b);
        $db->persist($c);
        self::assertSame([], $this->lines);   // scheduling sends nothing
        $db->flush();

        self::assertSame(['Role.insert', 'Role.insert', 'Role.insert', 'User.update'], $this->names());
        self::assertSame([], $this->lines);
    }

    #[Test]
    #[\PHPUnit\Framework\Attributes\DataProvider('modes')]
    public function a_flush_logs_the_values_of_each_statement_in_statement_order(BatchingMode $mode): void
    {
        $db = $this->open(TrackingMode::Tracked, $mode, values: true);
        $this->quiet();

        foreach (['a', 'b', 'c'] as $name) {
            $db->persist(self::role($name));
        }

        $db->flush();

        self::assertCount(3, $this->lines);
        foreach (['a', 'b', 'c'] as $i => $name) {
            self::assertStringContainsString("@role_name = '{$name}'", $this->lines[$i]);
        }
    }

    #[Test]
    #[\PHPUnit\Framework\Attributes\DataProvider('modes')]
    public function an_explicit_batch_writes_one_line_per_queued_statement_when_it_is_sent(BatchingMode $mode): void
    {
        $db = $this->open(TrackingMode::Untracked, $mode);
        $ada = SampleDatabase::insertUser($db, 'Ada', 'ada@example.com');
        $old = SampleDatabase::insertRole($db, 'old');
        $this->quiet();

        $batch = $db->batch();
        $batch->insert(self::role('new'));
        $batch->updateOnly($ada, ['name']);
        $batch->execute(LoggedRegistry::renameUser(), new SetDisplayNameArgs($ada->id, 'A'));
        $batch->delete(Role::class, $old->id);
        self::assertSame([], $this->lines);   // queuing sends nothing

        $result = $batch->send();

        self::assertSame(['Role.insert', 'User.updateOnly', 'command', 'Role.delete'], $this->names());
        self::assertCount(4, $result->affected);
        self::assertSame($mode === BatchingMode::Off ? 4 : 1, $result->messages);
    }

    #[Test]
    public function a_multi_statement_message_writes_every_line_before_any_statement_of_it_runs(): void
    {
        // SQLite sends statement by statement, so the multi-statement realization (SQL Server) is forced here only as far as
        // the log is concerned: the dialect claims to batch, the session forms one text, and the lines are written for it
        // after it is prepared and before it goes out. SQLite then refuses the text — the lines are still all there.
        $dialect = new class (new SqliteDialect()) extends DelegatingDialect {
            public function supportsStatementBatching(): bool
            {
                return true;
            }
        };
        $db = $this->open(TrackingMode::Untracked, BatchingMode::Auto, dialect: $dialect);
        $this->quiet();
        self::assertTrue($db->batchingInEffect());

        $batch = $db->batch();
        $batch->insert(self::role('a'));
        $batch->insert(self::role('b'));
        $batch->insert(self::role('c'));
        try {
            $batch->send();
        } catch (\Throwable) {
            // the engine behind the test cannot take a multi-statement text; what the log said is the point
        }

        $names = $this->names();
        self::assertSame(['Role.insert', 'Role.insert', 'Role.insert'], array_slice($names, 0, 3));
    }

    // --- the sink's own failure -------------------------------------------------------------------

    #[Test]
    public function a_sink_that_throws_fails_the_call_and_nothing_is_sent(): void
    {
        $db = Db::open($this->fixture->connectionString(), new DbOptions(
            new SqliteDialect(),
            log: static function (string $line): void {
                throw new RuntimeException('the sink failed');
            },
        ));
        $this->db = $db;

        try {
            $db->createTable(Role::class);
            self::fail('the sink exception was swallowed');
        } catch (RuntimeException $e) {
            self::assertSame('the sink failed', $e->getMessage());
        }

        $plain = Db::open($this->fixture->connectionString(), new DbOptions(new SqliteDialect()));
        $tables = $plain->connection()->query("select name from sqlite_master where name = 'roles'")->fetchAll();
        self::assertSame([], $tables);   // logged before it executed: the statement never ran
        $plain->close();
    }
}
