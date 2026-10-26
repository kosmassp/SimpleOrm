<?php

declare(strict_types=1);

namespace SimpleOrm\Dialect;

use PDO;
use SimpleOrm\Migrations\TriggerSchema;
use SimpleOrm\Session\StatementLog;

/**
 * The SQL Server {@see ShadowHost} (ADR-0043): `create database [simpleorm_shadow_<hex>]` on the admin connection (a
 * `sqlsrv:` or `odbc:` PDO DSN reaching `master`), dropped afterwards after `alter database … set single_user with
 * rollback immediate` so a lingering session cannot block it. **No trigger read-back:** the stored text renders a
 * `{new: cols}` token as `inserted`, so the declaration is not recoverable — the shadow snapshots triggers from the step.
 */
final class SqlServerShadowHost implements ShadowHost
{
    public function __construct(private readonly Dialect $dialect = new SqlServerDialect())
    {
    }

    public function provision(string $adminConnectionString): string
    {
        $name = 'simpleorm_shadow_' . bin2hex(random_bytes(8));
        $this->dialect->createConnection($adminConnectionString)->exec('create database ' . $this->dialect->quoteIdentifier($name));

        return PdoDsn::withValue($adminConnectionString, 'Database', $name);
    }

    public function drop(string $adminConnectionString, string $shadowConnectionString): void
    {
        $name = self::databaseName($shadowConnectionString);
        $quoted = $this->dialect->quoteIdentifier($name);
        $this->dialect->createConnection($adminConnectionString)->exec(
            "if db_id(N'" . str_replace("'", "''", $name) . "') is not null begin "
                . "alter database {$quoted} set single_user with rollback immediate; "
                . "drop database {$quoted}; end",
        );
    }

    public function canReadTriggers(): bool
    {
        return false;
    }

    public function readTrigger(PDO $connection, string $name, ?string &$note = null, ?StatementLog $log = null): ?TriggerSchema
    {
        $note = null;

        return null;
    }

    private static function databaseName(string $dsn): string
    {
        return preg_match('/(?:^|[:;\s])Database=([^;]+)/i', $dsn, $matches) === 1 ? trim($matches[1]) : '';
    }
}
