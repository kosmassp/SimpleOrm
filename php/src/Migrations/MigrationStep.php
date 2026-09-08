<?php

declare(strict_types=1);

namespace SimpleOrm\Migrations;

use ReflectionClass;
use SimpleOrm\Dialect\Dialect;
use SimpleOrm\Errors\SimpleOrmException;
use SimpleOrm\Metadata\EntityMapLoader;

/**
 * One object's change within a version: a class named `V<version>_<Description>`
 * in the object's folder (`Table/User/…`). Its version must match the composing
 * root (`MIG-003`); a step no root composes is an error (`MIG-004`) — nothing is
 * silently skipped.
 */
abstract class MigrationStep
{
    private const string NAME_PATTERN = '/^V(\d+)_(\w+)$/';

    /** Parsed from the class's short name; overridable by data-driven steps (`SqlVersionRawStep`). */
    public function version(): int
    {
        return $this->parse()[0];
    }

    /** Parsed from the class's short name; overridable by data-driven steps (`SqlVersionRawStep`). */
    public function description(): string
    {
        return $this->parse()[1];
    }

    abstract public function objectName(EntityMapLoader $maps): string;

    /** @return list<MigrationStatement> */
    abstract public function renderUp(EntityMapLoader $maps, Dialect $dialect): array;

    abstract public function renderDown(EntityMapLoader $maps, Dialect $dialect): DownPlan;

    /**
     * The step's declared column renames, for the derived rollback (ADR-0018):
     * snapshots alone cannot distinguish a rename from a drop+add, but the
     * typed actions can — so the deriver inverts them data-preservingly.
     *
     * @return list<ColumnRename>
     */
    public function upRenames(EntityMapLoader $maps, Dialect $dialect): array
    {
        return [];
    }

    /** @return array{0: int, 1: string} */
    private function parse(): array
    {
        $shortName = (new ReflectionClass($this))->getShortName();
        if (preg_match(self::NAME_PATTERN, $shortName, $matches) !== 1) {
            throw new SimpleOrmException(
                'MIG-001',
                $shortName,
                'object migration class names are V<version>_<Description>, e.g. V0002_AddDisplayName',
            );
        }

        return [(int) $matches[1], $matches[2]];
    }
}
