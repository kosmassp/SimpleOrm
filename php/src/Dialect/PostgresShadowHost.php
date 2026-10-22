<?php

declare(strict_types=1);

namespace SimpleOrm\Dialect;

use PDO;
use SimpleOrm\Migrations\TriggerSchema;

/**
 * The PostgreSQL {@see ShadowHost} (ADR-0043): `create database "simpleorm_shadow_<hex>"` on the admin connection (a
 * `pgsql:` PDO DSN with CREATEDB rights), `drop database … with (force)` afterwards — `force` ends any session the
 * replay left behind — and a lossless trigger read-back: the catalog query reconstructs the function and the trigger in
 * the dialect's own spelling ({@see PostgresTriggerParser}).
 */
final class PostgresShadowHost implements ShadowHost
{
    public function __construct(private readonly Dialect $dialect = new PostgresDialect())
    {
    }

    public function provision(string $adminConnectionString): string
    {
        $name = 'simpleorm_shadow_' . bin2hex(random_bytes(8));
        $this->dialect->createConnection($adminConnectionString)->exec('create database ' . $this->dialect->quoteIdentifier($name));

        return PdoDsn::withValue($adminConnectionString, 'dbname', $name);
    }

    public function drop(string $adminConnectionString, string $shadowConnectionString): void
    {
        $name = self::databaseName($shadowConnectionString);
        $this->dialect->createConnection($adminConnectionString)
            ->exec('drop database if exists ' . $this->dialect->quoteIdentifier($name) . ' with (force)');
    }

    public function canReadTriggers(): bool
    {
        return true;
    }

    public function readTrigger(PDO $connection, string $name, ?string &$note = null): ?TriggerSchema
    {
        return PostgresTriggerParser::parse($name, PostgresTriggerParser::definitions($connection, $name), $note);
    }

    private static function databaseName(string $dsn): string
    {
        return preg_match('/(?:^|[:;\s])dbname=([^;]+)/i', $dsn, $matches) === 1 ? trim($matches[1]) : '';
    }
}
