<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Dialect\Fixtures\ShadowTree\Postgres\Table\ShadowGadget;

use SimpleOrm\Migrations\TableActions;
use SimpleOrm\Migrations\TableMigration;
use SimpleOrm\Tests\Dialect\Fixtures\ShadowTree\Models\ShadowGadget;

final class V0004_AddNote extends TableMigration
{
    public function entityClass(): string
    {
        return ShadowGadget::class;
    }

    public function action(TableActions $actions): void
    {
        $actions->addColumn('note', 'text');
    }
}
