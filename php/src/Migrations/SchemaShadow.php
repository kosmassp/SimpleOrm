<?php

declare(strict_types=1);

namespace SimpleOrm\Migrations;

use DateTimeImmutable;
use DateTimeZone;
use PDO;
use SimpleOrm\Dialect\Dialect;
use SimpleOrm\Dialect\Dialects;
use SimpleOrm\Dialect\ShadowHost;
use SimpleOrm\Errors\SimpleOrmException;
use SimpleOrm\Metadata\EntityMapLoader;
use SimpleOrm\Metadata\RelationKind;
use SimpleOrm\Parameters\PdoBinder;
use SimpleOrm\Parameters\SqlPlaceholders;
use SimpleOrm\Session\StatementLog;
use Throwable;

/**
 * Rebuilds the versioned schema snapshots (`V000N.schema.json`) by replaying the migration history into a throwaway
 * database and recording each touched object after each version (ADR-0017, on every dialect since ADR-0043). The
 * database comes from the dialect's {@see ShadowHost} — a temp file on SQLite, a `simpleorm_shadow_<hex>` database
 * created on the admin connection and dropped afterwards (failure included) on SQL Server and PostgreSQL. The full
 * rebuild replays from empty; the range form (`$fromVersion`/`$toVersion`) **trusts version `$fromVersion` as correct**:
 * the baseline is rendered from the committed snapshots at or below it through the dialect (a snapshot stamped for another
 * dialect is `MIG-014`), without verifying anything below it, and only `($fromVersion, $toVersion]` replays.
 *
 * **Tables come from the engine** (`ColumnsInfoSql`/`IndexesInfoSql`); **views and triggers come from the replayed step**
 * (ADR-0043, spec/migrations.md "Dialects"): an engine's catalog may not hand them back as declared — PostgreSQL
 * reformats a view, SQL Server's `inserted` has lost a token's column list. A view is the step's last create statement,
 * normalized (a later drop ends its history; a step with no create falls back to the catalog where the engine stores the
 * text verbatim and is skipped with a note where it does not); a trigger is the declaration the step's actions leave
 * behind. Where the catalog is lossless the shadow reads it too and **notes** a disagreement — never writes a different
 * file.
 *
 * **PHP adaptation:** the C# reference resolves each touched relation back to its mapped entity by scanning the whole
 * assembly. PHP has no assembly to scan (CODING-STANDARD §10), so this walks the other direction — the steps already carry
 * `entityClass()` (needed to render their own SQL), so the entity, and therefore its relation name and snapshot folder,
 * come directly from the step composing each version.
 */
final class SchemaShadow
{
    /** The statement log (ADR-0045), every line written as `shadow`: the replay, its introspection, the view and trigger read-back. */
    private readonly StatementLog $log;

    public function __construct(
        private readonly Dialect $dialect,
        private readonly ShadowHost $host,
        private readonly string $adminConnectionString = '',
        ?StatementLog $log = null,
    ) {
        $this->log = ($log ?? StatementLog::off())->named('shadow');
    }

    /** The shadow of a dialect with its own host; `$adminConnectionString` is `--db` on SQL Server and PostgreSQL (ignored on SQLite). */
    public static function for(Dialect $dialect, string $adminConnectionString = '', ?StatementLog $log = null): self
    {
        return new self($dialect, Dialects::shadowHost($dialect), $adminConnectionString, $log);
    }

    public function rebuildSnapshots(
        MigrationSet $set,
        EntityMapLoader $maps,
        string $migrationsDir,
        int $fromVersion = 0,
        int $toVersion = PHP_INT_MAX,
    ): ShadowResult {
        $result = new ShadowResult();
        $versions = $set->versions();
        if ($versions === []) {
            $result->notes[] = 'no migration versions found';

            return $result;
        }

        if ($this->dialect->name() !== Dialects::SQLITE && trim($this->adminConnectionString) === '') {
            throw new SimpleOrmException(
                'CLI',
                '--db',
                "shadow on {$this->dialect->name()} creates and drops a throwaway database: --db must be the admin connection string (with the right to create databases)",
            );
        }

        $shadowConnectionString = $this->host->provision($this->adminConnectionString);
        try {
            $this->replay($set, $maps, $migrationsDir, $fromVersion, $toVersion, $shadowConnectionString, $result);
        } catch (Throwable $failure) {
            try {
                $this->host->drop($this->adminConnectionString, $shadowConnectionString);
            } catch (Throwable) {
                // The replay's failure is the one to report; the throwaway database is best-effort here.
            }

            throw $failure;
        }

        $this->host->drop($this->adminConnectionString, $shadowConnectionString);

        return $result;
    }

