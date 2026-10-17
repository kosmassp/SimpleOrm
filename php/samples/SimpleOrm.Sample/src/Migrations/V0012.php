<?php

declare(strict_types=1);

namespace SimpleOrm\Sample\Migrations;

use SimpleOrm\Migrations\MigrationVersion;
use SimpleOrm\Migrations\VersionBuilder;
use SimpleOrm\Sample\Migrations\Table\UserProfile\V0012_AddAvatar;

/** ADR-0040 add.1: the fixture's bytes column — user profiles gain `avatar`. */
final class V0012 extends MigrationVersion
{
    public function compose(VersionBuilder $version): void
    {
        $version
            ->apply(V0012_AddAvatar::class);
    }
}
