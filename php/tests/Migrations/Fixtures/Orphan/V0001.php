<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Migrations\Fixtures\Orphan;

use SimpleOrm\Migrations\MigrationVersion;
use SimpleOrm\Migrations\VersionBuilder;

/** Composes nothing — the sibling `Table/Widget/V0001_Orphan.php` step is discovered but never applied. */
final class V0001 extends MigrationVersion
{
    public function compose(VersionBuilder $version): void
    {
    }
}
