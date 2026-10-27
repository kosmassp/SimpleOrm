<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Session;

use InvalidArgumentException;
use PDO;
use PDOException;
use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use SimpleOrm\Dialect\SqliteDialect;
use SimpleOrm\Dialect\SqliteShadowHost;
use SimpleOrm\Metadata\EntityMapLoader;
use SimpleOrm\Migrations\MigrationRunner;
use SimpleOrm\Migrations\MigrationSet;
use SimpleOrm\Migrations\SchemaShadow;
use SimpleOrm\Migrations\SqlVersion;
use SimpleOrm\Migrations\SqlVersionStep;
use SimpleOrm\Query\Criteria;
use SimpleOrm\Session\BatchingMode;
use SimpleOrm\Session\CommandTimeout;
use SimpleOrm\Session\Db;
use SimpleOrm\Session\DbOptions;
use SimpleOrm\Session\EmptyArgs;
use SimpleOrm\Session\TrackingMode;
use SimpleOrm\Tests\Sample\Models\User;
use SimpleOrm\Tests\Session\Fixtures\LoggedRegistry;
use SimpleOrm\Tests\Support\FlakyOpenDialect;
use SimpleOrm\Tests\Support\ObservedConnectionDialect;
use SimpleOrm\Tests\Support\SampleDatabase;
use SimpleOrm\Tests\Support\SetDisplayNameArgs;
use SimpleOrm\Tests\Support\TempDatabase;
use SimpleOrm\Validation\SchemaGuard;
use Throwable;

/**
 * Resilience (Level 4, ADR-0047, spec/session.md "Resilience") on real temp-file SQLite databases: the open retry through
 * {@see FlakyOpenDialect} — a decorator around the real SQLite dialect that fails the first N opens with a failure it
 * calls transient or not — and the command timeout, which on SQLite is the busy timeout (`PDO::ATTR_TIMEOUT`, the bound on
 * waiting for a lock, as the reference's driver applies its command timeout): observed on the connection through
 * {@see ObservedConnectionDialect}, and proved on every path by a second connection holding an exclusive lock, under which
 * each statement fails after the timeout instead of pdo_sqlite's 60-second default. The PostgreSQL and SQL Server legs are
 * in `ResilienceLiveTest`.
 */
final class ResilienceTest extends TestCase
{
    private TempDatabase $fixture;

    /** @var list<Db> */
    private array $sessions = [];

    protected function setUp(): void
    {
        $this->fixture = TempDatabase::create();
    }

    protected function tearDown(): void
    {
        foreach ($this->sessions as $db) {
            $db->close();
        }

        $this->fixture->delete();
    }

    private function open(DbOptions $options): Db
    {
        $db = Db::open($this->fixture->connectionString(), $options);
        $this->sessions[] = $db;

        return $db;
    }

    /** Opens and returns the failure, or fails the test when the open succeeded. */
    private function openFailure(DbOptions $options): Throwable
    {
        try {
            $this->open($options);
        } catch (Throwable $failure) {
            return $failure;
        }

        self::fail('the open succeeded');
    }

    // --- the options ---------------------------------------------------------------------------

    #[Test]
    public function the_resilience_options_are_off_by_default(): void
    {
        $options = new DbOptions(new SqliteDialect());

        self::assertNull($options->commandTimeout);
        self::assertSame(0, $options->openRetries);
        self::assertSame(1, $options->openRetryDelay);
    }

    /** @return iterable<string, array{DbOptions}> */
    public static function refusedOptions(): iterable
    {
        $dialect = new FlakyOpenDialect(new SqliteDialect());
        yield 'negative openRetries' => [new DbOptions($dialect, openRetries: -1)];
        yield 'negative openRetryDelay' => [new DbOptions($dialect, openRetryDelay: -0.5)];
        yield 'non-finite openRetryDelay' => [new DbOptions($dialect, openRetryDelay: NAN)];
        yield 'negative commandTimeout' => [new DbOptions($dialect, commandTimeout: -1)];
        yield 'non-finite commandTimeout' => [new DbOptions($dialect, commandTimeout: INF)];
    }

    #[Test]
    #[DataProvider('refusedOptions')]
    public function a_refused_option_is_the_argument_error_before_any_attempt(DbOptions $options): void
    {
        $failure = $this->openFailure($options);

        self::assertInstanceOf(InvalidArgumentException::class, $failure);
        self::assertInstanceOf(FlakyOpenDialect::class, $options->dialect);
        self::assertSame(0, $options->dialect->attempts);
    }

    #[Test]
    public function the_timeout_rounds_up_to_whole_seconds_and_is_never_below_one(): void
    {
        self::assertSame(1, CommandTimeout::seconds(0.2));
        self::assertSame(2, CommandTimeout::seconds(1.5));
        self::assertSame(1, CommandTimeout::seconds(0));
        self::assertSame(1, CommandTimeout::seconds(0.0));
        self::assertSame(3, CommandTimeout::seconds(3));
        self::assertSame(600, CommandTimeout::seconds(600));
    }

