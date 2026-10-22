<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Migrations;

use PDO;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use SimpleOrm\Dialect\PostgresDialect;
use SimpleOrm\Errors\SimpleOrmException;
use SimpleOrm\Metadata\EntityMapLoader;
use SimpleOrm\Migrations\MigrationRunner;
use SimpleOrm\Migrations\MigrationSet;
use SimpleOrm\Migrations\SqlVersion;
use SimpleOrm\Migrations\SqlVersionStep;
use SimpleOrm\Tests\Support\LiveServer;
use SimpleOrm\Tests\Support\PostgresDatabase;

/**
 * The `MIG-012` view guard where the engine reformats stored views (ADR-0043, spec/migrations.md "The view and trigger
 * apply guard"): PostgreSQL's `pg_get_viewdef` rewrites the parse tree, so the runner creates the expected body as a probe
 * view inside the run, reads it back the same way, drops it, and compares the engine's two renderings — equal exactly
 * when the parse trees are. The probe runs under a savepoint, so a body the current schema cannot parse counts as drift
 * instead of poisoning the transaction. A real PostgreSQL, skipped as a group where none answers.
 */
final class ViewGuardProbeLiveTest extends TestCase
{
    private static ?PostgresDatabase $database = null;

    private ?PDO $connection = null;

    public static function setUpBeforeClass(): void
    {
        if (PostgresDatabase::available()) {
            self::$database = PostgresDatabase::create();
        }
    }

    public static function tearDownAfterClass(): void
    {
        self::$database?->drop();
        self::$database = null;
    }

    protected function setUp(): void
    {
        PostgresDatabase::skipUnlessAvailable();

        LiveServer::connect(self::database()->connectionString())->exec('drop schema public cascade; create schema public');
        $this->connection = (new PostgresDialect())->createConnection(self::database()->connectionString());
    }

    protected function tearDown(): void
    {
        $this->connection = null;
    }

    #[Test]
    public function a_differently_formatted_but_equal_view_matches_the_guard(): void
    {
        $this->migrate([self::create('create view totals as select   1 as a,   2 as b')]);

        // Text comparison would differ (spacing); the engine's two renderings of the same parse tree do not.
        $this->migrate([
            self::create('create view totals as select   1 as a,   2 as b'),
            self::change('create view totals as select 1 as a, 2 as b', 'select 3 as c'),
        ]);

        self::assertSame([[1, 'totals'], [2, 'totals']], $this->applied());
    }

    #[Test]
    public function a_view_changed_outside_the_code_refuses_and_force_recreates(): void
    {
        $this->migrate([self::create('create view totals as select 1 as a')]);
        $this->connection?->exec('drop view totals; create view totals as select 99 as a');
        $versions = [
            self::create('create view totals as select 1 as a'),
            self::change('create view totals as select 1 as a', 'select 2 as a'),
        ];

        try {
            $this->migrate($versions);
            self::fail('the hotfixed view refuses');
        } catch (SimpleOrmException $e) {
            self::assertSame('MIG-012', $e->errorCode);
        }

        self::assertSame([[1, 'totals']], $this->applied(), 'the refused run changed nothing');

        $this->migrate($versions, force: true);
        self::assertSame([[1, 'totals'], [2, 'totals']], $this->applied());
    }

    #[Test]
    public function an_expected_body_the_schema_cannot_parse_is_drift_and_does_not_poison_the_run(): void
    {
        $this->migrate([self::create('create view totals as select 1 as a')]);
        $versions = [
            self::create('create view totals as select 1 as a'),
            self::change('create view totals as select * from no_such_table_anywhere', 'select 2 as a'),
        ];

        try {
            $this->migrate($versions);
            self::fail('the unparsable expectation counts as drift');
        } catch (SimpleOrmException $e) {
            self::assertSame('MIG-012', $e->errorCode);
        }

        // Forced: the failed probe was rolled back to its savepoint, so the same transaction goes on to apply the step.
        $this->migrate($versions, force: true);
        self::assertSame([[1, 'totals'], [2, 'totals']], $this->applied());
        self::assertSame(
            0,
            (int) $this->connection?->query("select count(*) from pg_class where relname like 'simpleorm_expect_%'")->fetchColumn(),
            'no probe view is left behind',
        );
    }

    // --- helpers -----------------------------------------------------------------------------

    private static function create(string $ddl): SqlVersion
    {
        return new SqlVersion(1, new SqlVersionStep('totals', 'create_view', [$ddl]));
    }

    private static function change(string $expectedDdl, string $newBody): SqlVersion
    {
        return new SqlVersion(2, new SqlVersionStep(
            'totals',
            'change_view',
            ['drop view if exists "totals"', "create or replace view \"totals\" as {$newBody}"],
            expectDefinition: $expectedDdl,
        ));
    }

    /** @param list<SqlVersion> $versions */
    private function migrate(array $versions, bool $force = false): void
    {
        (new MigrationRunner($this->connection ?? throw new \LogicException('no connection'), new PostgresDialect(), new EntityMapLoader(), MigrationSet::of(...$versions)))
            ->migrate($force);
    }

    /** @return list<array{0: int, 1: string}> */
    private function applied(): array
    {
        $rows = [];
        foreach ($this->connection?->query('select version, object from schema_version order by version, object') ?? [] as $row) {
            $rows[] = [(int) $row['version'], (string) $row['object']];
        }

        return $rows;
    }

    private static function database(): PostgresDatabase
    {
        return self::$database ?? throw new \LogicException('the fixture database was not created');
    }
}
