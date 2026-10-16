<?php

declare(strict_types=1);

namespace SimpleOrm\Migrations;

use ReflectionClass;
use SimpleOrm\Errors\SimpleOrmException;

/**
 * The recorded unit of schema change (ADR-0013): a root class named
 * `V<version>` (e.g. `V0001`), composing per-object migration steps in
 * explicit, reviewable order. One `schema_version` row is written per
 * (version, object); a version applies atomically. Migrations are code — never
 * external .sql files; apply is always explicit, never at startup.
 */
abstract class MigrationVersion
{
    private const string NAME_PATTERN = '/^V(\d+)$/';

    /** Parsed from the class's short name; overridable by data-driven versions (`SqlVersion`). */
    public function version(): int
    {
        $shortName = (new ReflectionClass($this))->getShortName();
        if (preg_match(self::NAME_PATTERN, $shortName, $matches) !== 1) {
            throw new SimpleOrmException(
                'MIG-001',
                $shortName,
                'root migration class names are V<version>, e.g. V0001',
            );
        }

        return (int) $matches[1];
    }

    /**
     * Whether the root carries the `#[Draft]` marker (Level 3 M5): a pending draft is never applied or baselined
     * (`MIG-013`). Data-driven versions (`SqlVersion`) override it with their own flag.
     */
    public function isDraft(): bool
    {
        return (new ReflectionClass($this))->getAttributes(Draft::class) !== [];
    }

    /** Applies this version's object steps in order (FK order across tables is the author's/generator's job). */
    abstract public function compose(VersionBuilder $version): void;
}
