<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Conformance;

use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use SimpleOrm\Dialect\Dialects;
use SimpleOrm\Dialect\SqliteDialect;
use SimpleOrm\Tests\Support\ConformancePaths;
use SimpleOrm\Tests\Support\MigrationCaseRunner;
use SimpleOrm\Tests\Support\TempDatabase;

/**
 * The migrations-case runner (§9) for the root folder, which runs on SQLite: mirrors the reference's. Each
 * conformance/migrations-cases/*.json builds its migration set as data and replays the commands against a fresh SQLite
 * database, comparing the recorded (version, object) rows or the error code (the shared
 * {@see MigrationCaseRunner}; the live `postgres/` and `sqlserver/` folders run in `MigrationsCasesLiveTest`). A case may
 * carry `snapshots` (the derived-rollback history, ADR-0018; stamped for a dialect since ADR-0043), step
 * `renames`/`expectDefinition` (typed renames; the MIG-012 view guard), a raw `sql` command (the outside hotfix),
 * `force` on migrate/down, and deep expects (`columns` per table, `ddl` per view, `triggers`).
 */
final class MigrationsCasesTest extends TestCase
{
    /** @return list<array{0: string}> */
    public static function caseFiles(): array
    {
        return array_map(static fn (string $file): array => [$file], ConformancePaths::cases('migrations-cases'));
    }

    #[Test]
    #[DataProvider('caseFiles')]
    public function case_behaves_as_specified(string $fileName): void
    {
        $database = TempDatabase::create();
        $dialect = new SqliteDialect();

        try {
            MigrationCaseRunner::run(
                ConformancePaths::dir('migrations-cases') . DIRECTORY_SEPARATOR . $fileName,
                $dialect,
                Dialects::shadowHost($dialect),
                $database->connectionString(),
            );
        } finally {
            $database->delete();
        }
    }
}
