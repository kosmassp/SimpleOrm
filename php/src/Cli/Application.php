<?php

declare(strict_types=1);

namespace SimpleOrm\Cli;

use DateTimeImmutable;
use DateTimeZone;
use SimpleOrm\Dialect\Dialect;
use SimpleOrm\Dialect\SqliteDialect;
use SimpleOrm\Dialect\SqliteShadow;
use SimpleOrm\Discovery\ClassScanner;
use SimpleOrm\Errors\SchemaValidationException;
use SimpleOrm\Errors\SimpleOrmException;
use SimpleOrm\Metadata\EntityMapJson;
use SimpleOrm\Metadata\EntityMapLoader;
use SimpleOrm\Metadata\RelationKind;
use SimpleOrm\Migrations\Diff\DiffCommand;
use SimpleOrm\Migrations\Diff\DiffOptions;
use SimpleOrm\Migrations\MigrationRunner;
use SimpleOrm\Migrations\MigrationSet;
use SimpleOrm\Migrations\MigrationState;
use SimpleOrm\Migrations\SchemaSnapshot;
use SimpleOrm\Migrations\SchemaSync;
use SimpleOrm\Migrations\SnapshotSet;
use SimpleOrm\Naming\SnakeCaseNamingConvention;
use SimpleOrm\Session\Db;
use SimpleOrm\Session\DbOptions;
use SimpleOrm\Validation\SchemaGuard;

/**
 * `bin/simpleorm` (§7.24): migration is always an explicit act — the
 * application never migrates at startup. Mirrors
 * `dotnet/src/SimpleOrm.Cli/Program.cs`, adapted for PHP's lack of assemblies
 * (CODING-STANDARD §10): entities/registries are discovered anywhere under
 * `--src` (its PSR-4 root, named by `--namespace`) via {@see ClassScanner};
 * migrations live under `--migrations` (default `<src>/Migrations`) with
 * namespace `--migrations-namespace` (default `<namespace>\Migrations`);
 * snapshots default to the migrations directory, and so does `--out` for
 * `snapshot`/`diff`/`shadow`. `--dialect` accepts only `sqlite` for now —
 * other dialects refuse by name, naming ADR-0026.
 */
final class Application
{
    private function __construct()
    {
    }

    /** @param list<string> $args argv without the script name */
    public static function main(array $args): int
    {
        $arguments = new Arguments($args);
        if ($arguments->positional === []) {
            return self::usage();
        }

        try {
            $command = strtolower($arguments->positional[0]);

            return match (true) {
                $command === 'migrate' && ($arguments->positional[1] ?? null) === 'down' => self::migrateDown($arguments),
                $command === 'migrate' => self::migrate($arguments),
                $command === 'status' => self::status($arguments),
                $command === 'baseline' => self::baseline($arguments),
                $command === 'export-metadata' => self::exportMetadata($arguments),
                $command === 'validate' => self::validate($arguments),
                $command === 'snapshot' => self::snapshot($arguments),
                $command === 'diff' => self::diff($arguments),
                $command === 'shadow' => self::shadow($arguments),
                default => self::usage(),
            };
        } catch (SchemaValidationException $exception) {
            fwrite(STDERR, $exception->getMessage() . PHP_EOL);

            return 1;
        } catch (SimpleOrmException $exception) {
            fwrite(STDERR, $exception->getMessage() . PHP_EOL);

            return 1;
        }
    }

    // --- commands ----------------------------------------------------------------------

    private static function migrate(Arguments $arguments): int
    {
        [$db, $runner] = self::openRunner($arguments);
        $applied = $runner->migrate($arguments->flag('force'), self::viewDriftNotifier());
        echo ($applied === 0 ? 'nothing pending' : "applied {$applied} version(s)") . PHP_EOL;

        return $arguments->flag('force') ? self::forceSync($db, $arguments) : 0;
    }

    private static function forceSync(Db $db, Arguments $arguments): int
    {
        $plan = SchemaSync::plan($db->connection(), $db->options()->dialect, $db->maps(), self::mappedTypes($arguments));
        SchemaSync::apply($db->connection(), $plan->additive);
        foreach ($plan->additive as $sql) {
            echo 'sync: ' . $sql . PHP_EOL;
        }

        if ($plan->deletions !== []) {
            if ($arguments->flag('allow-delete')) {
                SchemaSync::apply($db->connection(), $plan->deletions);
                foreach ($plan->deletions as $sql) {
                    echo 'sync (destructive): ' . $sql . PHP_EOL;
                }
            } else {
                foreach ($plan->deletions as $sql) {
                    echo 'DDL-003 skipped (needs --allow-delete): ' . $sql . PHP_EOL;
                }
            }
        }

        foreach ($plan->unsupported as $message) {
            fwrite(STDERR, 'DDL-004 ' . $message . PHP_EOL);
        }

        if ($plan->isEmpty()) {
            echo 'schema already matches the model' . PHP_EOL;
        }

        return $plan->unsupported !== [] ? 1 : 0;
    }

