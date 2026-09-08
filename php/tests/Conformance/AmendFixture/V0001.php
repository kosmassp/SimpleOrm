<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Conformance\AmendFixture;

use SimpleOrm\Migrations\MigrationVersion;
use SimpleOrm\Migrations\VersionBuilder;
use SimpleOrm\Tests\Conformance\AmendFixture\Table\AmendWidget\V0001_Create;

final class V0001 extends MigrationVersion
{
    public function compose(VersionBuilder $version): void
    {
        $version->apply(V0001_Create::class);
    }
}
