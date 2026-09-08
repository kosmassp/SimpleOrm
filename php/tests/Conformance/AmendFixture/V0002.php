<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Conformance\AmendFixture;

use SimpleOrm\Migrations\MigrationVersion;
use SimpleOrm\Migrations\VersionBuilder;
use SimpleOrm\Tests\Conformance\AmendFixture\Table\AmendWidget\V0002_AddNote;

final class V0002 extends MigrationVersion
{
    public function compose(VersionBuilder $version): void
    {
        $version->apply(V0002_AddNote::class);
    }
}
