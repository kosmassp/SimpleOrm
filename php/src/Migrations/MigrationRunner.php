<?php

declare(strict_types=1);

namespace SimpleOrm\Migrations;

use DateTimeImmutable;
use DateTimeZone;
use PDO;
use PDOException;
use SimpleOrm\Dialect\Dialect;
use SimpleOrm\Errors\SimpleOrmException;
use SimpleOrm\Metadata\EntityMapLoader;
use SimpleOrm\Parameters\PdoBinder;
use SimpleOrm\Parameters\SqlPlaceholders;
use SimpleOrm\Types\Iso8601;
use Throwable;

/**
 * Applies versioned code migrations (ADR-0013), mirroring `MigrationRunner.cs`.
 * The whole run executes inside the dialect's run lock — on SQLite one
 * `BEGIN IMMEDIATE` transaction, committed or rolled back as a whole, so a
 * failed run is fully atomic. Every plan is validated (checksums, unknown
 * history) before any statement executes. The caller (a future CLI, built on a
 * `Db`) never calls this at startup; migrating is always an explicit act
 * (§7.24). This class takes its collaborators directly — a `PDO` connection, a
 * `Dialect`, an `EntityMapLoader`, and an already-validated `MigrationSet` —
 * so it has no dependency on the parallel `Db`/session area.
 */
final class MigrationRunner
{
    private readonly SnapshotSet $snapshots;

    /**
     * @param bool $replayDrafts `@internal` — set only by the tooling (`SchemaShadow`): a throwaway database is no one's
     *        history, so `shadow` replays a draft; the application has no way to bypass `MIG-013` (ADR-0038)
     */
    public function __construct(
        private readonly PDO $connection,
        private readonly Dialect $dialect,
        private readonly EntityMapLoader $maps,
        private readonly MigrationSet $set,
        ?SnapshotSet $snapshots = null,
        private readonly bool $replayDrafts = false,
    ) {
        $this->snapshots = $snapshots ?? new SnapshotSet();
    }

    /**
     * Applies pending versions in order; returns how many were applied. A view
     * step's `expectDefinition` guard normally refuses on a live definition
     * changed outside the code (`MIG-012`); with `$allowViewDrift` the drift is
     * reported through `$notify` and the view is recreated anyway.
     *
     * @param ?callable(string): void $notify
     */
    public function migrate(bool $allowViewDrift = false, ?callable $notify = null): int
    {
        $plan = $this->renderAll();
        $this->dialect->beginMigrationRunLock($this->connection);
        try {
            $this->execute($this->dialect->versionTableSql());
            $recorded = $this->readRecorded();
            $this->validateHistory($plan, $recorded);
            $this->refusePendingDrafts($plan, $recorded, null);

            $applied = 0;
            foreach ($plan as $version) {
                if (in_array($version->version, $recorded['versions'], true)) {
                    continue;
                }

                foreach ($version->steps as $step) {
                    $start = microtime(true);
                    foreach ($step->up as $statement) {
                        $this->applyStatement($version->version, $statement, $allowViewDrift, $notify);
                    }

                    $this->record($version->version, $step, (int) round((microtime(true) - $start) * 1000));
                }

                $applied++;
            }

            $this->connection->exec('COMMIT');

            return $applied;
        } catch (Throwable $exception) {
            $this->connection->exec('ROLLBACK');

            throw $exception;
        }
    }

    /**
     * Reverts versions above `$targetVersion`, newest first. A step's rollback
     * is the hand-written `down()` override when present; otherwise it derives
     * from the versioned snapshots (ADR-0018). Refuses when a step has neither
     * and no snapshot supports it (`MIG-020`) — resolved for every reverting
     * step before any statement runs.
     *
     * @param ?callable(string): void $notify
     */
    public function migrateDown(int $targetVersion, bool $allowViewDrift = false, ?callable $notify = null): int
    {
        $plan = $this->renderAll();
        $this->dialect->beginMigrationRunLock($this->connection);
        try {
            $this->execute($this->dialect->versionTableSql());
            $recorded = $this->readRecorded();
            $this->validateHistory($plan, $recorded);

            $reverting = array_values(array_filter(
                $plan,
                static fn (RenderedVersion $v): bool => $v->version > $targetVersion
                    && in_array($v->version, $recorded['versions'], true),
            ));
            usort($reverting, static fn (RenderedVersion $a, RenderedVersion $b): int => $b->version <=> $a->version);

            $notices = [];
            /** @var array<int, array<string, list<MigrationStatement>>> $derivedCores version => object => statements */
            $derivedCores = [];
            foreach ($reverting as $version) {
                foreach ($version->steps as $step) {
                    if ($step->up === [] || $step->down->core !== []) {
                        continue;
                    }

                    $derived = DownDeriver::derive(
                        $step->objectName,
                        $step->version,
                        $this->snapshots,
                        $step->upRenames,
                        $notices,
                        $this->dialect,
                    );
                    if ($derived === null) {
                        throw new SimpleOrmException(
                            'MIG-020',
                            sprintf('V%04d %s', $step->version, $step->objectName),
                            'no snapshot to derive the rollback from (run simpleorm snapshot/shadow and lay the '
                                . '.schema.json files beside the migrations), or override down()',
                        );
                    }

                    $derivedCores[$version->version][$step->objectName] = $derived;
                }
            }

            foreach ($notices as $notice) {
                if ($notify !== null) {
                    $notify($notice);
                }
            }

            foreach ($reverting as $version) {
                foreach (array_reverse($version->steps) as $step) {
                    $core = $step->down->core !== []
                        ? $step->down->core
                        : ($derivedCores[$version->version][$step->objectName] ?? []);
                    foreach ([...$step->down->pre, ...$core, ...$step->down->post] as $statement) {
                        $this->applyStatement($version->version, $statement, $allowViewDrift, $notify);
                    }
                }

                $this->execute('delete from schema_version where version = ' . $version->version);
            }

            $this->connection->exec('COMMIT');

            return count($reverting);
        } catch (Throwable $exception) {
            $this->connection->exec('ROLLBACK');

            throw $exception;
        }
    }

