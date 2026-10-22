<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Migrations;

use FilesystemIterator;
use PDO;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use RecursiveDirectoryIterator;
use RecursiveIteratorIterator;
use SimpleOrm\Dialect\SqliteDialect;
use SimpleOrm\Dialect\SqliteTriggerParser;
use SimpleOrm\Discovery\ClassScanner;
use SimpleOrm\Errors\SimpleOrmException;
use SimpleOrm\Metadata\EntityMapLoader;
use SimpleOrm\Metadata\TriggerDefinition;
use SimpleOrm\Metadata\TriggerEvent;
use SimpleOrm\Metadata\TriggerTiming;
use SimpleOrm\Migrations\Diff\DiffCommand;
use SimpleOrm\Migrations\Diff\DiffOptions;
use SimpleOrm\Migrations\MigrationGenerator;
use SimpleOrm\Migrations\MigrationRunner;
use SimpleOrm\Migrations\MigrationSet;
use SimpleOrm\Migrations\SnapshotSet;
use SimpleOrm\Migrations\TriggerSchema;
use SimpleOrm\Sample\Models\Tables\Role;
use SimpleOrm\Tests\Support\TempDatabase;

/**
 * Trigger migrations end to end on SQLite (Level 4, ADR-0042), against the sample project's history (V0013 creates the
 * audit trigger): the step applies and the trigger fires, its rollback derives from the trigger snapshot, the
 * generator emits literal-declaration steps for a new, a changed, and a removed trigger, and `diff` gates the removal
 * like a dropped column. The conformance cases pin the same behaviors as data; these use real step classes.
 */
final class TriggerMigrationTest extends TestCase
{
    private const string NAMESPACE = 'SimpleOrm\Sample';

    private TempDatabase $database;

    /** @var list<string> */
    private array $tempDirs = [];

    protected function setUp(): void
    {
        $this->database = TempDatabase::create();
    }

    protected function tearDown(): void
    {
        $this->database->delete();
        foreach ($this->tempDirs as $dir) {
            self::removeDirectory($dir);
        }
    }

    // --- the sample's trigger step: apply, fire, derive the rollback ------------------------------------------------

    #[Test]
    public function the_sample_trigger_step_applies_fires_and_rolls_back_without_any_down_code(): void
    {
        $dialect = new SqliteDialect();
        $connection = $dialect->createConnection($this->database->connectionString());
        $runner = $this->runner($connection, $dialect);

        self::assertSame(13, $runner->migrate());
        $connection->exec('update roles set is_deleted = 0');   // V0005 seeded two roles
        self::assertSame(2, (int) $connection->query('select count(*) from role_changes')->fetchColumn());

        $live = SqliteTriggerParser::definitions($connection, 'trg_roles_audit');
        self::assertCount(1, $live);

        self::assertSame(1, $runner->migrateDown(12));
        self::assertSame([], SqliteTriggerParser::definitions($connection, 'trg_roles_audit'), 'the derived rollback dropped the trigger');
        self::assertSame(0, (int) $connection->query("select count(*) from sqlite_master where name = 'role_changes'")->fetchColumn());
    }

    #[Test]
    public function a_trigger_rewritten_outside_the_code_refuses_the_rollback_with_mig012_and_force_overrides(): void
    {
        $dialect = new SqliteDialect();
        $connection = $dialect->createConnection($this->database->connectionString());
        $runner = $this->runner($connection, $dialect);
        $runner->migrate();

        $connection->exec('drop trigger trg_roles_audit');
        $connection->exec('create trigger trg_roles_audit after update on roles for each row begin select 1; end');

        try {
            $runner->migrateDown(12);
            self::fail('expected MIG-012');
        } catch (SimpleOrmException $e) {
            self::assertSame('MIG-012', $e->errorCode);
        }

        self::assertCount(1, SqliteTriggerParser::definitions($connection, 'trg_roles_audit'), 'the refused run changed nothing: the hotfix stands');

        $notices = [];
        self::assertSame(1, $runner->migrateDown(12, true, function (string $notice) use (&$notices): void {
            $notices[] = $notice;
        }));
        self::assertCount(1, $notices);
        self::assertStringContainsString('trg_roles_audit', $notices[0]);
    }

    #[Test]
    public function the_trigger_step_is_part_of_the_status_under_the_trigger_name(): void
    {
        $dialect = new SqliteDialect();
        $connection = $dialect->createConnection($this->database->connectionString());
        $runner = $this->runner($connection, $dialect);
        $runner->migrate();

        $objects = array_map(
            static fn ($entry): string => $entry->objectName,
            array_filter($runner->status(), static fn ($entry): bool => $entry->version === 13),
        );
        sort($objects);

        self::assertSame(['role_changes', 'trg_roles_audit'], array_values($objects));
    }