    private static function migrateDown(Arguments $arguments): int
    {
        $to = $arguments->option('to');
        if ($to === null || !ctype_digit($to)) {
            return self::fail('migrate down requires --to <version>');
        }

        $target = (int) $to;
        [, $runner] = self::openRunner($arguments);
        $reverted = $runner->migrateDown($target, $arguments->flag('force'), self::viewDriftNotifier());
        echo sprintf('reverted %d version(s); now at <= V%04d', $reverted, $target) . PHP_EOL;

        return 0;
    }

    private static function status(Arguments $arguments): int
    {
        [, $runner] = self::openRunner($arguments);
        foreach ($runner->status() as $entry) {
            echo $entry . PHP_EOL;
        }

        return 0;
    }

    private static function baseline(Arguments $arguments): int
    {
        $raw = $arguments->option('version');
        if ($raw === null || !ctype_digit($raw)) {
            return self::fail('baseline requires --version <N>');
        }

        $version = (int) $raw;
        [, $runner] = self::openRunner($arguments);
        $runner->baseline($version);
        echo sprintf('baselined at V%04d', $version) . PHP_EOL;

        return 0;
    }

    private static function validate(Arguments $arguments): int
    {
        $db = self::openDb($arguments);
        $set = MigrationSet::fromDirectory(self::migrationsDir($arguments), self::migrationsNamespace($arguments));
        $snapshots = SnapshotSet::fromDirectory(self::snapshotsDir($arguments));
        SchemaGuard::validate($db, self::classes($arguments), $set, $snapshots);
        echo 'valid' . PHP_EOL;

        return 0;
    }

    private static function snapshot(Arguments $arguments): int
    {
        $outDir = $arguments->option('out') ?? self::migrationsDir($arguments);
        [$db, $runner] = self::openRunner($arguments);

        // Last version touching each object, from the code-side plan (any state).
        $perObject = [];
        foreach ($runner->status() as $entry) {
            $perObject[$entry->objectName] = max($perObject[$entry->objectName] ?? PHP_INT_MIN, $entry->version);
        }

        $generatedAt = new DateTimeImmutable('now', new DateTimeZone('UTC'));
        foreach (self::mappedTypes($arguments) as $type) {
            $map = $db->maps()->load($type);
            if ($map->relationName === null || !isset($perObject[$map->relationName])) {
                continue;
            }

            $version = $perObject[$map->relationName];
            $dialect = $db->options()->dialect;

            // Tables snapshot by columns; views by DDL (ADR-0017 add.1). Kinds
            // the dialect cannot create (materialized views here) have no
            // history to record.
            [$folder, $content] = match (true) {
                $map->kind === RelationKind::Table => ['Table', SchemaSnapshot::export(SchemaSnapshot::fromMap($map, $dialect), $version, $generatedAt)],
                $map->kind === RelationKind::View => ['View', SchemaSnapshot::exportDdl($map->relationName, 'view', $dialect->createViewSql($map), $version, $generatedAt)],
                $map->kind === RelationKind::MaterializedView && $dialect->supportsMaterializedViews() => [
                    'MaterializedView',
                    SchemaSnapshot::exportDdl($map->relationName, 'materialized_view', $dialect->createViewSql($map), $version, $generatedAt),
                ],
                default => [null, null],
            };

            if ($content === null || $folder === null) {
                continue;
            }

            $directory = "{$outDir}/{$folder}/" . self::shortName($type);
            if (!is_dir($directory)) {
                mkdir($directory, 0777, true);
            }

            $file = $directory . '/' . sprintf('V%04d.schema.json', $version);
            file_put_contents($file, $content . "\n");
            echo 'wrote ' . $file . PHP_EOL;
        }

        return 0;
    }

