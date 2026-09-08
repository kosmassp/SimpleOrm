<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Dialect\Fixtures\ShadowApp\Table\Widget;

use SimpleOrm\Migrations\TableActions;
use SimpleOrm\Migrations\TableMigration;
use SimpleOrm\Tests\Dialect\Fixtures\ShadowApp\Models\Widget;

final class V0003_AddNote extends TableMigration
{
    public function entityClass(): string
    {
        return Widget::class;
    }

    public function action(TableActions $actions): void
    {
        $actions->addColumn('note', 'TEXT');
    }
}
