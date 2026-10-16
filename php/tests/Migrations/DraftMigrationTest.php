<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Migrations;

use PDO;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use SimpleOrm\Dialect\SqliteDialect;
use SimpleOrm\Dialect\SqliteShadow;
use SimpleOrm\Errors\MappingException;
use SimpleOrm\Errors\SimpleOrmException;
use SimpleOrm\Metadata\Attributes\Column;
use SimpleOrm\Metadata\Attributes\Generated;
use SimpleOrm\Metadata\Attributes\Ignore;
use SimpleOrm\Metadata\Attributes\Key;
use SimpleOrm\Metadata\Attributes\RenamedFrom;
use SimpleOrm\Metadata\Attributes\Table;
use SimpleOrm\Metadata\EntityMapBuilder;
use SimpleOrm\Metadata\EntityMapJson;
use SimpleOrm\Metadata\EntityMapLoader;
use SimpleOrm\Migrations\Draft;
use SimpleOrm\Migrations\MigrationGenerator;
use SimpleOrm\Migrations\MigrationRunner;
use SimpleOrm\Migrations\MigrationSet;
use SimpleOrm\Migrations\MigrationState;
use SimpleOrm\Migrations\MigrationVersion;
use SimpleOrm\Migrations\SqlVersion;
use SimpleOrm\Migrations\SqlVersionStep;
use SimpleOrm\Migrations\TableSchema;
use SimpleOrm\Migrations\TableSchemaColumn;
use SimpleOrm\Migrations\VersionBuilder;
use SimpleOrm\Tests\Support\TempDatabase;

/**
 * Level 3 M5 (spec/migrations.md "Draft migrations"): `#[RenamedFrom]` as a declared rename the diff generator
 * reads, and the draft marker `migrate` and `baseline` refuse (`MIG-013`). The conformance folders pin the
 * data-driven forms; this file pins the PHP surface — the attribute, the export, `MAP-027`, the generator's two
 * rename maps, the emitted `#[Draft]`, the runner's refusal, and `status`.
 */
final class DraftMigrationTest extends TestCase
{
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

    // --- the declaration -----------------------------------------------------------------

    #[Test]
    public function the_export_carries_renamed_from_only_when_declared(): void
    {
        $json = EntityMapJson::export($this->maps->load(DraftRenamedWidget::class), $this->maps);

        self::assertSame(1, substr_count($json, '"renamedFrom"'));
        self::assertMatchesRegularExpression('/"column": "title",\s+"type": "string",\s+"nullable": false,\s+"renamedFrom": "name"/', $json);
    }

    #[Test]
    public function the_property_map_holds_the_declared_previous_column(): void
    {
        $map = $this->maps->load(DraftRenamedWidget::class);

        self::assertSame('name', $map->properties[1]->renamedFrom);
        self::assertNull($map->properties[2]->renamedFrom);
        self::assertSame(['name' => 'title'], MigrationGenerator::declaredRenames($map));
    }

    #[Test]
    public function renamed_from_without_a_column_is_map027(): void
    {
        $this->assertLoadFails(DraftRenamedFromWithoutColumn::class);
    }

    #[Test]
    public function renamed_from_on_an_ignored_property_is_map027(): void
    {
        $this->assertLoadFails(DraftRenamedFromOnIgnored::class);
    }

    #[Test]
    public function renamed_from_with_a_blank_name_is_map027(): void
    {
        $this->assertLoadFails(DraftRenamedFromBlank::class);
    }

    #[Test]
    public function the_manual_builder_declares_a_rename_and_refuses_a_blank_one(): void
    {
        $map = EntityMapBuilder::for(DraftBuilt::class)->table('draft_built')
            ->column('id', key: true)
            ->column('title', name: 'headline', renamedFrom: 'title')
            ->build();
        self::assertSame('title', $map->properties[1]->renamedFrom);

        try {
            EntityMapBuilder::for(DraftBuilt::class)->table('draft_built')
                ->column('id', key: true)
                ->column('title', renamedFrom: '')
                ->build();
            self::fail('a blank rename was expected to fail the build');
        } catch (MappingException $e) {
            self::assertSame('MAP-027', $e->errorCode);
        }
    }

    // --- the generator ---------------------------------------------------------------------

