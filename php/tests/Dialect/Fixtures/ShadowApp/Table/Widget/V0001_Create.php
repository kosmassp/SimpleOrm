<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Dialect\Fixtures\ShadowApp\Table\Widget;

use SimpleOrm\Migrations\TableActions;
use SimpleOrm\Migrations\TableMigration;
use SimpleOrm\Tests\Dialect\Fixtures\ShadowApp\Models\Widget;

/** Frozen to literal SQL: `V0003_AddNote` adds `note`, so this must stay the V1 shape (§7.22). */
final class V0001_Create extends TableMigration
{
    public function entityClass(): string
    {
        return Widget::class;
    }

    public function action(TableActions $actions): void
    {
        $actions->sql('create table widgets (id INTEGER PRIMARY KEY, name TEXT NOT NULL) STRICT');
    }
}
