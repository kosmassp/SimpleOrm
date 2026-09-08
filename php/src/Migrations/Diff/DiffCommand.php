<?php

declare(strict_types=1);

namespace SimpleOrm\Migrations\Diff;

use ReflectionClass;
use SimpleOrm\Metadata\EntityMap;
use SimpleOrm\Metadata\EntityMapLoader;
use SimpleOrm\Metadata\RelationKind;
use SimpleOrm\Migrations\MigrationGenerator;
use SimpleOrm\Migrations\MigrationSet;
use SimpleOrm\Migrations\MigrationStep;
use SimpleOrm\Migrations\MigrationVersion;
use SimpleOrm\Migrations\SchemaSnapshot;
use SimpleOrm\Migrations\SnapshotSet;
use SimpleOrm\Migrations\TableDiff;
use SimpleOrm\Migrations\TableMigration;
use SimpleOrm\Migrations\TableSchema;
use SimpleOrm\Migrations\VersionBuilder;
use SimpleOrm\Migrations\ViewMigration;

/**
 * `simpleorm diff`: the model is the final truth, the committed snapshots are
 * the recorded past, the difference is the next migration — ordinary source
 * with literal SQL, no database needed (ADR-0017). Mirrors `DiffCommand.cs`.
 * Tables diff by columns, views by normalized DDL; new tables order
 * FK-referenced first; views compose after tables (§7.22). The recorded past
 * must exist: a table that earlier versions migrated but no snapshot describes
 * would diff as brand new, so it refuses and points at `shadow`/`snapshot`.
 *
 * **Amend** (ADR-0017 add.3) regenerates the *newest* version instead: the
 * model against the schema the migrations below it produce (the snapshot
 * history *below* it — a snapshot taken of the draft would otherwise hide the
 * change), replacing the version's files. A version without the generator's
 * header is hand-written — its raw SQL, hooks, and data steps are not
 * reproducible from a diff — so replacing it takes `--force` and names every
 * such file. Nothing on disk changes until every refusal has passed: steps 1-5
 * read, step 6 writes. Git, push state, and team agreement are human protocol;
 * the databases' recorded checksums (`MIG-010`) are the tool's boundary.
 */
final class DiffCommand
{
    /** @var list<string> */
    private const array KIND_FOLDERS = ['Table', 'View', 'MaterializedView'];

    private function __construct()
    {
    }