    private static function diff(Arguments $arguments): int
    {
        $migrationsDir = self::migrationsDir($arguments);
        $migrationsNamespace = self::migrationsNamespace($arguments);
        $outDir = $arguments->option('out') ?? $migrationsDir;
        $dialect = self::dialectFor($arguments);
        $dialectLabel = strtolower($arguments->option('dialect') ?? 'sqlite');

        // Renames are declared, never inferred: --rename <table>.<old>=<new>, repeatable.
        $renames = [];
        foreach ($arguments->all('rename') as $raw) {
            $dot = strpos($raw, '.');
            $eq = strpos($raw, '=');
            if ($dot === false || $dot === 0 || $eq === false || $eq <= $dot + 1 || $eq === strlen($raw) - 1) {
                return self::fail("--rename '{$raw}': expected <table>.<oldColumn>=<newColumn>");
            }

            $table = substr($raw, 0, $dot);
            $renames[$table] ??= [];
            $renames[$table][substr($raw, $dot + 1, $eq - $dot - 1)] = substr($raw, $eq + 1);
        }

        $isApplied = null;
        if ($arguments->flag('amend') && $arguments->option('db') !== null) {
            $isApplied = static function (int $version) use ($arguments): bool {
                [, $runner] = self::openRunner($arguments);
                foreach ($runner->status() as $entry) {
                    if ($entry->version === $version && ($entry->state === MigrationState::Applied || $entry->state === MigrationState::Drifted)) {
                        return true;
                    }
                }

                return false;
            };
        }

        $options = new DiffOptions(
            $migrationsDir,
            $migrationsNamespace,
            self::mappedTypes($arguments),
            $outDir,
            $dialect,
            $dialectLabel,
            $arguments->option('name'),
            $renames,
            $arguments->flag('allow-remove'),
            $arguments->flag('amend'),
            $arguments->flag('force'),
            $isApplied,
        );

        return DiffCommand::execute(
            $options,
            static function (string $message): void {
                echo $message . PHP_EOL;
            },
            static function (string $message): void {
                fwrite(STDERR, $message . PHP_EOL);
            },
        );
    }

    private static function shadow(Arguments $arguments): int
    {
        $dialectName = $arguments->option('dialect');
        if ($dialectName !== null && strcasecmp($dialectName, 'sqlite') !== 0) {
            return self::fail(
                "shadow replays into a throwaway SQLite database and supports only --dialect sqlite; "
                    . "'{$dialectName}' is not ported yet (ADR-0026) — its rollbacks need Down() overrides "
                    . 'until snapshot tooling learns that dialect',
            );
        }

        $outDir = $arguments->option('out') ?? self::migrationsDir($arguments);

        $from = 0;
        if (($fromRaw = $arguments->option('from')) !== null) {
            $parsed = self::parseVersion($fromRaw);
            if ($parsed === null) {
                return self::fail("--from '{$fromRaw}': expected V000N or a number");
            }

            $from = $parsed;
        }

        $to = PHP_INT_MAX;
        if (($toRaw = $arguments->option('to')) !== null) {
            $parsed = self::parseVersion($toRaw);
            if ($parsed === null) {
                return self::fail("--to '{$toRaw}': expected V000M or a number");
            }

            $to = $parsed;
        }

        $set = MigrationSet::fromDirectory(self::migrationsDir($arguments), self::migrationsNamespace($arguments));
        $result = SqliteShadow::rebuildSnapshots($set, new EntityMapLoader(), $outDir, $from, $to);

        foreach ($result->notes as $note) {
            echo $note . PHP_EOL;
        }

        foreach ($result->writtenFiles as $file) {
            echo 'wrote ' . $file . PHP_EOL;
        }

        echo ($result->writtenFiles === [] ? 'nothing to regenerate' : sprintf('regenerated %d snapshot(s)', count($result->writtenFiles))) . PHP_EOL;

        return 0;
    }

    private static function exportMetadata(Arguments $arguments): int
    {
        $loader = new EntityMapLoader();
        $convention = new SnakeCaseNamingConvention();
        $outDir = $arguments->option('out');
        if ($outDir !== null && !is_dir($outDir)) {
            mkdir($outDir, 0777, true);
        }

        foreach (self::mappedTypes($arguments) as $type) {
            $json = EntityMapJson::export($loader->load($type));
            if ($outDir === null) {
                echo $json . PHP_EOL;
            } else {
                $file = $outDir . '/' . $convention->toDatabase(self::shortName($type)) . '.json';
                file_put_contents($file, $json . "\n");
                echo 'wrote ' . $file . PHP_EOL;
            }
        }

        return 0;
    }

    // --- shared context ------------------------------------------------------------------

