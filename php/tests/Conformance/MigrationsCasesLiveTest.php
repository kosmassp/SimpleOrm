<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Conformance;

use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use SimpleOrm\Dialect\Dialects;
use SimpleOrm\Tests\Support\ConformancePaths;
use SimpleOrm\Tests\Support\LiveServer;
use SimpleOrm\Tests\Support\MigrationCaseRunner;
use SimpleOrm\Tests\Support\PostgresDatabase;
use SimpleOrm\Tests\Support\SqlServerDatabase;

/**
 * The live migrations-case folders (ADR-0043, spec/migrations.md "Conformance cases"): `migrations-cases/postgres/` and
 * `migrations-cases/sqlserver/` hold the same format as the root folder in that dialect's SQL, with snapshots stamped for
 * the dialect, and run through the same {@see MigrationCaseRunner} against a **real server** — each case in a throwaway
 * database the dialect's shadow host provisions on the admin connection (`SIMPLEORM_POSTGRES` / `SIMPLEORM_SQLSERVER`,
 * defaults per spec/dialects.md "Testing contract") and drops afterwards, failure included. Skipped as a group where no
 * server answers, so `composer test` needs only PHP and `pdo_sqlite`; `composer test-live` loads the PDO drivers.
 */
final class MigrationsCasesLiveTest extends TestCase
{
    /** @return iterable<string, array{0: string, 1: string}> dialect name, case file relative to migrations-cases/ */
    public static function caseFiles(): iterable
    {
        foreach ([Dialects::POSTGRES, Dialects::SQLSERVER] as $dialect) {
            foreach (ConformancePaths::cases('migrations-cases/' . $dialect) as $file) {
                yield "{$dialect}/{$file}" => [$dialect, $dialect . DIRECTORY_SEPARATOR . $file];
            }
        }
    }

    #[Test]
    #[DataProvider('caseFiles')]
    public function case_behaves_as_specified_on_a_live_server(string $dialectName, string $relativeFile): void
    {
        $admin = match ($dialectName) {
            Dialects::POSTGRES => PostgresDatabase::adminDsn(),
            default => SqlServerDatabase::adminDsn(),
        };
        LiveServer::skipUnless($admin, "{$dialectName} is not available (see spec/dialects.md \"Testing contract\")");

        $dialect = Dialects::create($dialectName);
        $host = Dialects::shadowHost($dialect);
        $connectionString = $host->provision((string) $admin);
        try {
            MigrationCaseRunner::run(
                ConformancePaths::dir('migrations-cases') . DIRECTORY_SEPARATOR . $relativeFile,
                $dialect,
                $host,
                $connectionString,
            );
        } finally {
            $host->drop((string) $admin, $connectionString);
        }
    }
}
