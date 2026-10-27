<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Dialect;

use PDOException;
use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use RuntimeException;
use SimpleOrm\Dialect\PostgresDialect;
use SimpleOrm\Dialect\SqlServerDialect;

/**
 * The dialects' answer to "is this open failure transient?" (ADR-0047, spec/dialects.md "Transient open failures"),
 * pinned on `PDOException`s in the shapes the PHP drivers raise them (CODING-STANDARD §10): `pdo_pgsql`'s connect failure
 * (always `08006`, libpq's message), a server SQLSTATE in `errorInfo`, `pdo_odbc`'s constructor form (no `errorInfo`, the
 * state and the native number in the message), `pdo_sqlsrv`'s `errorInfo`; directly and wrapped in a cause chain. The real
 * failures of a live server are in `ResilienceLiveTest`.
 */
final class TransientFailureTest extends TestCase
{
    /** A `PDOException` as `pdo_pgsql`/`pdo_sqlsrv` fill it: `errorInfo` = [SQLSTATE, native code, message]. */
    private static function withInfo(string $state, int $native, string $message): PDOException
    {
        $failure = new PDOException("SQLSTATE[{$state}] [{$native}] {$message}", $native);
        $failure->errorInfo = [$state, $native, $message];

        return $failure;
    }

    /** `pdo_odbc`'s constructor failure: no `errorInfo`, `getCode()` the native number, a fixed message prefix. */
    private static function odbcConnect(string $state, int $native, string $text): PDOException
    {
        return new PDOException("SQLSTATE[{$state}] SQLDriverConnect: {$native} [Microsoft][ODBC Driver 17 for SQL Server]{$text}", $native);
    }

    private static function libpq(string $failure): PDOException
    {
        return self::withInfo('08006', 7, "connection to server at \"localhost\" (::1), port 5432 failed: {$failure}");
    }

    // --- PostgreSQL ------------------------------------------------------------------------------

    /** @return iterable<string, array{PDOException, bool}> */
    public static function postgresFailures(): iterable
    {
        yield 'refused socket' => [self::libpq("Connection refused (0x0000274D/10061)\n\tIs the server running on that host and accepting TCP/IP connections?"), true];
        yield 'connect timeout' => [self::libpq('timeout expired'), true];
        yield 'unknown host' => [self::withInfo('08006', 7, 'could not translate host name "nosuch.invalid" to address: Unknown host'), true];
        yield 'too many clients (53300 as FATAL)' => [self::libpq('FATAL:  sorry, too many clients already'), true];
        yield 'reserved slots (53300 as FATAL)' => [self::libpq('FATAL:  remaining connection slots are reserved for roles with the SUPERUSER attribute'), true];
        yield 'starting up (57P03 as FATAL)' => [self::libpq('FATAL:  the database system is starting up'), true];
        yield 'in recovery (57P03 as FATAL)' => [self::libpq('FATAL:  the database system is in recovery mode'), true];
        yield 'missing database (3D000 as FATAL)' => [self::libpq('FATAL:  database "nope" does not exist'), false];
        yield 'failed password (28P01 as FATAL)' => [self::libpq('FATAL:  password authentication failed for user "postgres"'), false];
        yield 'server state 53300' => [self::withInfo('53300', 7, 'ERROR: too many connections'), true];
        yield 'server state 57P03' => [self::withInfo('57P03', 7, 'ERROR: cannot connect now'), true];
        yield 'server state 08P01' => [self::withInfo('08P01', 7, 'ERROR: protocol violation'), true];
        yield 'server state 3D000' => [self::withInfo('3D000', 7, 'ERROR: database does not exist'), false];
        yield 'statement timeout 57014' => [self::withInfo('57014', 7, 'ERROR:  canceling statement due to statement timeout'), false];
        yield 'serialization 40001' => [self::withInfo('40001', 7, 'ERROR: could not serialize access'), false];
        yield 'not a PDO failure' => [new PDOException('something else'), false];
    }