    // --- the open retry ------------------------------------------------------------------------

    #[Test]
    public function by_default_the_open_is_one_attempt(): void
    {
        $dialect = new FlakyOpenDialect(new SqliteDialect(), failures: 1);

        $failure = $this->openFailure(new DbOptions($dialect));

        self::assertSame(1, $dialect->attempts);
        self::assertSame($dialect->thrown[0], $failure);
    }

    #[Test]
    public function transient_failures_are_attempted_again_after_the_fixed_delay_until_the_open_succeeds(): void
    {
        $dialect = new FlakyOpenDialect(new SqliteDialect(), failures: 3);

        $start = microtime(true);
        $db = $this->open(new DbOptions($dialect, openRetries: 3, openRetryDelay: 0.05));
        $elapsed = microtime(true) - $start;

        self::assertSame(4, $dialect->attempts);
        self::assertGreaterThanOrEqual(0.15, $elapsed);   // three waits of 50 ms
        self::assertSame(1, (int) $db->connection()->query('select 1')->fetchColumn());
    }

    #[Test]
    public function a_zero_delay_attempts_again_at_once(): void
    {
        $dialect = new FlakyOpenDialect(new SqliteDialect(), failures: 2);

        $this->open(new DbOptions($dialect, openRetries: 2, openRetryDelay: 0));

        self::assertSame(3, $dialect->attempts);
    }

    #[Test]
    public function when_the_retries_are_spent_the_last_failure_is_thrown_as_it_was_raised(): void
    {
        $dialect = new FlakyOpenDialect(new SqliteDialect(), failures: 5);

        $failure = $this->openFailure(new DbOptions($dialect, openRetries: 2, openRetryDelay: 0));

        self::assertSame(3, $dialect->attempts);
        self::assertSame($dialect->thrown[2], $failure);   // the same object, unwrapped
    }

    #[Test]
    public function a_failure_the_dialect_does_not_call_transient_is_never_attempted_again(): void
    {
        $dialect = new FlakyOpenDialect(new SqliteDialect(), failures: 1, transient: false);

        $failure = $this->openFailure(new DbOptions($dialect, openRetries: 5, openRetryDelay: 0));

        self::assertSame(1, $dialect->attempts);
        self::assertSame($dialect->thrown[0], $failure);
    }

    #[Test]
    public function the_sqlite_dialect_never_calls_an_open_failure_transient(): void
    {
        $directory = sys_get_temp_dir() . DIRECTORY_SEPARATOR . 'simpleorm_missing_' . bin2hex(random_bytes(6));
        $dialect = new FlakyOpenDialect(new SqliteDialect());

        $failure = $this->openFailureAt($directory . DIRECTORY_SEPARATOR . 'app.db', new DbOptions($dialect, openRetries: 3, openRetryDelay: 0));

        self::assertInstanceOf(PDOException::class, $failure);   // a directory that does not exist: SQLITE_CANTOPEN
        self::assertSame(1, $dialect->attempts);
        self::assertFalse((new SqliteDialect())->isTransient($failure));
        self::assertFalse((new SqliteDialect())->isTransient(new PDOException('SQLSTATE[08006] [7] connection refused')));
    }

    private function openFailureAt(string $connectionString, DbOptions $options): Throwable
    {
        try {
            $this->sessions[] = Db::open($connectionString, $options);
        } catch (Throwable $failure) {
            return $failure;
        }

        self::fail('the open succeeded');
    }

    // --- the command timeout reaches the connection --------------------------------------------

    #[Test]
    public function unset_the_driver_default_is_untouched(): void
    {
        $dialect = new ObservedConnectionDialect(new SqliteDialect());

        $this->open(new DbOptions($dialect));

        self::assertArrayNotHasKey(PDO::ATTR_TIMEOUT, $dialect->connection?->attributes ?? []);
    }

    #[Test]
    public function set_the_connection_carries_it_in_whole_seconds_rounded_up(): void
    {
        $dialect = new ObservedConnectionDialect(new SqliteDialect());

        $this->open(new DbOptions($dialect, commandTimeout: 2.5));

        self::assertSame(3, $dialect->connection?->attributes[PDO::ATTR_TIMEOUT] ?? null);
    }

    #[Test]
    public function the_shadow_takes_the_timeout_for_its_throwaway_connection(): void
    {
        $dialect = new ObservedConnectionDialect(new SqliteDialect());
        $version = new SqlVersion(1, new SqlVersionStep('widgets', 'create', ['create table widgets (id INTEGER PRIMARY KEY, name TEXT) STRICT'], []));
        $out = sys_get_temp_dir() . DIRECTORY_SEPARATOR . 'simpleorm_shadow_out_' . bin2hex(random_bytes(6));
        mkdir($out);

        try {
            (new SchemaShadow($dialect, new SqliteShadowHost(), '', null, 0.4))
                ->rebuildSnapshots(MigrationSet::of($version), new EntityMapLoader(), $out);
        } finally {
            array_map('unlink', glob($out . DIRECTORY_SEPARATOR . '*') ?: []);
            rmdir($out);
        }

        self::assertSame(1, $dialect->connection?->attributes[PDO::ATTR_TIMEOUT] ?? null);
    }

