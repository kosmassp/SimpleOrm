<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Support;

/**
 * A real SQL Server test database (ADR-0024, spec/dialects.md "Testing
 * contract"): one database per fixture, dropped afterwards — never a mock. The
 * admin DSN comes from `SIMPLEORM_SQLSERVER` (a `sqlsrv:` or `odbc:` PDO DSN
 * reaching `master`, credentials embedded or integrated) or defaults to the
 * LocalDB default instance through `pdo_sqlsrv`; where neither a driver nor a
 * server answers, {@see self::available()} is false and the leg skips.
 */
final class SqlServerDatabase
{
    public const ENVIRONMENT_VARIABLE = 'SIMPLEORM_SQLSERVER';

    public const DEFAULT_ADMIN_DSN = 'sqlsrv:Server=(localdb)\MSSQLLocalDB;Database=master;Encrypt=no';

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
        LiveServer::skipUnless(self::adminDsn(), 'SQL Server is not available (set SIMPLEORM_SQLSERVER to a sqlsrv:/odbc: DSN, or run LocalDB)');
    }

    /** Creates the fixture database; call {@see self::available()} first. */
    public static function create(): self
    {
        $adminDsn = self::adminDsn() ?? throw new \LogicException('SQL Server is not available; gate the test with available()');
        $name = LiveServer::databaseName();
        LiveServer::connect($adminDsn)->exec("create database [{$name}]");

        return new self($adminDsn, $name);
    }

    /** The DSN `Db::open` takes for the fixture database. */
    public function connectionString(): string
    {
        return LiveServer::withDsnValue($this->adminDsn, 'Database', $this->name);
    }

    public function drop(): void
    {
        LiveServer::connect($this->adminDsn)->exec(
            "if db_id(N'{$this->name}') is not null begin "
                . "alter database [{$this->name}] set single_user with rollback immediate; "
                . "drop database [{$this->name}]; end",
        );
    }

    /** The admin DSN (with the right to create databases), or null where no server answers. */
    public static function adminDsn(): ?string
    {
        return LiveServer::adminDsn(self::ENVIRONMENT_VARIABLE, self::DEFAULT_ADMIN_DSN);
    }
}