    #[Test]
    public function create_trigger_in_a_step_whose_entity_does_not_link_the_class_is_ddl001(): void
    {
        $actions = new \SimpleOrm\Migrations\TriggerActions('roles', false, 'trg_roles_audit', null, new SqliteDialect(), 'Role');

        try {
            $actions->createTrigger();
            self::fail('expected DDL-001');
        } catch (SimpleOrmException $e) {
            self::assertSame('DDL-001', $e->errorCode);
            self::assertSame('Role', $e->target);
        }
    }

    #[Test]
    public function a_removal_step_derives_no_rollback_mig020_like_a_dropped_table(): void
    {
        $snapshot = \SimpleOrm\Migrations\SchemaSnapshot::exportTrigger(
            self::schema('select 1 from {new: id} n;'),
            13,
            new \DateTimeImmutable('2026-10-22T12:00:00Z'),
        );
        $snapshots = new SnapshotSet([$snapshot]);
        $notices = [];

        self::assertNull(\SimpleOrm\Migrations\DownDeriver::derive('trg_roles_audit', 14, $snapshots, [], $notices, new SqliteDialect()));
        self::assertNotNull(\SimpleOrm\Migrations\DownDeriver::derive('trg_roles_audit', 13, $snapshots, [], $notices, new SqliteDialect()));
    }

    // --- the generator -------------------------------------------------------------------------------------------------

    #[Test]
    public function a_new_trigger_step_is_a_literal_recreate(): void
    {
        $source = MigrationGenerator::emitTriggerStep(
            'App\Migrations',
            Role::class,
            \SimpleOrm\Sample\Models\Triggers\RoleAuditTrigger::class,
            14,
            'Auto',
            self::schema('select 1 from {new: id} n;'),
            null,
        );

        self::assertStringContainsString('namespace App\Migrations\Trigger\RoleAuditTrigger;', $source);
        self::assertStringContainsString('final class V0014_Auto extends \SimpleOrm\Migrations\TriggerMigration', $source);
        self::assertStringContainsString('return \SimpleOrm\Sample\Models\Tables\Role::class;', $source);
        self::assertStringContainsString('return \SimpleOrm\Sample\Models\Triggers\RoleAuditTrigger::class;', $source);
        self::assertStringContainsString(
            "\$actions->recreate(\\SimpleOrm\\Metadata\\TriggerTiming::After, [\\SimpleOrm\\Metadata\\TriggerEvent::Update], 'select 1 from {new: id} n;');",
            $source,
        );
        self::assertStringNotContainsString('expectDefinition', $source);
        self::assertTrue(MigrationGenerator::isGenerated($source));
    }

    #[Test]
    public function a_changed_trigger_step_opens_with_the_previous_declaration_as_its_guard(): void
    {
        $source = MigrationGenerator::emitTriggerStep(
            'App\Migrations',
            Role::class,
            \SimpleOrm\Sample\Models\Triggers\RoleAuditTrigger::class,
            14,
            'Auto',
            self::schema('select 2 from {old: id} o;', [TriggerEvent::Update, TriggerEvent::Delete], ['name']),
            self::schema('select 1 from {new: id} n;'),
        );

        $guard = strpos($source, "\$actions->expectDefinition(\\SimpleOrm\\Metadata\\TriggerTiming::After, [\\SimpleOrm\\Metadata\\TriggerEvent::Update], 'select 1 from {new: id} n;');");
        $recreate = strpos($source, "\$actions->recreate(\\SimpleOrm\\Metadata\\TriggerTiming::After, [\\SimpleOrm\\Metadata\\TriggerEvent::Update, \\SimpleOrm\\Metadata\\TriggerEvent::Delete], 'select 2 from {old: id} o;', ['name']);");

        self::assertNotFalse($guard);
        self::assertNotFalse($recreate);
        self::assertLessThan($recreate, $guard);
    }

    #[Test]
    public function a_removal_step_is_a_bare_drop(): void
    {
        $source = MigrationGenerator::emitTriggerStep(
            'App\Migrations',
            Role::class,
            \SimpleOrm\Sample\Models\Triggers\RoleAuditTrigger::class,
            14,
            'Auto',
            null,
            self::schema('select 1 from {new: id} n;'),
        );

        self::assertStringContainsString('$actions->dropTrigger();', $source);
        self::assertStringNotContainsString('recreate', $source);
    }

