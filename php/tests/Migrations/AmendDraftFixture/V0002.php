<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Migrations\AmendDraftFixture;

use SimpleOrm\Migrations\Draft;
use SimpleOrm\Migrations\MigrationVersion;
use SimpleOrm\Migrations\VersionBuilder;
use SimpleOrm\Tests\Conformance\AmendFixture\Table\AmendWidget\V0002_AddNote;

/** The amend fixture's second version, still carrying the draft marker `diff --draft` wrote (ADR-0038). */
#[Draft]
final class V0002 extends MigrationVersion
{
    public function compose(VersionBuilder $version): void
    {
        $version->apply(V0002_AddNote::class);
    }
}
