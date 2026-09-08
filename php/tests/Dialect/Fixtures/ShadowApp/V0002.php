<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Dialect\Fixtures\ShadowApp;

use SimpleOrm\Migrations\MigrationVersion;
use SimpleOrm\Migrations\VersionBuilder;
use SimpleOrm\Tests\Dialect\Fixtures\ShadowApp\Table\Gadget\V0002_Create;

final class V0002 extends MigrationVersion
{
    public function compose(VersionBuilder $version): void
    {
        $version->apply(V0002_Create::class);
    }
}
