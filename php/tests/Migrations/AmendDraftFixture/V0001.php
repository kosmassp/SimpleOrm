<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Migrations\AmendDraftFixture;

use SimpleOrm\Migrations\MigrationVersion;
use SimpleOrm\Migrations\VersionBuilder;
use SimpleOrm\Tests\Conformance\AmendFixture\Table\AmendWidget\V0001_Create;

/** The amend fixture's first version, reused as is: the draft case differs only in the marker on V0002. */
final class V0001 extends MigrationVersion
{
    public function compose(VersionBuilder $version): void
    {
        $version->apply(V0001_Create::class);
    }
}