    #[Test]
    public function a_declared_rename_applies_once(): void
    {
        $map = $this->maps->load(DraftRenamedWidget::class);

        // Against the shape before the rename: one data-preserving rename, nothing added or removed.
        $before = new TableSchema('draft_renamed_widgets', [
            new TableSchemaColumn('id', 'INTEGER', false, key: true, generated: true),
            new TableSchemaColumn('name', 'TEXT', false),
            new TableSchemaColumn('note', 'TEXT', true),
        ], []);
        $diff = MigrationGenerator::diffMap($map, $this->dialect, $before, []);
        self::assertCount(1, $diff->renamed);
        self::assertSame(['name', 'title'], [$diff->renamed[0]->from, $diff->renamed[0]->to]);
        self::assertSame([], $diff->added);
        self::assertSame([], $diff->removed);

        // Against the current shape: the declaration is permanent and harmless — nothing to do.
        $after = new TableSchema('draft_renamed_widgets', [
            new TableSchemaColumn('id', 'INTEGER', false, key: true, generated: true),
            new TableSchemaColumn('title', 'TEXT', false),
            new TableSchemaColumn('note', 'TEXT', true),
        ], []);
        $diff = MigrationGenerator::diffMap($map, $this->dialect, $after, []);
        self::assertFalse($diff->hasChanges());
        self::assertSame([], $diff->unsupported);

        // An explicit --rename of the already-applied pair stays strict: its old side is not in the snapshot.
        $diff = MigrationGenerator::diffMap($map, $this->dialect, $after, ['name' => 'title']);
        self::assertCount(1, $diff->unsupported);
    }

    #[Test]
    public function an_explicit_rename_wins_and_is_not_doubled_by_the_declaration(): void
    {
        $map = $this->maps->load(DraftRenamedWidget::class);
        $before = new TableSchema('draft_renamed_widgets', [
            new TableSchemaColumn('id', 'INTEGER', false, key: true, generated: true),
            new TableSchemaColumn('name', 'TEXT', false),
            new TableSchemaColumn('note', 'TEXT', true),
        ], []);

        $diff = MigrationGenerator::diffMap($map, $this->dialect, $before, ['name' => 'title']);

        self::assertCount(1, $diff->renamed);
        self::assertSame([], $diff->unsupported);
    }

    #[Test]
    public function the_emitted_root_carries_the_draft_marker_only_when_asked(): void
    {
        $plain = MigrationGenerator::emitRoot('My\\Migrations', 4, ['My\\Migrations\\Table\\Widget\\V0004_Reshape'], 'sqlite');
        $draft = MigrationGenerator::emitRoot('My\\Migrations', 4, ['My\\Migrations\\Table\\Widget\\V0004_Reshape'], 'sqlite', draft: true);

        self::assertStringNotContainsString('#[\\SimpleOrm\\Migrations\\Draft]', $plain);
        self::assertStringContainsString("#[\\SimpleOrm\\Migrations\\Draft]\nfinal class V0004 extends", $draft);
        self::assertTrue(MigrationGenerator::isGenerated($draft));
        self::assertSame('sqlite', MigrationGenerator::generatedDialectLabel($draft));
    }

    #[Test]
    public function an_emitted_draft_root_reads_back_as_a_draft(): void
    {
        $namespace = 'SimpleOrm\\Tests\\Migrations\\EmittedDraft' . bin2hex(random_bytes(4));
        foreach ([[1, false], [2, true]] as [$version, $draft]) {
            $code = MigrationGenerator::emitRoot($namespace, $version, ['X\\Step'], 'sqlite', $draft);
            $path = tempnam(sys_get_temp_dir(), 'simpleorm_draft_') . '.php';
            file_put_contents($path, $code);
            try {
                require $path;
            } finally {
                unlink($path);
            }

            $class = $namespace . '\\' . sprintf('V%04d', $version);
            /** @var MigrationVersion $instance */
            $instance = new $class();
            self::assertSame($draft, $instance->isDraft());
        }
    }

    #[Test]
    public function a_root_class_carrying_the_attribute_is_a_draft(): void
    {
        self::assertTrue((new V0042())->isDraft());
        self::assertFalse((new V0043())->isDraft());
        self::assertSame(1, count((new \ReflectionClass(V0042::class))->getAttributes(Draft::class)));
    }

    // --- the runner ------------------------------------------------------------------------

    private function plain(int $version, string $sql, string $description = 'step'): SqlVersion
    {
        return new SqlVersion($version, new SqlVersionStep('widgets', $description, [$sql]));
    }

    private function draft(int $version, string $sql): SqlVersion
    {
        return SqlVersion::draft($version, new SqlVersionStep('widgets', 'add_note', [$sql]));
    }

    private function runner(MigrationVersion ...$versions): MigrationRunner
    {
        return new MigrationRunner($this->connection, $this->dialect, $this->maps, MigrationSet::of(...$versions));
    }

    private function create(): SqlVersion
    {
        return $this->plain(1, 'create table widgets (id INTEGER PRIMARY KEY) STRICT', 'create');
    }

    private function addNoteDraft(): SqlVersion
    {
        return $this->draft(2, 'alter table widgets add column note TEXT');
    }

    /** @return list<array{0: int, 1: string}> */
    private function recorded(): array
    {
        try {
            $rows = $this->connection->query('select version, object from schema_version order by version')->fetchAll(PDO::FETCH_NUM);
        } catch (\PDOException) {
            return [];
        }

        return array_map(static fn (array $row): array => [(int) $row[0], (string) $row[1]], $rows);
    }