    private function replay(
        MigrationSet $set,
        EntityMapLoader $maps,
        string $migrationsDir,
        int $fromVersion,
        int $toVersion,
        string $shadowConnectionString,
        ShadowResult $result,
    ): void {
        $versions = $set->versions();
        $generatedAt = new DateTimeImmutable('now', new DateTimeZone('UTC'));
        $connection = $this->dialect->createConnection($shadowConnectionString);

        if ($fromVersion > 0) {
            // Trust: rebuild the state at fromVersion from the committed snapshots, verify nothing below it.
            $this->restoreBaseline($connection, $migrationsDir, $fromVersion, $result);
            (new MigrationRunner($connection, $this->dialect, $maps, MigrationSet::of(...$versions), replayDrafts: true, log: $this->log))->baseline($fromVersion);
        }

        foreach ($versions as $version) {
            if ($version->version() <= $fromVersion || $version->version() > $toVersion) {
                continue;
            }

            $subset = array_values(array_filter($versions, static fn ($v): bool => $v->version() <= $version->version()));
            (new MigrationRunner($connection, $this->dialect, $maps, MigrationSet::of(...$subset), replayDrafts: true, log: $this->log))->migrate();

            $builder = new VersionBuilder();
            $version->compose($builder);
            foreach ($builder->steps() as $step) {
                $this->snapshotStep($connection, $maps, $step, $version->version(), $migrationsDir, $generatedAt, $result);
            }
        }
    }

    // --- per-step snapshotting -------------------------------------------------------

    private function snapshotStep(
        PDO $connection,
        EntityMapLoader $maps,
        MigrationStep $step,
        int $version,
        string $migrationsDir,
        DateTimeImmutable $generatedAt,
        ShadowResult $result,
    ): void {
        $stamp = $this->dialect->name();
        if ($step instanceof TableMigration) {
            $map = $maps->load($step->entityClass());
            $schema = $this->introspectTable($connection, (string) $map->relationName);
            if ($schema === null) {
                return;   // dropped at this version — its snapshot history ends here
            }

            $this->writeSnapshot(
                $migrationsDir,
                'Table',
                self::shortName($step->entityClass()),
                $version,
                SchemaSnapshot::export($schema, $version, $generatedAt, $stamp),
                $result,
            );

            return;
        }

        if ($step instanceof TriggerMigration) {
            $this->snapshotTrigger($connection, $maps, $step, $version, $migrationsDir, $generatedAt, $result);

            return;
        }

        if ($step instanceof ViewMigration) {
            $map = $maps->load($step->entityClass());
            $relation = (string) $map->relationName;
            $materialized = $map->kind === RelationKind::MaterializedView;
            $ddl = $this->viewDdl($connection, $step->renderUp($maps, $this->dialect), $relation, $version, $result);
            if ($ddl === null) {
                return;
            }

            $this->writeSnapshot(
                $migrationsDir,
                $materialized ? 'MaterializedView' : 'View',
                self::shortName($step->entityClass()),
                $version,
                SchemaSnapshot::exportDdl($relation, $materialized ? 'materialized_view' : 'view', $ddl, $version, $generatedAt, $stamp),
                $result,
            );
        }
    }

