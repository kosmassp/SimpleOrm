<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Dialect\Fixtures\ShadowTree;

use SimpleOrm\Migrations\MigrationVersion;
use SimpleOrm\Migrations\VersionBuilder;
use SimpleOrm\Tests\Dialect\Fixtures\ShadowTree\View\ShadowGadgetCodes\V0002_Create;

/** The view step, shared by every dialect (the view's SQL is dialect-neutral; the dialect renders the create). */
final class V0002 extends MigrationVersion
{
    public function compose(VersionBuilder $version): void
    {
        $version->apply(V0002_Create::class);
    }
}
