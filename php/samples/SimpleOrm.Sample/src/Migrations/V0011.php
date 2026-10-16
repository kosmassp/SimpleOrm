<?php

declare(strict_types=1);

namespace SimpleOrm\Sample\Migrations;

use SimpleOrm\Migrations\MigrationVersion;
use SimpleOrm\Migrations\VersionBuilder;
use SimpleOrm\Sample\Migrations\Table\Role\V0011_AddIsDeleted;

/** The declared soft-delete hook (Level 3 M3): roles gain `is_deleted`. */
final class V0011 extends MigrationVersion
{
    public function compose(VersionBuilder $version): void
    {
        $version
            ->apply(V0011_AddIsDeleted::class);
    }
}
