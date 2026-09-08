<?php

declare(strict_types=1);

namespace SimpleOrm\Sample\Migrations;

use SimpleOrm\Migrations\MigrationVersion;
use SimpleOrm\Migrations\VersionBuilder;
use SimpleOrm\Sample\Migrations\View\UserTransactionTotal\V0006_AddLastTransactionAt;

final class V0006 extends MigrationVersion
{
    public function compose(VersionBuilder $version): void
    {
        $version
            ->apply(V0006_AddLastTransactionAt::class);
    }
}