    /**
     * A trigger's snapshot is the declaration the step's actions leave behind (ADR-0043); where the host reads the catalog
     * back the stored trigger is checked against it and a disagreement is noted. A step that creates and drops nothing
     * (only a guard) falls back to the catalog where it can be read, and is skipped with a note where it cannot.
     */
    private function snapshotTrigger(
        PDO $connection,
        EntityMapLoader $maps,
        TriggerMigration $step,
        int $version,
        string $migrationsDir,
        DateTimeImmutable $generatedAt,
        ShadowResult $result,
    ): void {
        $name = $step->objectName($maps);
        $actions = $step->upActions($maps, $this->dialect);
        $readBack = null;
        $note = null;
        if ($this->host->canReadTriggers()) {
            $readBack = $this->host->readTrigger($connection, $name, $note, $this->log);
            if ($note !== null) {
                $result->notes[] = sprintf('V%04d: %s', $version, $note);
            }
        }

        if ($actions->leavesDeclaration()) {
            $schema = $actions->declared();
            if ($schema === null) {
                return;   // dropped at this version — its snapshot history ends here
            }

            if ($this->host->canReadTriggers() && $note === null && ($readBack === null || !$readBack->sameAs($schema))) {
                $result->notes[] = sprintf(
                    'V%04d: trigger %s: the stored trigger %s the declaration the step leaves; the snapshot follows the step',
                    $version,
                    $name,
                    $readBack === null ? 'is absent, unlike' : 'differs from',
                );
            }
        } elseif ($readBack !== null) {
            $schema = $readBack;
        } else {
            if (!$this->host->canReadTriggers()) {
                $result->notes[] = sprintf(
                    'V%04d: trigger %s: the step creates and drops nothing and %s keeps no readable trigger text; skipped',
                    $version,
                    $name,
                    $this->dialect->name(),
                );
            }

            return;
        }

        $this->writeSnapshot(
            $migrationsDir,
            'Trigger',
            self::shortName($step->triggerClass()),
            $version,
            SchemaSnapshot::exportTrigger($schema, $version, $generatedAt, $this->dialect->name()),
            $result,
        );
    }

    private function writeSnapshot(
        string $migrationsDir,
        string $kindFolder,
        string $typeName,
        int $version,
        string $content,
        ShadowResult $result,
    ): void {
        $directory = "{$migrationsDir}/{$kindFolder}/{$typeName}";
        if (!is_dir($directory)) {
            mkdir($directory, 0777, true);
        }

        $file = $directory . '/' . sprintf('V%04d.schema.json', $version);
        file_put_contents($file, $content . "\n");
        $result->writtenFiles[] = $file;
    }

    // --- trusted baseline (--from) -----------------------------------------------------

    private function restoreBaseline(PDO $connection, string $migrationsDir, int $fromVersion, ShadowResult $result): void
    {
        $tableRoot = "{$migrationsDir}/Table";
        if (is_dir($tableRoot)) {
            foreach (SnapshotSet::subdirectories($tableRoot) as $objectDir) {
                $latest = $this->latestTableSnapshotAtOrBelow($objectDir, $fromVersion);
                if ($latest === null) {
                    continue;   // table born after the trusted version
                }

                [$schema, $atVersion] = $latest;
                $this->exec($connection, $this->dialect->createTableSqlFromSchema($schema));
                foreach ($schema->indexes as $index) {
                    $this->exec($connection, $this->dialect->createIndexSqlFromSchema($schema->name, $index));
                }

                $result->notes[] = sprintf('baseline: %s restored from V%04d snapshot (trusted)', $schema->name, $atVersion);
            }
        } else {
            $result->notes[] = sprintf('--from V%04d: no committed snapshots under %s; starting empty', $fromVersion, $tableRoot);
        }

        // Views restore after tables — their neutral DDL snapshots render through the dialect.
        foreach (['View', 'MaterializedView'] as $kindFolder) {
            $kindRoot = "{$migrationsDir}/{$kindFolder}";
            if (!is_dir($kindRoot)) {
                continue;
            }

            foreach (SnapshotSet::subdirectories($kindRoot) as $objectDir) {
                $latest = $this->latestDdlSnapshotAtOrBelow($objectDir, $fromVersion);
                if ($latest === null) {
                    continue;
                }

                [$ddl, $objectName, $atVersion] = $latest;
                $parts = SchemaSnapshot::splitViewDdl($ddl);
                $this->exec($connection, $parts === null ? $ddl : $this->dialect->createViewSqlFromBody($objectName, $parts['body'], $parts['materialized']));
                $result->notes[] = sprintf('baseline: %s restored from V%04d snapshot (trusted)', $objectName, $atVersion);
            }
        }

        // Triggers restore last (ADR-0042): their relation must exist; the declaration renders through the dialect.
        $triggerRoot = "{$migrationsDir}/Trigger";
        if (is_dir($triggerRoot)) {
            foreach (SnapshotSet::subdirectories($triggerRoot) as $objectDir) {
                $latest = $this->latestTriggerSnapshotAtOrBelow($objectDir, $fromVersion);
                if ($latest === null) {
                    continue;
                }

                [$schema, $atVersion] = $latest;
                foreach ($schema->createStatements($this->dialect) as $sql) {
                    $this->exec($connection, $sql);
                }

                $result->notes[] = sprintf('baseline: %s restored from V%04d snapshot (trusted)', $schema->name(), $atVersion);
            }
        }
    }

