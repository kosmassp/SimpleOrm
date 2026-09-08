<?php

declare(strict_types=1);

namespace SimpleOrm\Sample\Migrations;

use SimpleOrm\Migrations\MigrationVersion;
use SimpleOrm\Migrations\VersionBuilder;
use SimpleOrm\Sample\Migrations\Table\UserProfile\V0010_AddAddress;

/** The owned-type columns (ADR-0030): user_profiles gains address_* for the #[Owned] Address. */
final class V0010 extends MigrationVersion
{
    public function compose(VersionBuilder $version): void
    {
        $version
            ->apply(V0010_AddAddress::class);
    }
}
