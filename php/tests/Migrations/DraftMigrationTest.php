<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Migrations;

use DateTimeImmutable;
use FilesystemIterator;
use PDO;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use RecursiveDirectoryIterator;
use RecursiveIteratorIterator;
use SimpleOrm\Dialect\SqliteDialect;
use SimpleOrm\Dialect\SqliteShadow;
use SimpleOrm\Errors\MappingException;
use SimpleOrm\Errors\SchemaValidationException;
use SimpleOrm\Errors\SimpleOrmException;
use SimpleOrm\Metadata\Attributes\Column;
use SimpleOrm\Metadata\Attributes\ForeignKey;
use SimpleOrm\Metadata\Attributes\Generated;
use SimpleOrm\Metadata\Attributes\Ignore;
use SimpleOrm\Metadata\Attributes\Key;
use SimpleOrm\Metadata\Attributes\ManyToOne;
use SimpleOrm\Metadata\Attributes\Owned;
use SimpleOrm\Metadata\Attributes\RenamedFrom;
use SimpleOrm\Metadata\Attributes\Table;
use SimpleOrm\Metadata\Attributes\View;
use SimpleOrm\Metadata\AuditRole;
use SimpleOrm\Metadata\EntityMapBuilder;
use SimpleOrm\Metadata\EntityMapJson;
use SimpleOrm\Metadata\EntityMapLoader;
use SimpleOrm\Migrations\Diff\DiffCommand;
use SimpleOrm\Migrations\Diff\DiffOptions;
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
use SimpleOrm\Session\Db;
use SimpleOrm\Session\DbOptions;
use SimpleOrm\Tests\Conformance\AmendFixture\AmendWidget;
use SimpleOrm\Tests\Sample\Models\User;
use SimpleOrm\Tests\Support\TempDatabase;
use SimpleOrm\Validation\SchemaGuard;

/**
 * Level 3 M5 (spec/migrations.md "Draft migrations"): `#[RenamedFrom]` as a declared rename the diff generator
 * reads, and the draft marker `migrate` and `baseline` refuse (`MIG-013`). The conformance folders pin the
 * data-driven forms; this file pins the PHP surface — the attribute, the export, `MAP-027`, the generator's two
 * rename maps, the emitted `#[Draft]`, the runner's refusal, and `status`.
 */
final class DraftMigrationTest extends TestCase
{
    private const string DRAFT_FIXTURE_NAMESPACE = 'SimpleOrm\\Tests\\Migrations\\AmendDraftFixture';

    private const string DRAFT_FIXTURE_DIR = __DIR__ . '/AmendDraftFixture';

    private const string REVIEWED_FIXTURE_NAMESPACE = 'SimpleOrm\\Tests\\Conformance\\AmendFixture';

    private const string REVIEWED_FIXTURE_DIR = __DIR__ . '/../Conformance/AmendFixture';

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

    #[Test]
    public function renamed_from_on_a_navigation_is_map027(): void
    {
        $this->assertLoadFailsOnlyWithMap027(DraftRenamedOnNavigation::class);
    }

    #[Test]
    public function renamed_from_on_an_owned_navigation_is_map027(): void
    {
        $this->assertLoadFailsOnlyWithMap027(DraftRenamedOnOwnedNavigation::class);
    }

    #[Test]
    public function renamed_from_on_an_owned_types_member_is_map027(): void
    {
        $this->assertLoadFailsOnlyWithMap027(DraftRenamedOnOwnedMember::class);
    }

    #[Test]
    public function renamed_from_on_a_view_is_map027(): void
    {
        $this->assertLoadFailsOnlyWithMap027(DraftRenamedOnView::class);
    }

    #[Test]
    public function two_properties_declaring_the_same_previous_column_are_map027_at_load(): void
    {
        $this->assertLoadFailsOnlyWithMap027(DraftSameOrigin::class);
    }