    /** One restore statement of the trusted baseline: logged as `shadow` as it is sent, then executed. */
    private function exec(PDO $connection, string $sql): void
    {
        $this->log->write('shadow', $sql);
        $connection->exec($sql);
    }

    /** @return array{0: TriggerSchema, 1: int}|null */
    private function latestTriggerSnapshotAtOrBelow(string $dir, int $version): ?array
    {
        $best = null;
        foreach (glob("{$dir}/V*.schema.json") ?: [] as $file) {
            $content = file_get_contents($file);
            if ($content === false) {
                continue;
            }

            $parsed = SchemaSnapshot::parseTrigger($content);
            if ($parsed->asOfVersion <= $version && ($best === null || $parsed->asOfVersion > $best->asOfVersion)) {
                $best = $parsed;
            }
        }

        if ($best === null) {
            return null;
        }

        SchemaSnapshot::assertDialect($best->dialect, $this->dialect, $best->schema->name(), $best->asOfVersion);

        return [$best->schema, $best->asOfVersion];
    }

    /** @return array{0: TableSchema, 1: int}|null */
    private function latestTableSnapshotAtOrBelow(string $dir, int $version): ?array
    {
        $best = null;
        foreach (glob("{$dir}/V*.schema.json") ?: [] as $file) {
            $content = file_get_contents($file);
            if ($content === false) {
                continue;
            }

            $parsed = SchemaSnapshot::parse($content);
            if ($parsed->asOfVersion <= $version && ($best === null || $parsed->asOfVersion > $best->asOfVersion)) {
                $best = $parsed;
            }
        }

        if ($best === null) {
            return null;
        }

        SchemaSnapshot::assertDialect($best->dialect, $this->dialect, $best->schema->name, $best->asOfVersion);

        return [$best->schema, $best->asOfVersion];
    }

    /** @return array{0: string, 1: string, 2: int}|null [ddl, objectName, asOfVersion] */
    private function latestDdlSnapshotAtOrBelow(string $dir, int $version): ?array
    {
        $best = null;
        foreach (glob("{$dir}/V*.schema.json") ?: [] as $file) {
            $content = file_get_contents($file);
            if ($content === false) {
                continue;
            }

            $parsed = SchemaSnapshot::parseDdl($content);
            if ($parsed->asOfVersion <= $version && ($best === null || $parsed->asOfVersion > $best->asOfVersion)) {
                $best = $parsed;
            }
        }

        if ($best === null) {
            return null;
        }

        SchemaSnapshot::assertDialect($best->dialect, $this->dialect, $best->object, $best->asOfVersion);

        return [$best->ddl, $best->object, $best->asOfVersion];
    }

    // --- introspection -------------------------------------------------------------------