    #[Test]
    public function migrate_refuses_a_pending_draft_before_anything_executes(): void
    {
        $runner = $this->runner($this->create(), $this->addNoteDraft());

        try {
            $runner->migrate();
            self::fail('the draft was expected to refuse the run');
        } catch (SimpleOrmException $e) {
            self::assertSame('MIG-013', $e->errorCode);
        }

        // The earlier, non-draft version stayed unapplied too: the whole plan refused.
        self::assertSame([], $this->recorded());
        self::assertSame(
            0,
            (int) $this->connection->query("select count(*) from sqlite_master where name = 'widgets'")->fetchColumn(),
        );
    }

    #[Test]
    public function baseline_refuses_a_draft_it_would_record_but_not_one_above_it(): void
    {
        $runner = $this->runner($this->create(), $this->addNoteDraft());

        try {
            $runner->baseline(2);
            self::fail('baselining over the draft was expected to refuse');
        } catch (SimpleOrmException $e) {
            self::assertSame('MIG-013', $e->errorCode);
        }

        self::assertSame([], $this->recorded());

        $runner->baseline(1);
        self::assertSame([[1, 'widgets']], $this->recorded());
    }

    #[Test]
    public function status_reports_a_pending_draft_and_still_counts_it_as_pending(): void
    {
        $runner = $this->runner($this->create(), $this->addNoteDraft());
        $runner->baseline(1);

        $states = array_map(static fn ($entry): MigrationState => $entry->state, $runner->status());

        self::assertSame([MigrationState::Applied, MigrationState::Draft], $states);
        self::assertTrue($runner->hasPending());
        self::assertSame('draft', $runner->status()[1]->state->value);
    }

    #[Test]
    public function the_same_plan_without_the_marker_applies(): void
    {
        $runner = $this->runner($this->create(), $this->plain(2, 'alter table widgets add column note TEXT', 'add_note'));

        self::assertSame(2, $runner->migrate());
        self::assertSame([[1, 'widgets'], [2, 'widgets']], $this->recorded());
    }

    #[Test]
    public function a_draft_already_recorded_is_history_not_pending_work(): void
    {
        $this->runner($this->create(), $this->plain(2, 'alter table widgets add column note TEXT', 'add_note'))->migrate();

        $marked = $this->runner($this->create(), $this->draft(2, 'alter table widgets add column note TEXT'));

        self::assertSame(0, $marked->migrate());
        self::assertSame(MigrationState::Applied, $marked->status()[1]->state);
    }

    #[Test]
    public function shadow_replays_a_draft_like_any_version(): void
    {
        $dir = sys_get_temp_dir() . DIRECTORY_SEPARATOR . 'simpleorm_draft_shadow_' . bin2hex(random_bytes(6));
        mkdir($dir, 0777, true);
        try {
            $result = SqliteShadow::rebuildSnapshots(
                MigrationSet::of($this->create(), $this->addNoteDraft()),
                $this->maps,
                $dir,
            );

            self::assertSame([], $result->writtenFiles);   // raw SQL steps snapshot nothing; the point is that it did not refuse
        } finally {
            rmdir($dir);
        }
    }

    // --- helpers ---------------------------------------------------------------------------

    /** @param class-string $entityType */
    private function assertLoadFails(string $entityType): void
    {
        try {
            $this->maps->load($entityType);
            self::fail("{$entityType} was expected to fail loading");
        } catch (MappingException $e) {
            self::assertContains('MAP-027', array_map(static fn ($error): string => $error->code, $e->errors));
        }
    }
}

/** A table with a declared rename: `title` was `name`. */
#[Table('draft_renamed_widgets')]
final class DraftRenamedWidget
{
    #[Key]
    #[Generated]
    #[Column]
    public int $id;

    #[Column('title')]
    #[RenamedFrom('name')]
    public string $title;

    #[Column]
    public ?string $note = null;
}

/** A declaration on a property that maps no column. */
#[Table('draft_renamed_without_column')]
final class DraftRenamedFromWithoutColumn
{
    #[Key]
    #[Generated]
    #[Column]
    public int $id;

    #[RenamedFrom('old')]
    public string $legacy = '';
}

/** A declaration on an `#[Ignore]`d property. */
#[Table('draft_renamed_on_ignored')]
final class DraftRenamedFromOnIgnored
{
    #[Key]
    #[Generated]
    #[Column]
    public int $id;

    #[Ignore]
    #[RenamedFrom('old')]
    public string $legacy = '';
}

/** A declaration naming no column. */
#[Table('draft_renamed_blank')]
final class DraftRenamedFromBlank
{
    #[Key]
    #[Generated]
    #[Column]
    public int $id;

    #[Column]
    #[RenamedFrom('  ')]
    public string $title = '';
}

/** Target of the manual-builder cases. */
final class DraftBuilt
{
    public int $id;

    public string $title = '';
}

/** A root carrying the draft marker. */
#[Draft]
final class V0042 extends MigrationVersion
{
    public function compose(VersionBuilder $version): void
    {
    }
}

/** A root without it. */
final class V0043 extends MigrationVersion
{
    public function compose(VersionBuilder $version): void
    {
    }
}
