<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Support;

/**
 * A real PostgreSQL test database (ADR-0025, spec/dialects.md "Testing
 * contract"): one database per fixture on a local server, dropped afterwards —
 * the Postgres analog of {@see TempDatabase} (never a mock). The admin DSN comes
 * from `SIMPLEORM_POSTGRES` (a `pgsql:` PDO DSN with CREATEDB rights, credentials
 * embedded) or the conventional local server; {@see self::available()} is the
 * skip gate every Postgres test consults first.
 */
final class PostgresDatabase
{
    public const ENVIRONMENT_VARIABLE = 'SIMPLEORM_POSTGRES';

    public const DEFAULT_ADMIN_DSN = 'pgsql:host=localhost;port=5432;dbname=postgres;user=postgres;password=postgres';

    private function __construct(
        private readonly string $adminDsn,
        public readonly string $name,
    ) {
    }

    public static function available(): bool
    {
        return self::adminDsn() !== null;
    }

    /** Skips the calling test where no server answers. */
    public static function skipUnlessAvailable(): void
    {
        LiveServer::skipUnless(self::adminDsn(), 'PostgreSQL is not available (set SIMPLEORM_POSTGRES or run a local server on 5432)');
    }

    /** Creates the fixture database; call {@see self::available()} first. */
    public static function create(): self
    {
        $adminDsn = self::adminDsn() ?? throw new \LogicException('PostgreSQL is not available; gate the test with available()');
        $name = LiveServer::databaseName();
        LiveServer::connect($adminDsn)->exec("create database \"{$name}\"");

        return new self($adminDsn, $name);
    }

    /** The DSN `Db::open` takes for the fixture database. */
    public function connectionString(): string
    {
        return LiveServer::withDsnValue($this->adminDsn, 'dbname', $this->name);
    }

    public function drop(): void
    {
        LiveServer::connect($this->adminDsn)->exec("drop database if exists \"{$this->name}\" with (force)");
    }

    /** The admin DSN (with the right to create databases), or null where no server answers. */
    public static function adminDsn(): ?string
    {
        return LiveServer::adminDsn(self::ENVIRONMENT_VARIABLE, self::DEFAULT_ADMIN_DSN);
    }
}
