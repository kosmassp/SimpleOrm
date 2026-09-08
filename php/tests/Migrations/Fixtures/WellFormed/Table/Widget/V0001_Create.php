<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Migrations\Fixtures\WellFormed\Table\Widget;

use SimpleOrm\Dialect\Dialect;
use SimpleOrm\Metadata\EntityMapLoader;
use SimpleOrm\Migrations\DownPlan;
use SimpleOrm\Migrations\MigrationStatement;
use SimpleOrm\Migrations\MigrationStep;

/**
 * A raw `MigrationStep` fixture for `MigrationSetTest::from_directory_discovers_and_orders_versions()`.
 * Ignores `$maps` deliberately: `EntityMapLoader` is a parallel, not-yet-landed
 * area, and this fixture only exercises discovery/validation, not real
 * `TableMigration` rendering.
 */
final class V0001_Create extends MigrationStep
{
    public function objectName(EntityMapLoader $maps): string
    {
        return 'widgets';
    }

    public function renderUp(EntityMapLoader $maps, Dialect $dialect): array
    {
        return [new MigrationStatement('create table widgets (id INTEGER PRIMARY KEY) STRICT', 'create widgets')];
    }

    public function renderDown(EntityMapLoader $maps, Dialect $dialect): DownPlan
    {
        return new DownPlan([], [], []);
    }
}