    #[Test]
    public function an_emitted_step_compiles_and_renders_the_declaration_it_carries(): void
    {
        $source = MigrationGenerator::emitTriggerStep(
            'SimpleOrm\Tests\Migrations\Emitted',
            Role::class,
            \SimpleOrm\Sample\Models\Triggers\RoleAuditTrigger::class,
            14,
            'Probe',
            self::schema("select 'it''s' from {new: id} n;"),
            self::schema('select 1 from {new: id} n;'),
        );
        $dir = $this->tempDir('emitted');
        file_put_contents($dir . '/step.php', $source);
        require $dir . '/step.php';   // declares the generated class (the one place a test loads generated source)

        /** @var class-string<\SimpleOrm\Migrations\TriggerMigration> $class */
        $class = 'SimpleOrm\Tests\Migrations\Emitted\Trigger\RoleAuditTrigger\V0014_Probe';
        $rendered = (new $class())->renderUp(new EntityMapLoader(), new SqliteDialect());

        // guard, the drops, the create from the literal declaration.
        self::assertNotNull($rendered[0]->guardTrigger);
        self::assertStringContainsString("select 'it''s' from (select new.id as id) n;", implode("\n", array_map(static fn ($s): string => $s->sql, $rendered)));
    }

    // --- diff ------------------------------------------------------------------------------------------------------------

    #[Test]
    public function diff_of_the_sample_against_its_own_snapshots_finds_nothing(): void
    {
        [$exit, $output, $errors] = $this->diff(self::entityTypes());

        self::assertSame(0, $exit, implode("\n", $errors));
        self::assertSame(['no schema changes: the model matches the snapshots'], $output);
    }

    #[Test]
    public function a_changed_trigger_declaration_diffs_to_a_guarded_recreate_step_composed_last(): void
    {
        $out = $this->snapshotCopy();
        $file = $out . '/Trigger/RoleAuditTrigger/V0013.schema.json';
        file_put_contents($file, str_replace('select n.id, n.role_name', 'select n.id, upper(n.role_name)', (string) file_get_contents($file)));

        [$exit, $output, $errors] = $this->diff(self::entityTypes(), $out);

        self::assertSame(0, $exit, implode("\n", $errors));
        $step = (string) file_get_contents($out . '/Trigger/RoleAuditTrigger/V0014_Auto.php');
        self::assertStringContainsString('expectDefinition(', $step);
        self::assertStringContainsString("upper(n.role_name)", $step, 'the guard carries the previous (snapshot) declaration');
        self::assertStringContainsString('recreate(', $step);
        self::assertStringContainsString('select n.id, n.role_name, {now}', $step, 'the recreate carries the current model');
        $root = (string) file_get_contents($out . '/V0014.php');
        self::assertStringContainsString('Trigger\RoleAuditTrigger\V0014_Auto', $root);
        self::assertNotEmpty($output);
    }

    #[Test]
    public function a_trigger_without_a_recorded_snapshot_refuses_instead_of_creating_it_anew(): void
    {
        $out = $this->snapshotCopy();
        unlink($out . '/Trigger/RoleAuditTrigger/V0013.schema.json');

        [$exit, , $errors] = $this->diff(self::entityTypes(), $out);

        self::assertSame(1, $exit);
        self::assertStringContainsString('no recorded schema below V0014 for trg_roles_audit', implode("\n", $errors));
    }

    #[Test]
    public function a_removed_trigger_is_gated_by_allow_remove_and_then_a_bare_drop(): void
    {
        $out = $this->snapshotCopy();
        $withoutRole = array_values(array_filter(self::entityTypes(), static fn (string $type): bool => $type !== Role::class));

        [$exit, , $errors] = $this->diff($withoutRole, $out);
        self::assertSame(1, $exit);
        self::assertStringContainsString('DDL-003', implode("\n", $errors));
        self::assertStringContainsString('trigger trg_roles_audit', implode("\n", $errors));
        self::assertFileDoesNotExist($out . '/V0014.php');

        [$exit, , $errors] = $this->diff($withoutRole, $out, allowRemove: true);
        self::assertSame(0, $exit, implode("\n", $errors));
        $step = (string) file_get_contents($out . '/Trigger/RoleAuditTrigger/V0014_Auto.php');
        self::assertStringContainsString('$actions->dropTrigger();', $step);
        self::assertStringContainsString('return \SimpleOrm\Sample\Models\Tables\Role::class;', $step, 'the step names the entity it is on');
    }