    /** Records versions ≤ `$version` as applied without running them (§7.23). */
    public function baseline(int $version): void
    {
        $plan = $this->renderAll();
        $this->dialect->beginMigrationRunLock($this->connection);
        try {
            $this->execute($this->dialect->versionTableSql());
            $recorded = $this->readRecorded();
            $this->refusePendingDrafts($plan, $recorded, $version);

            foreach ($plan as $entry) {
                if ($entry->version > $version || in_array($entry->version, $recorded['versions'], true)) {
                    continue;
                }

                foreach ($entry->steps as $step) {
                    $this->record($entry->version, $step, 0);
                }
            }

            $this->connection->exec('COMMIT');
        } catch (Throwable $exception) {
            $this->connection->exec('ROLLBACK');

            throw $exception;
        }
    }

    /** @return list<MigrationEntry> */
    public function status(): array
    {
        $plan = $this->renderAll();
        $recorded = $this->readRecorded();

        $entries = [];
        foreach ($plan as $version) {
            foreach ($version->steps as $step) {
                $row = $recorded['rows'][$version->version][$step->objectName] ?? null;
                $state = match (true) {
                    $row !== null && $row['checksum'] === $step->checksum => MigrationState::Applied,
                    $row !== null => MigrationState::Drifted,
                    in_array($version->version, $recorded['versions'], true) => MigrationState::Drifted,
                    $version->draft => MigrationState::Draft,
                    default => MigrationState::Pending,
                };
                $entries[] = new MigrationEntry($version->version, $step->objectName, $step->description, $state);
            }
        }

        foreach ($recorded['rows'] as $recordedVersion => $objects) {
            $known = false;
            foreach ($plan as $version) {
                if ($version->version === $recordedVersion) {
                    $known = true;

                    break;
                }
            }

            if ($known) {
                continue;
            }

            foreach ($objects as $objectName => $row) {
                $entries[] = new MigrationEntry($recordedVersion, $objectName, $row['description'], MigrationState::Unknown);
            }
        }

        usort($entries, static fn (MigrationEntry $a, MigrationEntry $b): int => $a->version <=> $b->version
            ?: strcmp($a->objectName, $b->objectName));

        return $entries;
    }

    /**
     * True when any version is unapplied — the `MIG-030` check SchemaGuard runs. A pending draft is pending work too
     * (spec/migrations.md Clarifications, ADR-0038).
     */
    public function hasPending(): bool
    {
        foreach ($this->status() as $entry) {
            if ($entry->state === MigrationState::Pending || $entry->state === MigrationState::Draft) {
                return true;
            }
        }

        return false;
    }

    // --- rendering ------------------------------------------------------------------

    /** @return list<RenderedVersion> */
    private function renderAll(): array
    {
        return $this->set->render($this->maps, $this->dialect);
    }