    /**
     * @param callable(string): void $output
     * @param callable(string): void $error
     */
    public static function execute(DiffOptions $options, callable $output, callable $error): int
    {
        $fail = static function (string $message) use ($error): int {
            $error($message);

            return 1;
        };

        // 1. The target version: the next one, or — amending — the newest one.
        $set = MigrationSet::fromDirectory($options->migrationsDir, $options->rootNamespace);
        $versionNumbers = array_map(static fn (MigrationVersion $v): int => $v->version(), $set->versions());
        $latest = $versionNumbers === [] ? 0 : max($versionNumbers);
        if ($options->amend && $latest === 0) {
            return $fail("nothing to amend: no migration versions under namespace {$options->rootNamespace} in {$options->migrationsDir}");
        }

        $version = $options->amend ? $latest : $latest + 1;
        $prefix = sprintf('V%04d', $version);
        $allSteps = self::allSteps($set);

        // 2-3. Amend: locate the version's files and vet them.
        $replaceable = [];
        $notices = [];
        if ($options->amend) {
            $refusal = self::locateVersionFiles($options, $allSteps, $version, $prefix, $replaceable, $notices);
            if ($refusal !== null) {
                return $fail($refusal);
            }
        }

        // 4-5. The diff, against the schema the migrations below the target
        // version produce — the snapshot history below it. An object those
        // migrations touched but no snapshot records has no recorded past to
        // diff against: it would come out as brand new, so it refuses instead.
        $migratedBelow = self::entitiesMigratedBelow($allSteps, $version);
        $loader = new EntityMapLoader();

        /** @var list<array{type: class-string, map: EntityMap, diff: TableDiff}> $changed */
        $changed = [];
        /** @var list<array{type: class-string, folder: string, objectName: string, ddl: string, previousDdl: ?string}> $viewChanges */
        $viewChanges = [];
        $problems = [];
        $removals = [];
        $unrecorded = [];

        $types = $options->entityTypes;
        usort($types, static fn (string $a, string $b): int => strcmp(self::shortName($a), self::shortName($b)));

        foreach ($types as $type) {
            $map = $loader->load($type);
            $shortName = self::shortName($type);

            if ($map->kind === RelationKind::Table) {
                $snapshotDir = $options->outDir . '/Table/' . $shortName;
                $baseline = self::latestTableSnapshotBelow($snapshotDir, $version);
                if ($baseline === null && in_array($type, $migratedBelow, true)) {
                    $unrecorded[] = $map->relationName;

                    continue;
                }

                $tableRenames = $options->renames[$map->relationName] ?? [];
                $diff = MigrationGenerator::diffMap($map, $options->dialect, $baseline, $tableRenames);
                foreach ($diff->unsupported as $message) {
                    $problems[] = "{$map->relationName}: {$message}";
                }

                foreach ($diff->removed as $column) {
                    $removals[] = "{$map->relationName}.{$column->name}";
                }

                foreach ($diff->removedIndexNames as $name) {
                    $removals[] = "index {$name}";
                }

                if ($diff->hasChanges()) {
                    $changed[] = ['type' => $type, 'map' => $map, 'diff' => $diff];
                }
            } elseif ($map->kind === RelationKind::View
                || ($map->kind === RelationKind::MaterializedView && $options->dialect->supportsMaterializedViews())
            ) {
                // Views diff by DDL (ADR-0017 add.1): the definition is the schema.
                $folder = $map->kind === RelationKind::View ? 'View' : 'MaterializedView';
                $current = SchemaSnapshot::normalizeDdl($options->dialect->createViewSql($map));
                $snapshotDir = $options->outDir . '/' . $folder . '/' . $shortName;
                $previous = self::latestDdlSnapshotBelow($snapshotDir, $version);
                if ($previous === null && in_array($type, $migratedBelow, true)) {
                    $unrecorded[] = $map->relationName;

                    continue;
                }

                if ($previous !== $current) {
                    $viewChanges[] = [
                        'type' => $type,
                        'folder' => $folder,
                        'objectName' => $map->relationName,
                        'ddl' => $current,
                        'previousDdl' => $previous,
                    ];
                }
            }
        }

        if ($unrecorded !== []) {
            $below = sprintf('V%04d', $version - 1);

            return $fail(
                'no recorded schema below ' . $prefix . ' for ' . implode(', ', $unrecorded)
                . ' — earlier versions migrate them but no snapshot describes them, so the diff would create them '
                . "anew; run simpleorm shadow --to {$below} (sqlite) or snapshot on a database migrated to {$below}, then retry",
            );
        }

        if ($problems !== []) {
            foreach ($problems as $problem) {
                $error('DDL-004 ' . $problem);
            }

            return 1;
        }

        if ($removals !== [] && !$options->allowRemove) {
            return $fail('DDL-003 destructive changes need --allow-remove: ' . implode(', ', $removals));
        }

        // The files this run would write, computed before anything is touched.
        $description = $options->name ?? 'Auto';
        /** @var list<array{path: string, content: string}> $planned */
        $planned = [];
        $stepRefs = [];
        if ($changed !== [] || $viewChanges !== []) {
            // New tables first, FK-referenced before referencing; then modified tables by name.
            $newOnes = array_values(array_filter($changed, static fn (array $c): bool => $c['diff']->isNew));
            $newTypes = array_map(static fn (array $c): string => $c['type'], $newOnes);
            $modified = array_values(array_filter($changed, static fn (array $c): bool => !$c['diff']->isNew));
            $ordered = [...self::topologicalByForeignKey($newOnes, $newTypes), ...$modified];

            foreach ($ordered as $entry) {
                $shortName = self::shortName($entry['type']);
                $className = "{$prefix}_{$description}";
                $planned[] = [
                    'path' => "{$options->outDir}/Table/{$shortName}/{$className}.php",
                    'content' => MigrationGenerator::emitTableStep(
                        $options->rootNamespace,
                        $entry['type'],
                        $entry['map'],
                        $options->dialect,
                        $version,
                        $description,
                        $entry['diff'],
                    ),
                ];
                $stepRefs[] = "{$options->rootNamespace}\\Table\\{$shortName}\\{$className}";
            }

            foreach ($viewChanges as $entry) {
                $shortName = self::shortName($entry['type']);
                $className = "{$prefix}_{$description}";
                $planned[] = [
                    'path' => "{$options->outDir}/{$entry['folder']}/{$shortName}/{$className}.php",
                    'content' => MigrationGenerator::emitViewStep(
                        $options->rootNamespace,
                        $entry['type'],
                        $entry['folder'],
                        $entry['objectName'],
                        $version,
                        $description,
                        $entry['ddl'],
                        $entry['previousDdl'],
                    ),
                ];
                $stepRefs[] = "{$options->rootNamespace}\\{$entry['folder']}\\{$shortName}\\{$className}";
            }

            $planned[] = [
                'path' => "{$options->outDir}/{$prefix}.php",
                'content' => MigrationGenerator::emitRoot($options->rootNamespace, $version, $stepRefs, $options->dialectLabel),
            ];
        }

        // A file that exists and is not the tool's own for this version is never overwritten.
        foreach ($planned as $entry) {
            if (is_file($entry['path']) && !self::pathIn($entry['path'], $replaceable)) {
                return $fail("refusing to overwrite {$entry['path']}");
            }
        }

        if ($planned === [] && !$options->amend) {
            $output('no schema changes: the model matches the snapshots');

            return 0;
        }

        if ($options->amend && $options->isApplied !== null && ($options->isApplied)($version)) {
            $output(
                "warning: {$prefix} is recorded as applied in the database; amending changes its checksum, so migrate "
                . 'reports MIG-010 there — migrate down --to ' . ($version - 1) . ' or recreate that database before re-applying',
            );
        }

        // 6. Commit: every refusal has passed; the tree changes here and only here.
        foreach ($notices as $notice) {
            $output($notice);
        }

        $plannedPaths = array_map(static fn (array $p): string => $p['path'], $planned);
        foreach ($replaceable as $path) {
            if (self::pathIn($path, $plannedPaths)) {
                continue;
            }

            unlink($path);
            $output('deleted ' . $path);
        }

        foreach ($planned as $entry) {
            self::ensureDirectory(dirname($entry['path']));
            file_put_contents($entry['path'], $entry['content']);
            $output('wrote ' . $entry['path']);
        }

        if ($planned === []) {
            $output("{$prefix} removed: the model matches the snapshot history below it, so the version has no content");

            return 0;
        }

        $output("review the generated {$prefix}, build, migrate, then refresh snapshots: simpleorm snapshot --out <MigrationsDir>");

        return 0;
    }

