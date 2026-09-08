<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Dialect\Fixtures\ShadowApp;

use SimpleOrm\Migrations\MigrationVersion;
use SimpleOrm\Migrations\VersionBuilder;
use SimpleOrm\Tests\Dialect\Fixtures\ShadowApp\Table\Widget\V0003_AddNote;

final class V0003 extends MigrationVersion
{
    public function compose(VersionBuilder $version): void
    {
        $version->apply(V0003_AddNote::class);
    }
}
