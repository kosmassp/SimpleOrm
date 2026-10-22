<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Migrations;

use DateTimeImmutable;
use FilesystemIterator;
use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use RecursiveDirectoryIterator;
use RecursiveIteratorIterator;
use SimpleOrm\Dialect\Dialects;
use SimpleOrm\Dialect\SqliteDialect;
use SimpleOrm\Errors\SimpleOrmException;
use SimpleOrm\Metadata\EntityMapLoader;
use SimpleOrm\Metadata\TriggerDefinition;
use SimpleOrm\Metadata\TriggerEvent;
use SimpleOrm\Metadata\TriggerTiming;
use SimpleOrm\Migrations\Diff\DiffCommand;
use SimpleOrm\Migrations\Diff\DiffOptions;
use SimpleOrm\Migrations\DownDeriver;
use SimpleOrm\Migrations\SchemaSnapshot;
use SimpleOrm\Migrations\SnapshotSet;
use SimpleOrm\Migrations\TableSchema;
use SimpleOrm\Migrations\TableSchemaColumn;
use SimpleOrm\Migrations\TriggerSchema;
use SimpleOrm\Migrations\ViewActions;
use SimpleOrm\Tests\Conformance\AmendFixture\AmendWidget;
use SimpleOrm\Tests\Sample\Models\UserTransactionTotal;

/**
 * Snapshot format v3 (ADR-0043, spec/migrations.md "Format v3: the dialect stamp") and the neutral view normalizer: the
 * `"dialect"` stamp every non-SQLite snapshot carries right after `"object"` whatever its kind, the unstamped-is-SQLite
 * rule, and `MIG-014` from every reader — the derived rollback and the diff baseline (`--amend` included). The shadow's
 * trusted baseline is covered in `ShadowRoundTripTest`.
 */
final class SnapshotStampTest extends TestCase
{
    private const string FIXTURE_DIR = __DIR__ . '/../Conformance/AmendFixture';

    private const string FIXTURE_NAMESPACE = 'SimpleOrm\\Tests\\Conformance\\AmendFixture';

    #[Test]
    public function a_table_snapshot_is_stamped_right_after_object_for_every_dialect_but_sqlite(): void
    {
        $schema = new TableSchema('widgets', [new TableSchemaColumn('id', 'bigint', false, key: true, generated: true)], []);
        $at = new DateTimeImmutable('2026-10-22T12:00:00Z');

        self::assertSame(['object', 'asOfVersion', 'generatedAt', 'columns', 'indexes'], self::keys(SchemaSnapshot::export($schema, 3, $at)));
        self::assertSame(['object', 'asOfVersion', 'generatedAt', 'columns', 'indexes'], self::keys(SchemaSnapshot::export($schema, 3, $at, 'sqlite')));
        self::assertSame(
            ['object', 'dialect', 'asOfVersion', 'generatedAt', 'columns', 'indexes'],
            self::keys(SchemaSnapshot::export($schema, 3, $at, 'postgres')),
        );
        self::assertSame('sqlserver', self::decode(SchemaSnapshot::export($schema, 3, $at, 'sqlserver'))['dialect']);
    }

    #[Test]
    public function a_view_and_a_trigger_snapshot_are_stamped_the_same_way(): void
    {
        $at = new DateTimeImmutable('2026-10-22T12:00:00Z');
        $trigger = new TriggerSchema('widgets', false, new TriggerDefinition('trg_w', TriggerTiming::After, [TriggerEvent::Update], 'select 1;'));

        self::assertSame(
            ['object', 'dialect', 'kind', 'asOfVersion', 'generatedAt', 'ddl'],
            self::keys(SchemaSnapshot::exportDdl('totals', 'view', 'create view totals as select 1', 2, $at, 'postgres')),
        );
        self::assertSame(
            ['object', 'kind', 'asOfVersion', 'generatedAt', 'ddl'],
            self::keys(SchemaSnapshot::exportDdl('totals', 'view', 'create view totals as select 1', 2, $at)),
        );
        self::assertSame(
            ['object', 'dialect', 'kind', 'relation', 'relationKind', 'asOfVersion', 'generatedAt', 'timing', 'events', 'sql'],
            self::keys(SchemaSnapshot::exportTrigger($trigger, 4, $at, 'sqlserver')),
        );
    }

