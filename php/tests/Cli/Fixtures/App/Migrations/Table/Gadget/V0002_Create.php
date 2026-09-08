<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Cli\Fixtures\App\Migrations\Table\Gadget;

use SimpleOrm\Migrations\TableActions;
use SimpleOrm\Migrations\TableMigration;
use SimpleOrm\Tests\Cli\Fixtures\App\Models\Gadget;

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