    private static function usage(): int
    {
        echo <<<'TXT'
            simpleorm — SQL-first micro-ORM CLI

            commands (all need --src <dir> --namespace <Ns>; migrate/status/baseline/validate/snapshot also --db):
              migrate [--force [--allow-delete]]
                                          apply pending versions; a guarded view step refuses
                                          when the live view was changed outside the code
                                          (MIG-012) unless --force recreates it; --force then
                                          also syncs the live schema to the model (additive
                                          only; deletions need --allow-delete, DDL-003)
              migrate down --to <N> [--snapshots <dir>] [--force]
                                          revert versions above N; rollbacks derive from the
                                          snapshots (the migrations directory, or --snapshots);
                                          Down() overrides; no snapshot is MIG-020
              status                      list (version, object) states
              baseline --version <N>      record versions <= N without running them
              export-metadata [--out <dir>]
                                          write each entity's EntityMap JSON
              validate                    SchemaGuard: full report or exit 0
              snapshot [--out <dir>]      write V000N.schema.json per object (tables by
                                          columns; views by DDL), versioned and timestamped
              diff [--out <dir>] [--name <Description>] [--rename table.old=new]...
                   [--allow-remove] [--amend [--force] [--db <value>]]
                                          generate the next migration version from the model vs
                                          the latest snapshots; no database needed unless
                                          amending against one (a MIG-010 heads-up on an
                                          applied draft); removals need --allow-remove
                                          (DDL-003); inexpressible changes are DDL-004
              shadow [--out <dir>] [--from V000N] [--to V000M]
                                          rebuild snapshots by replaying migrations in a
                                          throwaway database; --from trusts version N as
                                          correct and regenerates only (N, M]

            options:
              --src <dir>              PSR-4-reachable root holding entities and registries
              --namespace <Ns>         the --src root's namespace
              --migrations <dir>       migrations directory (default: <src>/Migrations)
              --migrations-namespace <ns>
                                       migrations namespace (default: <namespace>\Migrations)
              --snapshots <dir>        snapshots for migrate down (default: the migrations dir)
              --out <dir>              output for snapshot/diff/shadow (default: the migrations dir)
              --db <value>             a bare SQLite path, a sqlite: DSN, or Data Source=...
              --dialect <name>         sqlite (default); other dialects are not ported yet (ADR-0026)
            TXT . PHP_EOL;

        return 2;
    }

    private static function fail(string $message): int
    {
        fwrite(STDERR, $message . PHP_EOL);

        return 1;
    }

    /** @return callable(string): void */
    private static function viewDriftNotifier(): callable
    {
        return static function (string $message): void {
            echo 'MIG-012 ' . $message . PHP_EOL;
        };
    }

    private static function dialectFor(Arguments $arguments): Dialect
    {
        $name = strtolower($arguments->option('dialect') ?? 'sqlite');
        if ($name === 'sqlite') {
            return new SqliteDialect();
        }

        throw new SimpleOrmException('CLI', 'arguments', "--dialect '{$name}' is not ported yet (ADR-0026); only sqlite is available");
    }

    private static function srcDir(Arguments $arguments): string
    {
        return $arguments->option('src') ?? throw new SimpleOrmException('CLI', 'arguments', '--src <dir> is required');
    }

    private static function rootNamespace(Arguments $arguments): string
    {
        return $arguments->option('namespace') ?? throw new SimpleOrmException('CLI', 'arguments', '--namespace <Ns> is required');
    }

    private static function migrationsDir(Arguments $arguments): string
    {
        return $arguments->option('migrations') ?? (rtrim(str_replace('\\', '/', self::srcDir($arguments)), '/') . '/Migrations');
    }

    private static function migrationsNamespace(Arguments $arguments): string
    {
        return $arguments->option('migrations-namespace') ?? (rtrim(self::rootNamespace($arguments), '\\') . '\\Migrations');
    }

    private static function snapshotsDir(Arguments $arguments): string
    {
        return $arguments->option('snapshots') ?? self::migrationsDir($arguments);
    }

    /** @return list<class-string> */
    private static function classes(Arguments $arguments): array
    {
        return ClassScanner::classes(self::srcDir($arguments), self::rootNamespace($arguments));
    }

    /** @return list<class-string> */
    private static function mappedTypes(Arguments $arguments): array
    {
        return array_values(array_filter(self::classes($arguments), EntityMapLoader::hasMappingAttributes(...)));
    }

    private static function openDb(Arguments $arguments): Db
    {
        $db = $arguments->option('db') ?? throw new SimpleOrmException('CLI', 'arguments', '--db <path or connection string> is required');

        return Db::open($db, new DbOptions(self::dialectFor($arguments)));
    }

    /** @return array{0: Db, 1: MigrationRunner} */
    private static function openRunner(Arguments $arguments): array
    {
        $db = self::openDb($arguments);
        $set = MigrationSet::fromDirectory(self::migrationsDir($arguments), self::migrationsNamespace($arguments));
        // Rollbacks derive from the snapshots (ADR-0018): the migrations dir by default, or --snapshots.
        $snapshots = SnapshotSet::fromDirectory(self::snapshotsDir($arguments));
        $runner = new MigrationRunner($db->connection(), $db->options()->dialect, $db->maps(), $set, $snapshots);

        return [$db, $runner];
    }

    private static function parseVersion(string $raw): ?int
    {
        $stripped = (str_starts_with($raw, 'V') || str_starts_with($raw, 'v')) ? substr($raw, 1) : $raw;

        return $stripped !== '' && ctype_digit($stripped) ? (int) $stripped : null;
    }

    private static function shortName(string $fqcn): string
    {
        $slash = strrpos($fqcn, '\\');

        return $slash === false ? $fqcn : substr($fqcn, $slash + 1);
    }
}
