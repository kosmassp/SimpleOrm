<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Conformance\AmendFixture\Table\AmendWidget;

use SimpleOrm\Migrations\TableActions;
use SimpleOrm\Migrations\TableMigration;
use SimpleOrm\Tests\Conformance\AmendFixture\AmendWidget;

final class V0001_Create extends TableMigration
{
    public function entityClass(): string
    {
        return AmendWidget::class;
    }

    public function action(TableActions $actions): void
    {
        $actions->sql('create table amend_widgets (id INTEGER PRIMARY KEY, name TEXT NOT NULL) STRICT');
    }
}
