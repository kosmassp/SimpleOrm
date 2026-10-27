<?php

declare(strict_types=1);

namespace SimpleOrm\Session;

use InvalidArgumentException;
use PDO;
use SimpleOrm\Parameters\PdoBinder;
use SimpleOrm\Parameters\SqlPlaceholders;

/**
 * The one place `DbOptions::$commandTimeout` reaches the database (ADR-0047, spec/session.md "Resilience"). PDO has no
 * command object to stamp — every PDO driver keeps its timeout **on the connection** — so the funnel is the connection
 * itself: the session applies the option once, right after the open succeeds ({@see Db::open()}), and the shadow does the
 * same for the throwaway connection it opens; every statement sent on that connection afterwards — registry entries,
 * generated writes in every realization, key reads, criteria, loading, DDL, the migration runner, SchemaGuard, the
 * shadow's replay, the force sync — carries it, so no path can miss it (CODING-STANDARD §10). Per driver
 * (`PDO::ATTR_DRIVER_NAME`):
 *
 * - `sqlsrv`: `PDO::SQLSRV_ATTR_QUERY_TIMEOUT` on the connection, which every statement created afterwards inherits —
 *   a client-side attribute, no round trip; a fired timeout is the driver's `HYT00` "Query timeout expired";
 * - `pgsql`: the session's `statement_timeout`, set once with `select set_config('statement_timeout', @value, false)` —
 *   the one statement the option sends, at open, not logged (connection setup, like transaction control); a fired
 *   timeout is the engine's `57014`, and the session's next statement runs;
 * - `sqlite`: `PDO::ATTR_TIMEOUT`, the busy timeout — SQLite has no statement timeout, and the reference's driver applies
 *   its command timeout only to waiting on a lock, which is exactly this; a fired one is `SQLITE_BUSY` ("database is
 *   locked");
 * - `odbc` (and any other driver): **refused at open** with an `InvalidArgumentException` naming the driver — `pdo_odbc`
 *   exposes no query timeout (no `SQL_ATTR_QUERY_TIMEOUT` attribute, no connection keyword), and silently running
 *   statements without the bound the application asked for would be the silent default §2 forbids.
 *
 * Unset (null), nothing is touched: every driver keeps its own default.
 */
final class CommandTimeout
{
    private function __construct()
    {
    }

    /**
     * The option's value in whole seconds: rounded up, at least 1 — `0.2` → 1, `1.5` → 2, `0` → 1, never a driver's
     * "no limit".
     */
    public static function seconds(int|float $timeout): int
    {
        return max(1, (int) ceil($timeout));
    }

    /** The argument rule checked before any attempt: null, or a finite number of seconds that is not negative. */
    public static function validate(int|float|null $timeout): void
    {
        if ($timeout === null) {
            return;
        }

        if (is_float($timeout) && (is_nan($timeout) || is_infinite($timeout))) {
            throw new InvalidArgumentException('commandTimeout must be a finite number of seconds or null');
        }

        if ($timeout < 0) {
            throw new InvalidArgumentException("commandTimeout must not be negative, got {$timeout}");
        }

        if ($timeout > PHP_INT_MAX - 1) {
            throw new InvalidArgumentException("commandTimeout is too large, got {$timeout}");
        }
    }

    /** Applies the option to a connection that just opened; null leaves the driver's default untouched. */
    public static function applyTo(PDO $connection, int|float|null $timeout): void
    {
        if ($timeout === null) {
            return;
        }

        $seconds = self::seconds($timeout);
        $driver = (string) $connection->getAttribute(PDO::ATTR_DRIVER_NAME);
        switch ($driver) {
            case 'sqlite':
                $connection->setAttribute(PDO::ATTR_TIMEOUT, $seconds);

                return;
            case 'pgsql':
                $sql = "select set_config('statement_timeout', @value, false)";
                $statement = $connection->prepare(SqlPlaceholders::toPdo($sql));
                PdoBinder::bindAndExecute($statement, ['value' => $seconds . 's']);
                $statement->closeCursor();

                return;
            case 'sqlsrv':
                if (defined('PDO::SQLSRV_ATTR_QUERY_TIMEOUT')) {
                    $connection->setAttribute((int) constant('PDO::SQLSRV_ATTR_QUERY_TIMEOUT'), $seconds);

                    return;
                }

                break;
        }

        throw new InvalidArgumentException(
            "commandTimeout cannot be applied through the '{$driver}' PDO driver: it exposes no statement timeout"
                . ($driver === 'odbc'
                    ? ' (pdo_odbc has no SQL_ATTR_QUERY_TIMEOUT attribute); connect through pdo_sqlsrv (a sqlsrv: DSN) for a'
                        . ' command timeout on SQL Server,'
                    : ';')
                . ' or leave commandTimeout unset',
        );
    }
}