    #[Test]
    public function every_reader_returns_the_stamp_and_an_unstamped_document_is_sqlites(): void
    {
        $at = new DateTimeImmutable('2026-10-22T12:00:00Z');
        $schema = new TableSchema('widgets', [new TableSchemaColumn('id', 'bigint', false, key: true, generated: true)], []);
        $trigger = new TriggerSchema('widgets', false, new TriggerDefinition('trg_w', TriggerTiming::After, [TriggerEvent::Update], 'select 1;'));

        self::assertSame('postgres', SchemaSnapshot::parse(SchemaSnapshot::export($schema, 1, $at, 'postgres'))->dialect);
        self::assertSame('sqlite', SchemaSnapshot::parse(SchemaSnapshot::export($schema, 1, $at))->dialect);
        self::assertSame('sqlserver', SchemaSnapshot::parseDdl(SchemaSnapshot::exportDdl('t', 'view', 'create view t as select 1', 1, $at, 'sqlserver'))->dialect);
        self::assertSame('sqlite', SchemaSnapshot::parseDdl(SchemaSnapshot::exportDdl('t', 'view', 'create view t as select 1', 1, $at))->dialect);
        self::assertSame('postgres', SchemaSnapshot::parseTrigger(SchemaSnapshot::exportTrigger($trigger, 1, $at, 'postgres'))->dialect);

        $set = new SnapshotSet([SchemaSnapshot::export($schema, 1, $at, 'postgres'), SchemaSnapshot::export($schema, 2, $at)]);
        self::assertSame('postgres', $set->at('widgets', 1)?->dialect);
        self::assertSame('sqlite', $set->at('widgets', 2)?->dialect);
    }

    #[Test]
    public function the_derived_rollback_refuses_a_snapshot_stamped_for_another_dialect(): void
    {
        $at = new DateTimeImmutable('2026-10-22T12:00:00Z');
        $v1 = new TableSchema('widgets', [new TableSchemaColumn('id', 'INTEGER', false, key: true, generated: true)], []);
        $v2 = new TableSchema('widgets', [
            new TableSchemaColumn('id', 'INTEGER', false, key: true, generated: true),
            new TableSchemaColumn('note', 'TEXT', true),
        ], []);

        // The version's own snapshot is foreign.
        $notices = [];
        try {
            DownDeriver::derive('widgets', 2, new SnapshotSet([
                SchemaSnapshot::export($v1, 1, $at),
                SchemaSnapshot::export($v2, 2, $at, 'postgres'),
            ]), [], $notices, new SqliteDialect());
            self::fail('a postgres snapshot is refused by a sqlite rollback');
        } catch (SimpleOrmException $e) {
            self::assertSame('MIG-014', $e->errorCode);
        }

        // The snapshot it would diff against is foreign.
        try {
            DownDeriver::derive('widgets', 2, new SnapshotSet([
                SchemaSnapshot::export($v1, 1, $at, 'sqlserver'),
                SchemaSnapshot::export($v2, 2, $at),
            ]), [], $notices, new SqliteDialect());
            self::fail('the previous snapshot is checked too');
        } catch (SimpleOrmException $e) {
            self::assertSame('MIG-014', $e->errorCode);
        }
    }

    /** @return iterable<string, array{bool}> */
    public static function amendModes(): iterable
    {
        yield 'a plain diff' => [false];
        yield 'an amend' => [true];
    }

    #[Test]
    #[DataProvider('amendModes')]
    public function the_diff_baseline_refuses_a_snapshot_stamped_for_another_dialect(bool $amend): void
    {
        $dir = sys_get_temp_dir() . DIRECTORY_SEPARATOR . 'simpleorm_stamp_' . bin2hex(random_bytes(8));
        mkdir($dir . '/Table/AmendWidget', 0777, true);
        // A plain diff targets V0003 and reads below it (V0002); an amend targets V0002 and reads below it (V0001).
        $at = $amend ? 1 : 2;
        file_put_contents(
            $dir . "/Table/AmendWidget/V000{$at}.schema.json",
            SchemaSnapshot::export(
                new TableSchema('amend_widgets', [new TableSchemaColumn('id', 'bigint', false, key: true, generated: true)], []),
                $at,
                new DateTimeImmutable('2026-10-22T12:00:00Z'),
                'postgres',
            ),
        );
        if ($amend) {
            // The amend target's generated files must exist for the run to reach the baseline.
            file_put_contents($dir . '/V0002.php', "<?php\n/** Generated by simpleorm diff (ADR-0017, dialect sqlite) */\n");
            file_put_contents($dir . '/Table/AmendWidget/V0002_AddNote.php', "<?php\n/** Generated by simpleorm diff (ADR-0017) */\n");
        }

        try {
            DiffCommand::execute(
                new DiffOptions(
                    migrationsDir: self::FIXTURE_DIR,
                    rootNamespace: self::FIXTURE_NAMESPACE,
                    entityTypes: [AmendWidget::class],
                    outDir: $dir,
                    dialect: new SqliteDialect(),
                    dialectLabel: 'sqlite',
                    amend: $amend,
                ),
                static function (string $line): void {
                },
                static function (string $line): void {
                },
            );
            self::fail('the foreign-stamped baseline is refused');
        } catch (SimpleOrmException $e) {
            self::assertSame('MIG-014', $e->errorCode);
        } finally {
            self::removeDirectory($dir);
        }
    }