    /**
     * A pending version marked a draft (`diff --draft`, Level 3 M5) refuses the whole run — `MIG-013`, before any
     * statement executes — until the author has reviewed it and removed the marker. `$upTo` limits the check to the
     * versions a baseline would record. A draft already recorded (applied somewhere) is history, not pending work, and
     * baseline refuses only drafts at or below its target; the refusal names the version (spec/migrations.md Clarifications,
     * ADR-0038).
     *
     * @param list<RenderedVersion> $plan
     * @param array{versions: list<int>, rows: array<int, array<string, array{description: string, checksum: string}>>} $recorded
     */
    private function refusePendingDrafts(array $plan, array $recorded, ?int $upTo): void
    {
        if ($this->replayDrafts) {
            return;   // `shadow` replays a draft like any version: tooling only, no application-side bypass (ADR-0038)
        }

        foreach ($plan as $version) {
            if (!$version->draft || in_array($version->version, $recorded['versions'], true)
                || ($upTo !== null && $version->version > $upTo)) {
                continue;
            }

            throw new SimpleOrmException(
                'MIG-013',
                sprintf('V%04d', $version->version),
                'is marked #[Draft] (diff --draft): review the generated version and remove the marker before it can apply or baseline',
            );
        }
    }

    /**
     * @param list<RenderedVersion> $plan
     * @param array{versions: list<int>, rows: array<int, array<string, array{description: string, checksum: string}>>} $recorded
     */
    private function validateHistory(array $plan, array $recorded): void
    {
        foreach ($plan as $version) {
            if (!in_array($version->version, $recorded['versions'], true)) {
                continue;
            }

            foreach ($version->steps as $step) {
                $row = $recorded['rows'][$version->version][$step->objectName] ?? null;
                if ($row === null) {
                    throw new SimpleOrmException(
                        'MIG-010',
                        sprintf('V%04d %s', $version->version, $step->objectName),
                        'the applied version has no record for this object; history and code disagree',
                    );
                }

                if ($row['checksum'] !== $step->checksum) {
                    throw new SimpleOrmException(
                        'MIG-010',
                        sprintf('V%04d %s', $version->version, $step->objectName),
                        'checksum changed since it was applied; applied migrations must not change',
                    );
                }
            }
        }

        foreach ($recorded['rows'] as $recordedVersion => $objects) {
            $known = false;
            foreach ($plan as $version) {
                if ($version->version === $recordedVersion) {
                    $known = true;

                    break;
                }
            }

            if ($known) {
                continue;
            }

            $anyObject = array_key_first($objects);
            throw new SimpleOrmException(
                'MIG-011',
                sprintf('V%04d %s', $recordedVersion, $anyObject),
                'applied in the database but unknown to the code',
            );
        }
    }

    // --- statement execution ---------------------------------------------------------

    /** Executes one rendered statement — or, for a guard, checks the view's or trigger's live definition instead. */
    private function applyStatement(int $version, MigrationStatement $statement, bool $allowViewDrift, ?callable $notify): void
    {
        if ($statement->guardTrigger !== null) {
            $this->checkTriggerGuard($version, $statement, $allowViewDrift, $notify);

            return;
        }

        if ($statement->guardView === null) {
            $this->execute($statement->sql);

            return;
        }

        $live = $this->readViewDefinition($statement->guardView);
        if ($live !== null && $this->liveViewMatches($live, $statement->sql)) {
            return;
        }

        $drift = $live === null
            ? sprintf('V%04d %s: the view is absent; expected the previous definition', $version, $statement->guardView)
            : sprintf(
                'V%04d %s: the live definition does not match the expected one — it was changed outside migrations',
                $version,
                $statement->guardView,
            );

        if (!$allowViewDrift) {
            throw new SimpleOrmException(
                'MIG-012',
                sprintf('V%04d %s', $version, $statement->guardView),
                ($live === null
                    ? 'the view is absent but a previous definition was expected'
                    : 'the live definition was changed outside migrations')
                    . '; review the drift, then rerun with --force to recreate it from the code',
            );
        }

        if ($notify !== null) {
            $notify($drift . '; recreating (--force)');
        }
    }

    /**
     * The trigger guard (`MIG-012`, ADR-0042): the live definitions — the dialect's `triggerDefinitionSql()` rows, one per
     * statement it created — normalized, must be the same set as the dialect's rendering of the expected declaration.
     * Absent or different refuses (the run is one transaction, so nothing applies and the outside hotfix is kept);
     * `--force` reports the drift and lets the recreate proceed.
     */
    private function checkTriggerGuard(int $version, MigrationStatement $statement, bool $allowDrift, ?callable $notify): void
    {
        $trigger = (string) $statement->guardTrigger;
        $live = $this->readTriggerDefinitions($trigger);
        $expected = $statement->guardExpected;
        sort($expected, SORT_STRING);
        if ($live === $expected) {
            return;
        }

        $absent = $live === [];
        if (!$allowDrift) {
            throw new SimpleOrmException(
                'MIG-012',
                sprintf('V%04d %s', $version, $trigger),
                ($absent
                    ? 'the trigger is absent but a previous declaration was expected'
                    : 'the live trigger was changed outside migrations')
                    . '; review the drift, then rerun with --force to recreate it from the code',
            );
        }

        if ($notify !== null) {
            $notify(sprintf(
                $absent
                    ? 'V%04d %s: the trigger is absent; expected the previous declaration; recreating (--force)'
                    : 'V%04d %s: the live trigger does not match the expected declaration — it was changed outside migrations; recreating (--force)',
                $version,
                $trigger,
            ));
        }
    }