    #[Test]
    public function the_manual_builder_refuses_two_properties_with_one_previous_column(): void
    {
        $builder = EntityMapBuilder::for(DraftBuilt::class)->table('draft_built')
            ->column('id', key: true)
            ->column('title', name: 'headline', renamedFrom: 'legacy')
            ->column('other', renamedFrom: ' LEGACY ');

        try {
            $builder->build();
            self::fail('two declarations of one previous column were expected to fail the build');
        } catch (MappingException $e) {
            self::assertSame(['MAP-027'], array_map(static fn ($error): string => $error->code, $e->errors));
        }
    }

    #[Test]
    public function a_declared_name_is_trimmed_in_the_map_and_the_export(): void
    {
        $map = EntityMapBuilder::for(DraftBuilt::class)->table('draft_built')
            ->column('id', key: true)
            ->column('title', renamedFrom: '  name ')
            ->build();

        self::assertSame('name', $map->properties[1]->renamedFrom);
        self::assertSame(['name' => 'title'], MigrationGenerator::declaredRenames($map));
        self::assertStringContainsString('"renamedFrom": "name"', EntityMapJson::export($map, $this->maps));
    }

    #[Test]
    public function renamed_from_is_the_last_optional_key_of_an_exported_column(): void
    {
        $map = EntityMapBuilder::for(DraftAudited::class)->table('draft_audited')
            ->column('id', key: true, generated: true, renamedFrom: 'legacy_id')
            ->column('stamped', audit: AuditRole::CreatedAt, renamedFrom: 'old_stamp')
            ->build();

        $json = EntityMapJson::export($map, $this->maps);

        self::assertMatchesRegularExpression('/"key": true,\s+"generated": true,\s+"renamedFrom": "legacy_id"\s+\}/', $json);
        self::assertMatchesRegularExpression('/"audit": "created_at",\s+"renamedFrom": "old_stamp"\s+\}/', $json);
    }

    #[Test]
    public function a_declaration_declares_nothing_while_the_model_still_maps_the_old_column(): void
    {
        $map = $this->maps->load(DraftStillMapped::class);
        $snapshot = new TableSchema('draft_still_mapped', [
            new TableSchemaColumn('id', 'INTEGER', false, key: true, generated: true),
            new TableSchemaColumn('remark', 'TEXT', true),
        ], []);

        $diff = MigrationGenerator::diffMap($map, $this->dialect, $snapshot, []);

        self::assertSame([], $diff->renamed);
        self::assertSame(['note'], array_map(static fn ($column): string => $column->name, $diff->added));
        self::assertSame([], $diff->removed);
        self::assertSame([], $diff->unsupported);
    }

    #[Test]
    public function an_explicit_rename_matches_case_insensitively_and_is_applied_once(): void
    {
        $map = $this->maps->load(DraftRenamedWidget::class);
        $before = new TableSchema('draft_renamed_widgets', [
            new TableSchemaColumn('id', 'INTEGER', false, key: true, generated: true),
            new TableSchemaColumn('name', 'TEXT', false),
            new TableSchemaColumn('note', 'TEXT', true),
        ], []);

        $diff = MigrationGenerator::diffMap($map, $this->dialect, $before, ['NAME' => 'title']);

        self::assertCount(1, $diff->renamed);
        self::assertSame([], $diff->unsupported);
    }

    #[Test]
    public function the_amend_of_a_draft_keeps_the_marker_with_or_without_the_flag(): void
    {
        foreach ([false, true] as $draftFlag) {
            $root = $this->amendedRoot($draftFlag, self::DRAFT_FIXTURE_NAMESPACE, self::DRAFT_FIXTURE_DIR);

            self::assertStringContainsString("#[\\SimpleOrm\\Migrations\\Draft]\nfinal class V0002", $root, 'draft flag ' . var_export($draftFlag, true));
        }
    }

    #[Test]
    public function the_amend_of_a_reviewed_version_stays_reviewed_without_the_flag(): void
    {
        $root = $this->amendedRoot(false, self::REVIEWED_FIXTURE_NAMESPACE, self::REVIEWED_FIXTURE_DIR);

        self::assertStringNotContainsString('Migrations\\Draft]', $root);
    }

