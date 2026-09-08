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

    public function __construct(
        private readonly PDO $connection,
        private readonly Dialect $dialect,
        private readonly EntityMapLoader $maps,
        private readonly MigrationSet $set,
        ?SnapshotSet $snapshots = null,
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

    /** True when any version is unapplied — the `MIG-030` check SchemaGuard runs at milestone 6. */
    public function hasPending(): bool
    {
        foreach ($this->status() as $entry) {
            if ($entry->state === MigrationState::Pending) {
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

    /** Executes one rendered statement — or, for a guard, checks the view's live definition instead. */
    private function applyStatement(int $version, MigrationStatement $statement, bool $allowViewDrift, ?callable $notify): void
    {
        if ($statement->guardView === null) {
            $this->execute($statement->sql);

            return;
        }

        $live = $this->readViewDefinition($statement->guardView);
        $normalized = $live === null ? null : SchemaSnapshot::normalizeDdl($live);
        if ($normalized === $statement->sql) {
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

    private function readViewDefinition(string $view): ?string
    {
        $statement = $this->connection->prepare(SqlPlaceholders::toPdo($this->dialect->viewDefinitionSql()));
        PdoBinder::bindAndExecute($statement, ['relation' => $view]);
        $value = $statement->fetchColumn();

        return $value === false ? null : (string) $value;
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
