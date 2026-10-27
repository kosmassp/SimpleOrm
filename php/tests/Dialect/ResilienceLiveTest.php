<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Dialect;

use InvalidArgumentException;
use PDOException;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use SimpleOrm\Dialect\PostgresDialect;
use SimpleOrm\Dialect\SqlServerDialect;
use SimpleOrm\Session\Command;
use SimpleOrm\Session\Db;
use SimpleOrm\Session\DbOptions;
use SimpleOrm\Session\EmptyArgs;
use SimpleOrm\Session\Query;
use SimpleOrm\Tests\Support\FlakyOpenDialect;
use SimpleOrm\Tests\Support\LiveServer;
use SimpleOrm\Tests\Support\PostgresDatabase;
use SimpleOrm\Tests\Support\SqlServerDatabase;
use Throwable;

/**
 * The resilience legs of the testing contract (ADR-0047, spec/dialects.md "Testing contract") on real servers, skipped
 * where none answers: a statement outrunning a one-second `commandTimeout` surfaces the driver's or the engine's own error
 * and the session's next statement runs; on PostgreSQL an unreachable port is attempted `openRetries + 1` times and a
 * missing database once (the attempts counted by {@see FlakyOpenDialect} around the real dialect). The statements change
 * no data, so they run on the admin connection's database. SQL Server through `pdo_odbc` has no statement timeout:
 * there the leg proves the refusal at open instead (CODING-STANDARD §10).
 */
final class ResilienceLiveTest extends TestCase
{
    /** @var list<Db> */
    private array $sessions = [];

    protected function tearDown(): void
    {
        foreach ($this->sessions as $db) {
            $db->close();
        }
    }

    private function open(string $dsn, DbOptions $options): Db
    {
        $db = Db::open($dsn, $options);
        $this->sessions[] = $db;

        return $db;
    }

    private static function one(Db $db): int
    {
        return $db->querySingle(Query::inline(EmptyArgs::class, 'int', 'select 1 as n -- notnull: n'), EmptyArgs::value());
    }

    /** Runs the statement and returns its failure with the seconds it took, or fails the test when it ran. */
    private static function failureOf(callable $statement): array
    {
        $start = microtime(true);
        try {
            $statement();
        } catch (Throwable $failure) {
            return [$failure, microtime(true) - $start];
        }

        self::fail('the statement completed under the timeout');
    }

    // --- PostgreSQL ------------------------------------------------------------------------------

    #[Test]
    public function postgres_a_statement_outrunning_the_timeout_is_the_engines_57014_and_the_next_statement_runs(): void
    {
        PostgresDatabase::skipUnlessAvailable();
        $db = $this->open((string) PostgresDatabase::adminDsn(), new DbOptions(new PostgresDialect(), commandTimeout: 1));

        self::assertSame('1s', $db->querySingle(Query::inline(EmptyArgs::class, 'string', 'select current_setting(\'statement_timeout\') as t -- notnull: t'), EmptyArgs::value()));

        [$failure, $elapsed] = self::failureOf(static fn () => $db->execute(Command::inline(EmptyArgs::class, 'select pg_sleep(3)'), EmptyArgs::value()));

        self::assertInstanceOf(PDOException::class, $failure);
        self::assertSame('57014', $failure->errorInfo[0] ?? null);
        self::assertLessThan(2.9, $elapsed);
        self::assertSame(1, self::one($db));
    }

    #[Test]
    public function postgres_unset_the_servers_statement_timeout_is_untouched(): void
    {
        PostgresDatabase::skipUnlessAvailable();
        $dsn = (string) PostgresDatabase::adminDsn();
        $server = LiveServer::connect($dsn)->query("select current_setting('statement_timeout')")->fetchColumn();

        $db = $this->open($dsn, new DbOptions(new PostgresDialect()));

        self::assertSame($server, $db->querySingle(Query::inline(EmptyArgs::class, 'string', 'select current_setting(\'statement_timeout\') as t -- notnull: t'), EmptyArgs::value()));
    }

    #[Test]
    public function postgres_an_unreachable_port_is_attempted_open_retries_plus_one_times(): void
    {
        PostgresDatabase::skipUnlessAvailable();
        $dsn = LiveServer::withDsnValue(LiveServer::withDsnValue((string) PostgresDatabase::adminDsn(), 'host', '127.0.0.1'), 'port', '1');
        $dialect = new FlakyOpenDialect(new PostgresDialect());

        try {
            $this->open($dsn . ';connect_timeout=2', new DbOptions($dialect, openRetries: 2, openRetryDelay: 0.1));
            self::fail('an open of port 1 succeeded');
        } catch (PDOException $failure) {
            self::assertSame('08006', $failure->errorInfo[0] ?? null);   // the driver's failure, as raised
        }

        self::assertSame(3, $dialect->attempts);
    }

    #[Test]
    public function postgres_a_missing_database_is_attempted_once(): void
    {
        PostgresDatabase::skipUnlessAvailable();
        $dsn = LiveServer::withDsnValue((string) PostgresDatabase::adminDsn(), 'dbname', 'simpleorm_missing_' . bin2hex(random_bytes(6)));
        $dialect = new FlakyOpenDialect(new PostgresDialect());

        try {
            $this->open($dsn, new DbOptions($dialect, openRetries: 2, openRetryDelay: 0.1));
            self::fail('an open of a missing database succeeded');
        } catch (PDOException $failure) {
            self::assertStringContainsString('does not exist', $failure->getMessage());   // 3D000, as libpq reports it
        }

        self::assertSame(1, $dialect->attempts);
    }

    // --- SQL Server ------------------------------------------------------------------------------

    #[Test]
    public function sql_server_a_statement_outrunning_the_timeout_is_the_drivers_timeout_and_the_next_statement_runs(): void
    {
        SqlServerDatabase::skipUnlessAvailable();
        $dsn = (string) SqlServerDatabase::adminDsn();
        if (!str_starts_with(strtolower($dsn), 'sqlsrv:')) {
            self::markTestSkipped('the command timeout needs pdo_sqlsrv (a sqlsrv: DSN); through pdo_odbc it is refused at open');
        }

        $db = $this->open($dsn, new DbOptions(new SqlServerDialect(), commandTimeout: 1));

        [$failure, $elapsed] = self::failureOf(static fn () => $db->execute(Command::inline(EmptyArgs::class, "waitfor delay '00:00:03'"), EmptyArgs::value()));

        self::assertInstanceOf(PDOException::class, $failure);
        self::assertSame('HYT00', $failure->errorInfo[0] ?? null);   // "Query timeout expired" — SqlClient's -2
        self::assertLessThan(2.9, $elapsed);
        self::assertSame(1, self::one($db));
    }

    #[Test]
    public function sql_server_through_pdo_odbc_refuses_the_command_timeout_at_open(): void
    {
        SqlServerDatabase::skipUnlessAvailable();
        $dsn = (string) SqlServerDatabase::adminDsn();
        if (!str_starts_with(strtolower($dsn), 'odbc:')) {
            self::markTestSkipped('the refusal is pdo_odbc\'s; this server is reached through another driver');
        }

        try {
            $this->open($dsn, new DbOptions(new SqlServerDialect(), commandTimeout: 1));
            self::fail('pdo_odbc took a command timeout');
        } catch (InvalidArgumentException $refusal) {
            self::assertStringContainsString("'odbc'", $refusal->getMessage());
        }

        self::assertSame(1, self::one($this->open($dsn, new DbOptions(new SqlServerDialect()))));   // unset, the same DSN opens
    }
}