    #[Test]
    public function the_refusal_names_the_version(): void
    {
        try {
            $this->runner($this->create(), $this->addNoteDraft())->migrate();
            self::fail('the draft was expected to refuse the run');
        } catch (SimpleOrmException $e) {
            self::assertSame('MIG-013', $e->errorCode);
            self::assertSame('V0002', $e->target);
        }
    }

    #[Test]
    public function migrate_down_ignores_the_marker(): void
    {
        $step = new SqlVersionStep('widgets', 'add_note', ['alter table widgets add column note TEXT'], ['alter table widgets drop column note']);
        $this->runner($this->create(), new SqlVersion(2, $step))->migrate();

        $marked = $this->runner($this->create(), SqlVersion::draft(2, $step));

        self::assertSame(1, $marked->migrateDown(1));
        self::assertSame([[1, 'widgets']], $this->recorded());
        // Rolled back, the version is pending again — and now it is pending work the marker refuses.
        try {
            $marked->migrate();
            self::fail('the rolled-back draft was expected to refuse');
        } catch (SimpleOrmException $e) {
            self::assertSame('MIG-013', $e->errorCode);
        }
    }

    #[Test]
    public function schema_guard_reports_a_pending_draft_as_mig030_and_a_recorded_one_as_nothing(): void
    {
        $this->runner($this->create())->migrate();
        $db = Db::open($this->database->connectionString(), new DbOptions($this->dialect));

        try {
            SchemaGuard::validate($db, [], MigrationSet::of($this->create(), $this->addNoteDraft()));
            self::fail('a pending draft was expected to fail the application check');
        } catch (SchemaValidationException $exception) {
            self::assertSame(['MIG-030'], array_map(static fn ($e): string => $e->code, $exception->errors));
            self::assertSame('V0002 widgets', $exception->errors[0]->target);
        }

        $this->runner($this->create(), $this->plain(2, 'alter table widgets add column note TEXT', 'add_note'))->migrate();
        SchemaGuard::validate($db, [], MigrationSet::of($this->create(), $this->addNoteDraft()));   // recorded: history, no throw
        $this->addToAssertionCount(1);
    }

    #[Test]
    public function the_replay_option_is_the_only_way_past_the_marker(): void
    {
        $replaying = new MigrationRunner(
            $this->connection,
            $this->dialect,
            $this->maps,
            MigrationSet::of($this->create(), $this->addNoteDraft()),
            replayDrafts: true,
        );

        self::assertSame(2, $replaying->migrate());
        self::assertSame([[1, 'widgets'], [2, 'widgets']], $this->recorded());
    }

    // --- helpers ---------------------------------------------------------------------------

    /** @param class-string $entityType */
    private function assertLoadFailsOnlyWithMap027(string $entityType): void
    {
        try {
            $this->maps->load($entityType);
            self::fail("{$entityType} was expected to fail loading");
        } catch (MappingException $e) {
            self::assertSame(['MAP-027'], array_map(static fn ($error): string => $error->code, $e->errors));
        }
    }

