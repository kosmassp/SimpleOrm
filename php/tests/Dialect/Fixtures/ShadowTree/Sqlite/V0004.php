<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Dialect\Fixtures\ShadowTree\Sqlite;

use SimpleOrm\Migrations\MigrationVersion;
use SimpleOrm\Migrations\VersionBuilder;
use SimpleOrm\Tests\Dialect\Fixtures\ShadowTree\Sqlite\Table\ShadowGadget\V0004_AddNote;

final class V0004 extends MigrationVersion
{
    public function compose(VersionBuilder $version): void
    {
        $version->apply(V0004_AddNote::class);
    }
}
