<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Dialect\Fixtures\ShadowApp\Table\Gadget;

use SimpleOrm\Migrations\TableActions;
use SimpleOrm\Migrations\TableMigration;
use SimpleOrm\Tests\Dialect\Fixtures\ShadowApp\Models\Gadget;

/** Gadget never changes after this, so its create stays metadata-rendered. */
final class V0002_Create extends TableMigration
{
    public function entityClass(): string
    {
        return Gadget::class;
    }

    public function action(TableActions $actions): void
    {
        $actions->createTable();
    }
}
