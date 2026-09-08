<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Migrations\Fixtures\Orphan\Table\Widget;

use SimpleOrm\Dialect\Dialect;
use SimpleOrm\Metadata\EntityMapLoader;
use SimpleOrm\Migrations\DownPlan;
use SimpleOrm\Migrations\MigrationStep;

/** Exists on disk but composed by no root — `MigrationSetTest::orphan_step_is_mig_004()`. */
final class V0001_Orphan extends MigrationStep
{
    public function objectName(EntityMapLoader $maps): string
    {
        return 'orphans';
    }

    public function renderUp(EntityMapLoader $maps, Dialect $dialect): array
    {
        return [];
    }

    public function renderDown(EntityMapLoader $maps, Dialect $dialect): DownPlan
    {
        return new DownPlan([], [], []);
    }
}
