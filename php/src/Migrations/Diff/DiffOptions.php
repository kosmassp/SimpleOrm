<?php

declare(strict_types=1);

namespace SimpleOrm\Migrations\Diff;

use Closure;
use SimpleOrm\Dialect\Dialect;

/**
 * Inputs of `simpleorm diff` (ADR-0017), the amend mode included (add.3).
 * Mirrors C#'s `DiffCommand.cs` `DiffOptions`, adapted for PHP's lack of
 * assemblies (CODING-STANDARD §10): where C# scans one assembly for both the
 * migration classes and the file layout, PHP needs two directories, because
 * Composer's PSR-4 autoloader maps a namespace to a fixed, real directory that
 * cannot be redirected at runtime — `$migrationsDir` is where the *compiled*
 * `MigrationVersion`/`MigrationStep` classes physically live (ordinary CLI use
 * points it at the same place as `$outDir`; the amend conformance suite gives a
 * separate, fixed fixture directory since its per-case files live in an
 * ephemeral temp directory the autoloader knows nothing about).
 */
final readonly class DiffOptions
{
    /**
     * @param string $migrationsDir PSR-4-reachable directory holding the compiled `MigrationVersion`/`MigrationStep`
     *        classes under `$rootNamespace` (`MigrationSet::fromDirectory()` reads it) — the newest version, and for
     *        `--amend` the compiled step count for the target version, come from here.
     * @param string $rootNamespace the migrations root namespace: the discovery filter and the namespace of emitted code
     * @param list<class-string> $entityTypes the entities to diff — every mapped type, for the CLI
     * @param string $outDir the migrations directory: snapshots are read from it, sources are written into it
     * @param string $dialectLabel the CLI's `--dialect` name, stamped into the generated root
     * @param array<string, array<string, string>> $renames declared old-column => new-column pairs, per relation name
     * @param Closure(int): bool|null $isApplied amend only, when a database was given: whether it has the version
     *        recorded as applied (only ever warns — other databases exist the tool can't see)
     */
    public function __construct(
        public string $migrationsDir,
        public string $rootNamespace,
        public array $entityTypes,
        public string $outDir,
        public Dialect $dialect,
        public string $dialectLabel,
        public ?string $name = null,
        public array $renames = [],
        public bool $allowRemove = false,
        public bool $amend = false,
        public bool $force = false,
        public ?Closure $isApplied = null,
    ) {
    }
}
