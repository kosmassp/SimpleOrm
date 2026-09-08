<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Migrations\Fixtures\WellFormed\Table\Widget;

use SimpleOrm\Dialect\Dialect;
use SimpleOrm\Metadata\EntityMapLoader;
use SimpleOrm\Migrations\DownPlan;
use SimpleOrm\Migrations\MigrationStatement;
use SimpleOrm\Migrations\MigrationStep;

/** See `V0001_Create` for why `$maps` is ignored here. */
final class V0002_AddNote extends MigrationStep
{
    public function objectName(EntityMapLoader $maps): string
    {
        return 'widgets';
    }

    public function renderUp(EntityMapLoader $maps, Dialect $dialect): array
    {
        return [new MigrationStatement('alter table widgets add column note TEXT', 'add widgets.note')];
    }

    public function renderDown(EntityMapLoader $maps, Dialect $dialect): DownPlan
    {
        return new DownPlan([], [], []);
    }
}