    #[Test]
    #[DataProvider('postgresFailures')]
    public function postgres_answers_from_the_sqlstate_and_the_server_fatal(PDOException $failure, bool $transient): void
    {
        self::assertSame($transient, (new PostgresDialect())->isTransient($failure));
        self::assertSame($transient, (new PostgresDialect())->isTransient(new RuntimeException('wrapped', 0, new RuntimeException('again', 0, $failure))));
    }

    // --- SQL Server ------------------------------------------------------------------------------

    /** @return iterable<string, array{PDOException, bool}> */
    public static function sqlServerFailures(): iterable
    {
        yield 'odbc: refused (08001, 10061)' => [self::odbcConnect('08001', 10061, 'TCP Provider: No connection could be made because the target machine actively refused it.'), true];
        yield 'odbc: unknown host (08001, 11001)' => [self::odbcConnect('08001', 11001, 'TCP Provider: No such host is known.'), true];
        yield 'odbc: link failure (08S01)' => [self::odbcConnect('08S01', 0, 'Communication link failure'), true];
        yield 'odbc: login timeout (HYT00)' => [self::odbcConnect('HYT00', 0, 'Login timeout expired'), true];
        yield 'odbc: login failed (28000, 18456)' => [self::odbcConnect('28000', 18456, "Login failed for user 'sa'."), false];
        yield 'odbc: no driver (IM002)' => [new PDOException('SQLSTATE[IM002] SQLDriverConnect: 0 [Microsoft][ODBC Driver Manager] Data source name not found and no default driver specified', 0), false];
        yield 'sqlsrv: database unavailable (4060)' => [self::withInfo('42000', 4060, 'Cannot open database "x" requested by the login.'), true];
        yield 'sqlsrv: Azure busy (40501)' => [self::withInfo('42000', 40501, 'The service is currently busy.'), true];
        yield 'sqlsrv: connect timeout (-2)' => [self::withInfo('HYT00', -2, 'Timeout expired'), true];
        yield 'sqlsrv: login failed (18456)' => [self::withInfo('28000', 18456, "Login failed for user 'sa'."), false];
        yield 'sqlsrv: deadlock (1205, a statement failure)' => [self::withInfo('40001', 1205, 'deadlock victim'), false];
    }

    #[Test]
    #[DataProvider('sqlServerFailures')]
    public function sql_server_answers_from_the_native_numbers_and_the_odbc_states(PDOException $failure, bool $transient): void
    {
        self::assertSame($transient, (new SqlServerDialect())->isTransient($failure));
        self::assertSame($transient, (new SqlServerDialect())->isTransient(new RuntimeException('wrapped', 0, $failure)));
    }

    #[Test]
    public function sql_server_reads_every_error_record_the_failure_carries(): void
    {
        $flattened = self::withInfo('28000', 18456, 'Login failed');
        $flattened->errorInfo = ['42000', 4099, 'first', '42000', 4060, 'Cannot open database'];
        $nested = self::withInfo('28000', 18456, 'Login failed');
        $nested->errorInfo = ['42000', 4099, 'first', ['42000', 40613, 'Database not currently available']];

        self::assertTrue((new SqlServerDialect())->isTransient($flattened));
        self::assertTrue((new SqlServerDialect())->isTransient($nested));
    }

    #[Test]
    public function the_sql_server_set_is_the_specs_twenty_six_numbers(): void
    {
        self::assertSame(
            [-2, 20, 64, 121, 233, 997, 4060, 4221, 10053, 10054, 10060, 10928, 10929, 11001, 17197, 18401,
                40143, 40197, 40501, 40540, 40613, 42108, 42109, 49918, 49919, 49920],
            SqlServerDialect::TRANSIENT_ERROR_NUMBERS,
        );
        self::assertNotContains(18456, SqlServerDialect::TRANSIENT_ERROR_NUMBERS);
        self::assertNotContains(1205, SqlServerDialect::TRANSIENT_ERROR_NUMBERS);
    }
}