    // --- the neutral view form ---------------------------------------------------------------

    /** @return iterable<string, array{string, string}> */
    public static function viewTexts(): iterable
    {
        $neutral = 'create view totals as select 1 as answer';
        yield 'sqlite as stored' => ["CREATE VIEW totals AS\nselect 1 as answer", $neutral];
        yield 'sqlite as created' => ["create view if not exists totals as\nselect 1 as answer", $neutral];
        yield 'sql server' => ["create or alter view [totals] as\nselect 1 as answer", $neutral];
        yield 'sql server stored as submitted' => ["CREATE OR ALTER VIEW [totals] AS select 1 as answer", $neutral];
        yield 'postgres' => ["create or replace view \"totals\" as\nselect 1 as answer", $neutral];
        yield 'a backtick name' => ["create view `totals` as select 1 as answer", $neutral];
        yield 'already neutral' => [$neutral, $neutral];
        yield 'a materialized view' => ["create materialized view if not exists \"totals\" as\nselect 1 as answer", 'create materialized view totals as select 1 as answer'];
        yield 'layout is not schema' => ["create   view totals as\n\n   select  1   as answer\n", $neutral];
    }

    #[Test]
    #[DataProvider('viewTexts')]
    public function every_dialects_view_prefix_normalizes_to_the_neutral_form(string $text, string $neutral): void
    {
        self::assertSame($neutral, SchemaSnapshot::normalizeDdl($text));
    }

    #[Test]
    public function a_body_with_brackets_or_quotes_is_left_as_written(): void
    {
        self::assertSame(
            'create view totals as select "a" as [b], \'it\'\'s\' as c',
            SchemaSnapshot::normalizeDdl("create or alter view [totals] as select \"a\" as [b], 'it''s' as c"),
        );
    }

    #[Test]
    public function the_neutral_form_splits_into_name_body_and_kind(): void
    {
        self::assertSame(
            ['name' => 'totals', 'body' => 'select 1 as answer', 'materialized' => false],
            SchemaSnapshot::splitViewDdl('create view totals as select 1 as answer'),
        );
        self::assertSame(
            ['name' => 'totals', 'body' => 'select 1', 'materialized' => true],
            SchemaSnapshot::splitViewDdl('create materialized view totals as select 1'),
        );
        self::assertNull(SchemaSnapshot::splitViewDdl('create procedure p as select 1'));
    }

    #[Test]
    public function the_metadata_side_view_snapshot_is_the_same_text_for_every_dialect(): void
    {
        $map = (new EntityMapLoader())->load(UserTransactionTotal::class);
        $texts = [];
        foreach (Dialects::names() as $name) {
            $texts[$name] = SchemaSnapshot::normalizeDdl(Dialects::create($name)->createViewSql($map));
        }

        self::assertCount(1, array_unique($texts), 'a view snapshot is dialect-neutral: ' . json_encode($texts));
    }

    #[Test]
    public function expect_definition_normalizes_the_way_the_snapshot_does(): void
    {
        $map = (new EntityMapLoader())->load(UserTransactionTotal::class);
        $actions = new ViewActions($map, new SqliteDialect());
        $actions->expectDefinition("create or alter view [user_transaction_totals] as\n  select 1 as one");

        $statements = $actions->build();

        self::assertCount(1, $statements);
        self::assertSame('create view user_transaction_totals as select 1 as one', $statements[0]->sql);
        self::assertSame('user_transaction_totals', $statements[0]->guardView);
    }

    // --- helpers -----------------------------------------------------------------------------

    /** @return list<string> */
    private static function keys(string $json): array
    {
        return array_keys(self::decode($json));
    }

    /** @return array<string, mixed> */
    private static function decode(string $json): array
    {
        /** @var array<string, mixed> $decoded */
        $decoded = json_decode($json, true, flags: JSON_THROW_ON_ERROR);

        return $decoded;
    }

    private static function removeDirectory(string $dir): void
    {
        $iterator = new RecursiveIteratorIterator(
            new RecursiveDirectoryIterator($dir, FilesystemIterator::SKIP_DOTS),
            RecursiveIteratorIterator::CHILD_FIRST,
        );
        foreach ($iterator as $fileInfo) {
            $fileInfo->isDir() ? rmdir($fileInfo->getPathname()) : unlink($fileInfo->getPathname());
        }

        rmdir($dir);
    }
}