    #[Test]
    public function a_trigger_moved_to_another_relation_is_ddl004_by_hand(): void
    {
        $out = $this->snapshotCopy();
        $file = $out . '/Trigger/RoleAuditTrigger/V0013.schema.json';
        file_put_contents($file, str_replace('"relation": "roles"', '"relation": "other_roles"', (string) file_get_contents($file)));

        [$exit, , $errors] = $this->diff(self::entityTypes(), $out);

        self::assertSame(1, $exit);
        self::assertStringContainsString('DDL-004', implode("\n", $errors));
        self::assertStringContainsString('trg_roles_audit', implode("\n", $errors));
    }

    // --- helpers -----------------------------------------------------------------------------------------------------------

    private function runner(PDO $connection, SqliteDialect $dialect): MigrationRunner
    {
        return new MigrationRunner(
            $connection,
            $dialect,
            new EntityMapLoader(),
            MigrationSet::fromDirectory(self::migrationsDir(), self::NAMESPACE . '\Migrations'),
            SnapshotSet::fromDirectory(self::migrationsDir()),
        );
    }

    /**
     * @param list<class-string> $entityTypes
     * @return array{0: int, 1: list<string>, 2: list<string>}
     */
    private function diff(array $entityTypes, ?string $out = null, bool $allowRemove = false): array
    {
        $out ??= $this->snapshotCopy();
        $output = [];
        $errors = [];
        $exit = DiffCommand::execute(
            new DiffOptions(
                self::migrationsDir(),
                self::NAMESPACE . '\Migrations',
                $entityTypes,
                $out,
                new SqliteDialect(),
                'sqlite',
                allowRemove: $allowRemove,
            ),
            static function (string $message) use (&$output): void {
                $output[] = $message;
            },
            static function (string $message) use (&$errors): void {
                $errors[] = $message;
            },
        );

        return [$exit, $output, $errors];
    }

    /** The scanned mapped entities of the sample project, like the CLI's `mappedTypes`. @return list<class-string> */
    private static function entityTypes(): array
    {
        return array_values(array_filter(
            ClassScanner::classes(self::sourceDir(), self::NAMESPACE),
            static fn (string $class): bool => !(new \ReflectionClass($class))->isAbstract()
                && EntityMapLoader::hasMappingAttributes($class)
                && !EntityMapLoader::isOwnedType($class),
        ));
    }

    /** A temp directory holding a copy of every committed snapshot of the sample, in its layout. */
    private function snapshotCopy(): string
    {
        $out = $this->tempDir('snapshots');
        $base = self::migrationsDir();
        $iterator = new RecursiveIteratorIterator(new RecursiveDirectoryIterator($base, FilesystemIterator::SKIP_DOTS));
        foreach ($iterator as $file) {
            if (!str_ends_with($file->getFilename(), '.schema.json')) {
                continue;
            }

            $relative = ltrim(str_replace('\\', '/', substr($file->getPathname(), strlen($base))), '/');
            if (!is_dir(dirname("{$out}/{$relative}"))) {
                mkdir(dirname("{$out}/{$relative}"), 0777, true);
            }

            copy($file->getPathname(), "{$out}/{$relative}");
        }

        return $out;
    }

    /**
     * @param list<TriggerEvent> $events
     * @param list<string> $updateOf
     */
    private static function schema(string $sql, array $events = [TriggerEvent::Update], array $updateOf = []): TriggerSchema
    {
        return new TriggerSchema('roles', false, new TriggerDefinition('trg_roles_audit', TriggerTiming::After, $events, $sql, $updateOf));
    }

    private function tempDir(string $label): string
    {
        $dir = sys_get_temp_dir() . '/simpleorm-trigger-' . $label . '-' . bin2hex(random_bytes(6));
        mkdir($dir, 0777, true);
        $this->tempDirs[] = $dir;

        return $dir;
    }

    private static function sourceDir(): string
    {
        return dirname(__DIR__, 2) . '/samples/SimpleOrm.Sample/src';
    }

    private static function migrationsDir(): string
    {
        return self::sourceDir() . '/Migrations';
    }

    private static function removeDirectory(string $dir): void
    {
        if (!is_dir($dir)) {
            return;
        }

        $iterator = new RecursiveIteratorIterator(
            new RecursiveDirectoryIterator($dir, FilesystemIterator::SKIP_DOTS),
            RecursiveIteratorIterator::CHILD_FIRST,
        );
        foreach ($iterator as $file) {
            $file->isDir() ? rmdir($file->getPathname()) : unlink($file->getPathname());
        }

        rmdir($dir);
    }
}
