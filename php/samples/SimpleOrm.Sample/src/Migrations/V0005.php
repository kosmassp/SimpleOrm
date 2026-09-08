<?php

declare(strict_types=1);

namespace SimpleOrm\Sample\Migrations;

use SimpleOrm\Migrations\MigrationVersion;
use SimpleOrm\Migrations\VersionBuilder;
use SimpleOrm\Sample\Migrations\Table\Role\V0005_SeedUserRole;
use SimpleOrm\Sample\Migrations\Table\UserRole\V0005_AddGrantedBy;

/** A multi-object version: two steps, applied atomically in this order. */
final class V0005 extends MigrationVersion
{
    public function compose(VersionBuilder $version): void
    {
        $version
            ->apply(V0005_AddGrantedBy::class)
            ->apply(V0005_SeedUserRole::class);
    }
}
