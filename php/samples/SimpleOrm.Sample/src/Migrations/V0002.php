<?php

declare(strict_types=1);

namespace SimpleOrm\Sample\Migrations;

use SimpleOrm\Migrations\MigrationVersion;
use SimpleOrm\Migrations\VersionBuilder;
use SimpleOrm\Sample\Migrations\Table\User\V0002_AddDisplayName;

final class V0002 extends MigrationVersion
{
    public function compose(VersionBuilder $version): void
    {
        $version
            ->apply(V0002_AddDisplayName::class);
    }
}