    /**
     * Amend steps 2-3: the root, every `V000N_*.php` step under the generator's
     * layout, and any snapshot stamped at that version (it describes the
     * draft). Refuses — naming the file — when the root or a step lacks the
     * generator's header and `--force` was not given (hand-written content is
     * not reproducible from a diff; with force it is replaced and every such
     * file is noted), when the root was stamped for another dialect, or when
     * the number of step files disagrees with the number of compiled steps:
     * the layout diverged from the tool's, and the tool does not guess.
     *
     * @param list<array{step: MigrationStep, version: int}> $allSteps
     * @param list<string> $replaceable appended to, by reference
     * @param list<string> $notices appended to, by reference
     */
    private static function locateVersionFiles(
        DiffOptions $options,
        array $allSteps,
        int $version,
        string $prefix,
        array &$replaceable,
        array &$notices,
    ): ?string {
        $rootFile = "{$options->outDir}/{$prefix}.php";
        if (!is_file($rootFile)) {
            return "amend: {$rootFile} not found — {$prefix} is the newest version under --migrations but has no root file";
        }

        $rootSource = file_get_contents($rootFile);
        $stamped = MigrationGenerator::generatedDialectLabel($rootSource);
        if ($stamped !== null && strcasecmp($stamped, $options->dialectLabel) !== 0) {
            return "amend refuses: {$prefix} was generated for dialect {$stamped}; storage types are dialect-specific — run with --dialect {$stamped}";
        }

        $objectDirs = [];
        foreach (self::KIND_FOLDERS as $kind) {
            $kindDir = "{$options->outDir}/{$kind}";
            if (is_dir($kindDir)) {
                foreach (SnapshotSet::subdirectories($kindDir) as $dir) {
                    $objectDirs[] = $dir;
                }
            }
        }

        $stepFiles = [];
        foreach ($objectDirs as $dir) {
            foreach (glob("{$dir}/{$prefix}_*.php") ?: [] as $file) {
                $stepFiles[] = $file;
            }
        }

        sort($stepFiles, SORT_STRING);

        foreach ([$rootFile, ...$stepFiles] as $file) {
            $content = $file === $rootFile ? $rootSource : file_get_contents($file);
            if (MigrationGenerator::isGenerated($content)) {
                continue;
            }

            if (!$options->force) {
                return "amend refuses: {$file} is hand-written (no '" . MigrationGenerator::GENERATED_MARKER . "' header) — "
                    . 'its raw SQL, hooks, data steps, and down() are not reproducible from the diff — '
                    . 'add --force to replace it anyway and re-add those by hand';
            }

            $notices[] = "replacing hand-written {$file} (--force): re-add by hand any raw SQL, hooks, data steps, or down() it carried that still apply";
        }

        $compiledSteps = 0;
        foreach ($allSteps as $entry) {
            if (str_starts_with((new ReflectionClass($entry['step']))->getShortName(), $prefix . '_')) {
                $compiledSteps++;
            }
        }

        if ($compiledSteps !== count($stepFiles)) {
            return "amend refuses: {$prefix} compiles {$compiledSteps} step(s) but " . count($stepFiles)
                . " {$prefix}_*.php file(s) exist under {$options->outDir} — the layout diverged from the generator's";
        }

        $replaceable[] = $rootFile;
        foreach ($stepFiles as $file) {
            $replaceable[] = $file;
        }

        foreach ($objectDirs as $dir) {
            foreach (glob("{$dir}/{$prefix}.schema.json") ?: [] as $file) {
                $replaceable[] = $file;
            }
        }

        return null;
    }