    // --- ... and every statement the session sends --------------------------------------------

    /** @return iterable<string, array{string}> */
    public static function paths(): iterable
    {
        foreach (['query', 'stream', 'execute', 'insert', 'get', 'criteria', 'update', 'delete', 'batch', 'flush', 'migrate', 'validate'] as $path) {
            yield $path => [$path];
        }
    }

    #[Test]
    #[DataProvider('paths')]
    public function a_statement_waiting_longer_than_the_timeout_fails_with_the_drivers_error_and_the_next_one_runs(string $path): void
    {
        $tracked = $path === 'flush';
        $db = SampleDatabase::open($this->fixture, new DbOptions(
            new SqliteDialect(),
            tracking: $tracked ? TrackingMode::Tracked : TrackingMode::Untracked,
            batching: $path === 'batch' ? BatchingMode::Auto : BatchingMode::Off,
            commandTimeout: 1,
        ));
        $this->sessions[] = $db;
        $ada = SampleDatabase::insertUser($db, 'Ada', 'ada@example.com');
        if ($tracked) {
            $ada = $db->get(User::class, $ada->id);   // read before the lock: the flush is the statement under test
            self::assertInstanceOf(User::class, $ada);
            $ada->name = 'Ada F';
        }

        $holder = new PDO('sqlite:' . $this->fixture->connectionString());
        $holder->setAttribute(PDO::ATTR_ERRMODE, PDO::ERRMODE_EXCEPTION);
        $holder->exec('BEGIN EXCLUSIVE');   // every reader and writer of the file now waits for the lock

        $start = microtime(true);
        $failure = null;
        try {
            $this->runPath($db, $path, $ada);
        } catch (Throwable $caught) {
            $failure = $caught;
        } finally {
            $holder->exec('ROLLBACK');
        }

        $elapsed = microtime(true) - $start;
        self::assertNotNull($failure, "{$path}: the statement ran under the lock");
        self::assertStringContainsString('database is locked', $failure->getMessage());
        if ($path !== 'validate') {
            self::assertInstanceOf(PDOException::class, $failure);   // the driver's own error, unwrapped
        }

        self::assertGreaterThanOrEqual(0.9, $elapsed);
        self::assertLessThan(10, $elapsed);   // the timeout, not pdo_sqlite's 60-second default

        self::assertSame(1, (int) $db->connection()->query('select count(*) from users')->fetchColumn());
    }

    private function runPath(Db $db, string $path, User $ada): void
    {
        switch ($path) {
            case 'query':
                $db->query(LoggedRegistry::userCount(), EmptyArgs::value());

                return;
            case 'stream':
                foreach ($db->stream(LoggedRegistry::userCount(), EmptyArgs::value()) as $row) {
                    self::assertIsInt($row);
                }

                return;
            case 'execute':
                $db->execute(LoggedRegistry::renameUser(), new SetDisplayNameArgs($ada->id, 'A'));

                return;
            case 'insert':
                SampleDatabase::insertUser($db, 'Grace', 'grace@example.com');

                return;
            case 'get':
                $db->get(User::class, $ada->id);

                return;
            case 'criteria':
                $db->from(User::class)->where(Criteria::eq('name', 'Ada'))->toList();

                return;
            case 'update':
                $ada->name = 'Ada L';
                $db->update($ada);

                return;
            case 'delete':
                $db->delete(User::class, $ada->id);

                return;
            case 'batch':
                $batch = $db->batch();
                $batch->update($ada);
                $batch->execute(LoggedRegistry::renameUser(), new SetDisplayNameArgs($ada->id, 'A'));
                $batch->send();

                return;
            case 'flush':
                $db->flush();

                return;
            case 'migrate':
                // The first statement to wait is the run lock's `BEGIN IMMEDIATE`: in PHP the timeout is the connection's,
                // so the dialect's own statements on it carry it too (CODING-STANDARD §10).
                $version = new SqlVersion(1, new SqlVersionStep('widgets', 'create', ['create table widgets (id INTEGER PRIMARY KEY) STRICT'], []));
                (new MigrationRunner($db->connection(), $db->options()->dialect, $db->maps(), MigrationSet::of($version), log: $db->log()))->migrate();

                return;
            case 'validate':
                $errors = SchemaGuard::report($db, [LoggedRegistry::class]);
                foreach ($errors as $error) {
                    if ($error->code === 'VAL-001') {
                        throw new \RuntimeException($error->message);   // the row SchemaGuard reports for the refused describe
                    }
                }

                return;
        }

        self::fail("unknown path {$path}");
    }
}
