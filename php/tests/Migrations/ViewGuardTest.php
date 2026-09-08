<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Migrations;

use PDO;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use SimpleOrm\Dialect\SqliteDialect;
use SimpleOrm\Errors\SimpleOrmException;
use SimpleOrm\Metadata\EntityMapLoader;
use SimpleOrm\Migrations\MigrationRunner;
use SimpleOrm\Migrations\MigrationSet;
use SimpleOrm\Migrations\MigrationStep;
use SimpleOrm\Migrations\MigrationVersion;
use SimpleOrm\Migrations\SchemaSync;
use SimpleOrm\Migrations\SqlVersion;
use SimpleOrm\Migrations\SqlVersionStep;
use SimpleOrm\Migrations\VersionBuilder;
use SimpleOrm\Migrations\ViewActions;
use SimpleOrm\Migrations\ViewMigration;
use SimpleOrm\Tests\Migrations\Fixtures\Guard\GuardedTotals;
use SimpleOrm\Tests\Support\TempDatabase;

/**
 * ADR-0017 add.1 (owner): views get adjusted outside the code in urgencies, so
 * a view step's `expectDefinition` guard compares the live definition against
 * the expected previous one before applying — match applies, drift refuses
 * with `MIG-012`, and only `--force` (`$allowViewDrift`) recreates over the
 * drift (with notice). Mirrors `dotnet/tests/SimpleOrm.Tests/ViewGuardTests.cs`.
 */
final class ViewGuardTest extends TestCase
{
    // Public: read from the anonymous ViewMigration step below, a distinct class scope.
    public const string V1_DDL = 'create view guarded_totals as select 1 as answer';

    public const string V2_DDL = 'create view guarded_totals as select 2 as answer';

    private TempDatabase $database;

    private SqliteDialect $dialect;

    private PDO $connection;

    private EntityMapLoader $maps;

    protected function setUp(): void
    {
        $this->database = TempDatabase::create();
        $this->dialect = new SqliteDialect();
        $this->connection = $this->dialect->createConnection($this->database->connectionString());
        $this->maps = new EntityMapLoader();
    }

    protected function tearDown(): void
    {
        unset($this->connection);
        $this->database->delete();
    }

    private function v1(): SqlVersion
    {
        return new SqlVersion(1, new SqlVersionStep('guarded_totals', 'create', [self::V1_DDL], ['drop view guarded_totals']));
    }

    /** The guarded change step (ADR-0017 add.1): hand-written `down()` carries its own guard on `V2_DDL`, the same way `action()` guards `V1_DDL`. */
    private function changeStep(): MigrationStep
    {
        return new class extends ViewMigration {
            public function version(): int
            {
                return 2;
            }

            public function description(): string
            {
                return 'ChangeGuarded';
            }

            public function entityClass(): string
            {
                return GuardedTotals::class;
            }

            public function action(ViewActions $actions): void
            {
                $actions->expectDefinition(ViewGuardTest::V1_DDL);
                $actions->sql('drop view if exists guarded_totals');
                $actions->sql(ViewGuardTest::V2_DDL);
            }

            public function down(ViewActions $actions): void
            {
                $actions->expectDefinition(ViewGuardTest::V2_DDL);
                $actions->sql('drop view if exists guarded_totals');
                $actions->sql(ViewGuardTest::V1_DDL);
            }
        };
    }

    private function v2(): MigrationVersion
    {
        $step = $this->changeStep();

        return new class($step) extends MigrationVersion {
            public function __construct(private readonly MigrationStep $step)
            {
            }

            public function version(): int
            {
                return 2;
            }

            public function compose(VersionBuilder $version): void
            {
                $version->apply($this->step);
            }
        };
    }

    private function runner(): MigrationRunner
    {
        return new MigrationRunner($this->connection, $this->dialect, $this->maps, MigrationSet::of($this->v1(), $this->v2()));
    }

    private function answer(): int
    {
        return (int) $this->connection->query('select answer from guarded_totals')->fetchColumn();
    }

    #[Test]
    public function matching_previous_definition_applies(): void
    {
        $runner = $this->runner();
        self::assertSame(2, $runner->migrate());
        self::assertSame(2, $this->answer());
    }

    #[Test]
    public function outside_drift_refuses_with_mig012_and_applies_nothing(): void
    {
        $only1 = new MigrationRunner($this->connection, $this->dialect, $this->maps, MigrationSet::of($this->v1()));
        $only1->migrate();

        // The urgency hotfix: the view is patched directly in the database.
        SchemaSync::apply($this->connection, ['drop view guarded_totals', 'create view guarded_totals as select 99 as answer']);

        $runner = $this->runner();
        try {
            $runner->migrate();
            self::fail('expected MIG-012');
        } catch (SimpleOrmException $exception) {
            self::assertSame('MIG-012', $exception->errorCode);
        }

        // The whole run rolled back: the hotfixed definition is untouched.
        self::assertSame(99, $this->answer());
    }

    #[Test]
    public function force_recreates_over_the_drift_and_notifies(): void
    {
        $only1 = new MigrationRunner($this->connection, $this->dialect, $this->maps, MigrationSet::of($this->v1()));
        $only1->migrate();
        SchemaSync::apply($this->connection, ['drop view guarded_totals', 'create view guarded_totals as select 99 as answer']);

        $notices = [];
        $runner = $this->runner();
        $applied = $runner->migrate(true, function (string $notice) use (&$notices): void {
            $notices[] = $notice;
        });

        self::assertSame(1, $applied);
        self::assertCount(1, $notices);
        self::assertStringContainsString('guarded_totals', $notices[0]);
        self::assertSame(2, $this->answer());
    }

    #[Test]
    public function down_is_guarded_the_same_way(): void
    {
        $runner = $this->runner();
        $runner->migrate();

        SchemaSync::apply($this->connection, ['drop view guarded_totals', 'create view guarded_totals as select 99 as answer']);
        try {
            $runner->migrateDown(1);
            self::fail('expected MIG-012');
        } catch (SimpleOrmException $exception) {
            self::assertSame('MIG-012', $exception->errorCode);
        }

        self::assertSame(1, $runner->migrateDown(1, true));
        self::assertSame(1, $this->answer());
    }
}