    /**
     * Every step composed by every discovered version, with its root's
     * version number — the PHP counterpart of scanning an assembly's types
     * (CODING-STANDARD §10): `MigrationSet::fromDirectory()` already resolved
     * which classes exist under the namespace.
     *
     * @return list<array{step: MigrationStep, version: int}>
     */
    private static function allSteps(MigrationSet $set): array
    {
        $result = [];
        foreach ($set->versions() as $version) {
            $builder = new VersionBuilder();
            $version->compose($builder);
            foreach ($builder->steps() as $step) {
                $result[] = ['step' => $step, 'version' => $version->version()];
            }
        }

        return $result;
    }

    /**
     * The entity types that migration steps below `$version` touch: objects
     * with a migration history, which therefore need a recorded schema to
     * diff against.
     *
     * @param list<array{step: MigrationStep, version: int}> $allSteps
     * @return list<class-string>
     */
    private static function entitiesMigratedBelow(array $allSteps, int $version): array
    {
        /** @var array<class-string, true> $migrated */
        $migrated = [];
        foreach ($allSteps as $entry) {
            if ($entry['version'] >= $version) {
                continue;
            }

            $step = $entry['step'];
            if ($step instanceof TableMigration || $step instanceof ViewMigration) {
                $migrated[$step->entityClass()] = true;
            }
        }

        return array_keys($migrated);
    }

    /** The latest table snapshot strictly below `$version`, or null when none is recorded (a new table, or unrecorded history). */
    private static function latestTableSnapshotBelow(string $dir, int $version): ?TableSchema
    {
        if (!is_dir($dir)) {
            return null;
        }

        $best = null;
        $bestVersion = -1;
        foreach (glob("{$dir}/V*.schema.json") ?: [] as $file) {
            $parsed = SchemaSnapshot::parse(file_get_contents($file));
            if ($parsed->asOfVersion < $version && $parsed->asOfVersion > $bestVersion) {
                $bestVersion = $parsed->asOfVersion;
                $best = $parsed->schema;
            }
        }

        return $best;
    }

    /** The latest view/materialized-view DDL snapshot strictly below `$version`, or null when none is recorded. */
    private static function latestDdlSnapshotBelow(string $dir, int $version): ?string
    {
        if (!is_dir($dir)) {
            return null;
        }

        $best = null;
        $bestVersion = -1;
        foreach (glob("{$dir}/V*.schema.json") ?: [] as $file) {
            $parsed = SchemaSnapshot::parseDdl(file_get_contents($file));
            if ($parsed->asOfVersion < $version && $parsed->asOfVersion > $bestVersion) {
                $bestVersion = $parsed->asOfVersion;
                $best = $parsed->ddl;
            }
        }

        return $best;
    }

    /**
     * @param list<array{type: class-string, map: EntityMap, diff: TableDiff}> $newTables
     * @param list<class-string> $newTypes
     * @return list<array{type: class-string, map: EntityMap, diff: TableDiff}>
     */
    private static function topologicalByForeignKey(array $newTables, array $newTypes): array
    {
        $ordered = [];
        $visited = [];

        $visit = function (array $node) use (&$visit, &$ordered, &$visited, $newTables, $newTypes): void {
            if (isset($visited[$node['type']])) {
                return;
            }

            $visited[$node['type']] = true;

            foreach ($node['map']->properties as $property) {
                if ($property->foreignKeyReferences === null || !in_array($property->foreignKeyReferences, $newTypes, true)) {
                    continue;
                }

                foreach ($newTables as $candidate) {
                    if ($candidate['type'] === $property->foreignKeyReferences) {
                        $visit($candidate);

                        break;
                    }
                }
            }

            $ordered[] = $node;
        };

        foreach ($newTables as $node) {
            $visit($node);
        }

        return $ordered;
    }

    /** @param class-string $fqcn */
    private static function shortName(string $fqcn): string
    {
        $pos = strrpos($fqcn, '\\');

        return $pos === false ? $fqcn : substr($fqcn, $pos + 1);
    }

    /** @param list<string> $haystack */
    private static function pathIn(string $path, array $haystack): bool
    {
        foreach ($haystack as $candidate) {
            if (strcasecmp($candidate, $path) === 0) {
                return true;
            }
        }

        return false;
    }

    private static function ensureDirectory(string $dir): void
    {
        if (!is_dir($dir)) {
            mkdir($dir, 0777, true);
        }
    }
}
