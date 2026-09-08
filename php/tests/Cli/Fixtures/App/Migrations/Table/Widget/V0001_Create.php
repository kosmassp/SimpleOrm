<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Cli\Fixtures\App\Migrations\Table\Widget;

use SimpleOrm\Migrations\TableActions;
use SimpleOrm\Migrations\TableMigration;
use SimpleOrm\Tests\Cli\Fixtures\App\Models\Widget;

final class V0001_Create extends TableMigration
{
    public function entityClass(): string
    {
        return Widget::class;
    }

    public function action(TableActions $actions): void
    {
        $actions->createTable();
    }
}