    /**
     * The live trigger statements of a name, normalized and sorted (the guard compares sets).
     *
     * @return list<string>
     */
    private function readTriggerDefinitions(string $trigger): array
    {
        $statement = $this->connection->prepare(SqlPlaceholders::toPdo($this->dialect->triggerDefinitionSql()));
        PdoBinder::bindAndExecute($statement, ['relation' => $trigger]);
        $rows = array_map(
            static fn (mixed $definition): string => SchemaSnapshot::normalizeTriggerDdl((string) $definition),
            $statement->fetchAll(PDO::FETCH_COLUMN),
        );
        sort($rows, SORT_STRING);

        return $rows;
    }

    private function readViewDefinition(string $view): ?string
    {
        $statement = $this->connection->prepare(SqlPlaceholders::toPdo($this->dialect->viewDefinitionSql()));
        PdoBinder::bindAndExecute($statement, ['relation' => $view]);
        $value = $statement->fetchColumn();

        return $value === false ? null : (string) $value;
    }

    /**
     * Whether a live view definition is the expected one (the `MIG-012` guard, ADR-0043). Where the engine stores the text
     * as submitted (`viewDefinitionIsVerbatim`: SQLite, SQL Server) both sides normalize and compare as text. Where it
     * reformats the parse tree (PostgreSQL's `pg_get_viewdef`) the expected body is created as a **probe view** inside the
     * run, read back the same way, and dropped, and the engine's two renderings are compared — equal exactly when the parse
     * trees are. The probe runs under a savepoint, so a body the current schema cannot parse counts as drift instead of
     * poisoning the transaction.
     */
    private function liveViewMatches(string $live, string $expectedDdl): bool
    {
        $parts = SchemaSnapshot::splitViewDdl($expectedDdl);
        if ($this->dialect->viewDefinitionIsVerbatim() || $parts === null) {
            return SchemaSnapshot::normalizeDdl($live) === $expectedDdl;
        }

        $probe = 'simpleorm_expect_' . bin2hex(random_bytes(6));
        $this->connection->exec('savepoint simpleorm_probe');
        try {
            $this->connection->exec($this->dialect->createViewSqlFromBody($probe, $parts['body'], $parts['materialized']));
            $expected = $this->readViewDefinition($probe);
            $this->connection->exec($this->dialect->dropViewSql($probe, $parts['materialized']));
            $this->connection->exec('release savepoint simpleorm_probe');
        } catch (PDOException) {
            $this->connection->exec('rollback to savepoint simpleorm_probe');
            $this->connection->exec('release savepoint simpleorm_probe');

            return false;   // the expected body does not even parse against the current schema: drift
        }

        return $expected !== null && trim($expected) === trim($live);
    }

    private function execute(string $sql): void
    {
        try {
            $this->connection->exec($sql);
        } catch (PDOException $exception) {
            throw new SimpleOrmException('MIG-021', 'migration statement', $exception->getMessage() . ' -- while executing: ' . $sql);
        }
    }

    private function record(int $version, RenderedStep $step, int $executionMs): void
    {
        $sql = SqlPlaceholders::toPdo(
            'insert into schema_version (version, object, description, checksum, applied_at, execution_ms) '
                . 'values (@version, @object, @description, @checksum, @applied_at, @execution_ms)',
        );
        $statement = $this->connection->prepare($sql);
        PdoBinder::bindAndExecute($statement, [
            'version' => $version,
            'object' => $step->objectName,
            'description' => $step->description,
            'checksum' => $step->checksum,
            'applied_at' => self::isoUtcNow(),
            'execution_ms' => $executionMs,
        ]);
    }

    /**
     * Reads every recorded `(version, object)` row. A missing `schema_version`
     * table (no migration has ever run) means "nothing recorded", not an error.
     *
     * @return array{versions: list<int>, rows: array<int, array<string, array{description: string, checksum: string}>>}
     */
    private function readRecorded(): array
    {
        try {
            $statement = $this->connection->query('select version, object, description, checksum from schema_version');
        } catch (PDOException) {
            return ['versions' => [], 'rows' => []];
        }

        $rows = [];
        foreach ($statement as $row) {
            $version = (int) $row['version'];
            $rows[$version][(string) $row['object']] = [
                'description' => (string) $row['description'],
                'checksum' => (string) $row['checksum'],
            ];
        }

        return ['versions' => array_keys($rows), 'rows' => $rows];
    }

    private static function isoUtcNow(): string
    {
        return Iso8601::format(new DateTimeImmutable('now', new DateTimeZone('UTC')));
    }
}