    /**
     * The live shape of a table through the dialect's two introspection queries, or null when the relation has no columns
     * (dropped). A column is a key when `pk` is positive and nullable when it is neither `notnull` nor a key; `generated`
     * is the fifth column (ADR-0043).
     */
    private function introspectTable(PDO $connection, string $relation): ?TableSchema
    {
        $columnsSql = $this->dialect->columnsInfoSql();
        $columnsStatement = $connection->prepare(SqlPlaceholders::toPdo($columnsSql));
        $this->log->write('shadow', $columnsSql, ['relation' => $relation]);
        PdoBinder::bindAndExecute($columnsStatement, ['relation' => $relation]);
        $columns = [];
        foreach ($columnsStatement->fetchAll(PDO::FETCH_NUM) as $row) {
            $isKey = ((int) $row[3]) > 0;
            $columns[] = new TableSchemaColumn(
                (string) $row[0],
                (string) $row[1],
                ((int) $row[2]) === 0 && !$isKey,
                $isKey,
                ((int) $row[4]) !== 0,
            );
        }

        if ($columns === []) {
            return null;
        }

        $indexesSql = $this->dialect->indexesInfoSql();
        $indexesStatement = $connection->prepare(SqlPlaceholders::toPdo($indexesSql));
        $this->log->write('shadow', $indexesSql, ['relation' => $relation]);
        PdoBinder::bindAndExecute($indexesStatement, ['relation' => $relation]);
        /** @var array<string, array{unique: bool, parts: list<TableSchemaIndexPart>}> $found */
        $found = [];
        foreach ($indexesStatement->fetchAll(PDO::FETCH_NUM) as $row) {
            $name = (string) $row[0];
            $found[$name] ??= ['unique' => ((int) $row[1]) !== 0, 'parts' => []];
            $found[$name]['parts'][] = new TableSchemaIndexPart((string) $row[3], ((int) $row[4]) !== 0);
        }

        $indexes = [];
        foreach ($found as $name => $index) {
            $indexes[] = new TableSchemaIndex($name, $index['parts'], $index['unique']);
        }

        return new TableSchema($relation, $columns, $indexes);
    }

    /**
     * A view's neutral DDL after a step: the step's last create statement, normalized; null when its last relevant
     * statement dropped the view. A step with neither falls back to the catalog where it stores the text verbatim, and is
     * skipped with a note where it does not. Where the catalog is verbatim it is read anyway and a disagreement noted.
     *
     * @param list<MigrationStatement> $statements the step's rendered up statements
     */
    private function viewDdl(PDO $connection, array $statements, string $relation, int $version, ShadowResult $result): ?string
    {
        $declared = null;
        $touched = false;
        foreach ($statements as $statement) {
            if ($statement->guardView !== null || $statement->guardTrigger !== null) {
                continue;
            }

            $sql = ltrim($statement->sql);
            if (preg_match('/^create\s+(?:or\s+(?:alter|replace)\s+)?(?:materialized\s+)?view\b/i', $sql) === 1) {
                $declared = SchemaSnapshot::normalizeDdl($sql);
                $touched = true;
            } elseif (preg_match('/^drop\s+(?:materialized\s+)?view\b/i', $sql) === 1) {
                $declared = null;
                $touched = true;
            }
        }

        $stored = $this->dialect->viewDefinitionIsVerbatim() ? $this->storedViewDdl($connection, $relation) : null;
        if ($touched) {
            if ($declared !== null && $this->dialect->viewDefinitionIsVerbatim() && $stored !== $declared) {
                $result->notes[] = sprintf(
                    'V%04d: view %s: the stored definition differs from the step\'s last create statement; the snapshot follows the step',
                    $version,
                    $relation,
                );
            }

            return $declared;
        }

        if ($this->dialect->viewDefinitionIsVerbatim()) {
            return $stored;
        }

        $result->notes[] = sprintf(
            'V%04d: view %s: the step creates and drops nothing and %s reformats stored views; skipped',
            $version,
            $relation,
            $this->dialect->name(),
        );

        return null;
    }

    /** The stored view text, normalized — null when absent. */
    private function storedViewDdl(PDO $connection, string $relation): ?string
    {
        $sql = $this->dialect->viewDefinitionSql();
        $statement = $connection->prepare(SqlPlaceholders::toPdo($sql));
        $this->log->write('shadow', $sql, ['relation' => $relation]);
        PdoBinder::bindAndExecute($statement, ['relation' => $relation]);
        $sql = $statement->fetchColumn();

        return $sql === false ? null : SchemaSnapshot::normalizeDdl((string) $sql);
    }

    private static function shortName(string $fqcn): string
    {
        $slash = strrpos($fqcn, '\\');

        return $slash === false ? $fqcn : substr($fqcn, $slash + 1);
    }
}
