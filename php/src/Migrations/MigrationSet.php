<?php

declare(strict_types=1);

namespace SimpleOrm\Migrations;

use ReflectionClass;
use SimpleOrm\Dialect\Dialect;
use SimpleOrm\Discovery\ClassScanner;
use SimpleOrm\Errors\SimpleOrmException;
use SimpleOrm\Metadata\EntityMapLoader;

/**
 * A validated, ordered set of migration versions (ADR-0013) — the PHP
 * counterpart of `MigrationRunner`'s discovery and validation (the runner
 * itself, which executes SQL, is a later phase built on this class).
 *
 * Structural validation runs eagerly, at construction (`of()`/`fromDirectory()`):
 * malformed names (`MIG-001`, via `version()`/`description()` parsing), two
 * roots declaring the same version or a step whose version disagrees with its
 * composing root (`MIG-002` root half, `MIG-003`), and a discovered step no
 * root composes (`MIG-004`). The one check that needs entity resolution — a
 * version composing the **same object** twice (`MIG-002` object half) — runs in
 * `render()`, the only place a caller supplies an `EntityMapLoader`.
 */
final class MigrationSet
{
    /** @var list<MigrationVersion> */
    private readonly array $versions;

    /**
     * @param list<MigrationVersion> $versions
     * @param list<MigrationStep> $strayCandidates every step class discovered alongside the versions (empty when built from `of()`)
     */
    private function __construct(array $versions, array $strayCandidates)
    {
        $this->versions = self::sortByVersion($versions);
        self::validateStructure($this->versions, $strayCandidates);
    }

    /** Builds a set from explicit versions; no directory to discover orphan steps from, so `MIG-004` never fires here. */
    public static function of(MigrationVersion ...$versions): self
    {
        return new self($versions, []);
    }

    /**
     * Discovers `MigrationVersion`/`MigrationStep` subclasses under `$dir` via
     * {@see ClassScanner} — every autoloadable class under `$namespace` (PSR-4)
     * — and instantiates the ones this area recognizes; never `require`d
     * directly (CODING-STANDARD §1).
     */
    public static function fromDirectory(string $dir, string $namespace): self
    {
        $versions = [];
        $steps = [];
        foreach (ClassScanner::classes($dir, $namespace) as $class) {
            $reflection = new ReflectionClass($class);
            if ($reflection->isAbstract()) {
                continue;
            }

            $constructor = $reflection->getConstructor();
            if ($constructor !== null && $constructor->getNumberOfRequiredParameters() > 0) {
                continue;
            }

            if ($reflection->isSubclassOf(MigrationVersion::class)) {
                /** @var MigrationVersion $instance */
                $instance = $reflection->newInstance();
                $versions[] = $instance;
            } elseif ($reflection->isSubclassOf(MigrationStep::class)) {
                /** @var MigrationStep $instance */
                $instance = $reflection->newInstance();
                $steps[] = $instance;
            }
        }

        return new self($versions, $steps);
    }

    /** @return list<MigrationVersion> ordered by version */
    public function versions(): array
    {
        return $this->versions;
    }

    /**
     * Renders every version's steps against the current metadata and dialect —
     * the plan the runner phase applies. Completes the `MIG-002` check that
     * needs entity resolution (a version composing the same object twice).
     *
     * @return list<RenderedVersion>
     */
    public function render(EntityMapLoader $maps, Dialect $dialect): array
    {
        $rendered = [];
        foreach ($this->versions as $version) {
            $builder = new VersionBuilder();
            $version->compose($builder);

            $seenObjects = [];
            $steps = [];
            foreach ($builder->steps() as $step) {
                $objectName = $step->objectName($maps);
                if (isset($seenObjects[$objectName])) {
                    throw new SimpleOrmException(
                        'MIG-002',
                        sprintf('V%04d %s', $version->version(), $objectName),
                        'composed twice in one version',
                    );
                }

                $seenObjects[$objectName] = true;
                $steps[] = new RenderedStep(
                    $version->version(),
                    $objectName,
                    $step->description(),
                    $step->renderUp($maps, $dialect),
                    $step->renderDown($maps, $dialect),
                    $step->upRenames($maps, $dialect),
                );
            }

            $rendered[] = new RenderedVersion($version->version(), $steps);
        }

        return $rendered;
    }

    /**
     * @param list<MigrationVersion> $versions
     * @param list<MigrationStep> $strayCandidates
     */
    private static function validateStructure(array $versions, array $strayCandidates): void
    {
        $seenVersions = [];
        foreach ($versions as $version) {
            $number = $version->version();
            if (isset($seenVersions[$number])) {
                throw new SimpleOrmException(
                    'MIG-002',
                    sprintf('V%04d', $number),
                    'more than one root migration declares this version',
                );
            }

            $seenVersions[$number] = true;
        }

        $composedTypes = [];
        foreach ($versions as $version) {
            $builder = new VersionBuilder();
            $version->compose($builder);
            foreach ($builder->steps() as $step) {
                $composedTypes[$step::class] = true;
                if ($step->version() !== $version->version()) {
                    throw new SimpleOrmException(
                        'MIG-003',
                        $step::class,
                        sprintf('declares version %d but is composed by V%04d', $step->version(), $version->version()),
                    );
                }
            }
        }

        foreach ($strayCandidates as $stray) {
            if (!isset($composedTypes[$stray::class])) {
                throw new SimpleOrmException('MIG-004', $stray::class, 'exists but no root version composes it');
            }
        }
    }

    /** @param list<MigrationVersion> $versions @return list<MigrationVersion> */
    private static function sortByVersion(array $versions): array
    {
        $sorted = $versions;
        usort($sorted, static fn (MigrationVersion $a, MigrationVersion $b): int => $a->version() <=> $b->version());

        return $sorted;
    }
}