    /**
     * The V0002 root `diff --amend` leaves behind, over a tree whose generated V0002 is laid out the way the amend
     * cases do and whose compiled roots are the given fixture's.
     */
    private function amendedRoot(bool $draftFlag, string $namespace, string $migrationsDir): string
    {
        $dir = sys_get_temp_dir() . DIRECTORY_SEPARATOR . 'simpleorm_amend_draft_' . bin2hex(random_bytes(6));
        mkdir($dir . '/Table/AmendWidget', 0777, true);
        $stepRef = 'SimpleOrm\\Tests\\Conformance\\AmendFixture\\Table\\AmendWidget\\V0002_AddNote';
        file_put_contents($dir . '/V0002.php', MigrationGenerator::emitRoot($namespace, 2, [$stepRef], 'sqlite', $draftFlag));
        file_put_contents($dir . '/Table/AmendWidget/V0002_AddNote.php', "<?php\n\n/** Generated by simpleorm diff (ADR-0017); stub */\nfinal class Step\n{\n}\n");
        file_put_contents($dir . '/Table/AmendWidget/V0001.schema.json', json_encode([
            'object' => 'amend_widgets',
            'asOfVersion' => 1,
            'columns' => [
                ['column' => 'id', 'type' => 'INTEGER', 'nullable' => false, 'key' => true, 'generated' => true],
                ['column' => 'name', 'type' => 'TEXT', 'nullable' => false],
            ],
            'indexes' => [],
        ], JSON_THROW_ON_ERROR));

        try {
            $errors = [];
            $exit = DiffCommand::execute(
                new DiffOptions(
                    migrationsDir: $migrationsDir,
                    rootNamespace: $namespace,
                    entityTypes: [AmendWidget::class],
                    outDir: $dir,
                    dialect: $this->dialect,
                    dialectLabel: 'sqlite',
                    name: 'AddRemark',
                    amend: true,
                    draft: $draftFlag,
                ),
                static function (string $line): void {
                },
                static function (string $line) use (&$errors): void {
                    $errors[] = $line;
                },
            );
            self::assertSame(0, $exit, implode("\n", $errors));

            return (string) file_get_contents($dir . '/V0002.php');
        } finally {
            foreach (new RecursiveIteratorIterator(new RecursiveDirectoryIterator($dir, FilesystemIterator::SKIP_DOTS), RecursiveIteratorIterator::CHILD_FIRST) as $entry) {
                $entry->isDir() ? rmdir($entry->getPathname()) : unlink($entry->getPathname());
            }

            rmdir($dir);
        }
    }

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

/** A declaration on a navigation. */
#[Table('draft_renamed_on_navigation')]
final class DraftRenamedOnNavigation
{
    #[Key]
    #[Generated]
    #[Column]
    public int $id;

    #[Column]
    #[ForeignKey(User::class)]
    public int $userId;

    #[ManyToOne('userId')]
    #[RenamedFrom('old')]
    public private(set) ?User $user = null;
}

/** An owned value type whose member declares a rename. */
#[Owned]
final class DraftOwnedWithRename
{
    #[Column]
    #[RenamedFrom('old')]
    public string $street = '';
}

/** An owned value type with no declaration of its own. */
#[Owned]
final class DraftOwnedPlain
{
    #[Column]
    public string $street = '';
}

/** A declaration on an owned navigation. */
#[Table('draft_renamed_on_owned_navigation')]
final class DraftRenamedOnOwnedNavigation
{
    #[Key]
    #[Generated]
    #[Column]
    public int $id;

    #[Owned]
    #[RenamedFrom('old')]
    public ?DraftOwnedPlain $address = null;
}

/** A declaration on a member of an owned type. */
#[Table('draft_renamed_on_owned_member')]
final class DraftRenamedOnOwnedMember
{
    #[Key]
    #[Generated]
    #[Column]
    public int $id;

    #[Owned]
    public ?DraftOwnedWithRename $address = null;
}

/** A declaration on a view's column: a view has no column diff for it to feed. */
#[View('draft_renamed_view', 'select 1 as id, 2 as note')]
final class DraftRenamedOnView
{
    #[Key]
    #[Column]
    public int $id;

    #[Column]
    #[RenamedFrom('old')]
    public ?int $note = null;
}

/** Two columns claiming one previous column (matched case-insensitively, names trimmed). */
#[Table('draft_same_origin')]
final class DraftSameOrigin
{
    #[Key]
    #[Generated]
    #[Column]
    public int $id;

    #[Column]
    #[RenamedFrom('old')]
    public string $first = '';

    #[Column]
    #[RenamedFrom(' OLD ')]
    public string $second = '';
}

/** `note` was `remark`, and the model still maps `remark`: two columns, nothing renamed. */
#[Table('draft_still_mapped')]
final class DraftStillMapped
{
    #[Key]
    #[Generated]
    #[Column]
    public int $id;

    #[Column]
    public ?string $remark = null;

    #[Column]
    #[RenamedFrom('remark')]
    public ?string $note = null;
}

/** Target of the export-order case (built manually). */
final class DraftAudited
{
    public int $id;

    public DateTimeImmutable $stamped;
}

/** Target of the manual-builder cases. */
final class DraftBuilt
{
    public int $id;

    public string $title = '';

    public string $other = '';
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
