<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Dialect\Fixtures\ShadowTree\Sqlite\Table\ShadowGadget;

use SimpleOrm\Migrations\TableActions;
use SimpleOrm\Migrations\TableMigration;
use SimpleOrm\Tests\Dialect\Fixtures\ShadowTree\Models\ShadowGadget;

/** SQLite's literal create (frozen: V0004 adds `note`): a STRICT table and a unique index. */
final class V0001_Create extends TableMigration
{
    public function entityClass(): string
    {
        return ShadowGadget::class;
    }

    public function action(TableActions $actions): void
    {
        $actions->sql('create table shadow_gadgets (id INTEGER PRIMARY KEY, name TEXT NOT NULL, code TEXT NOT NULL) STRICT');
        $actions->sql('create unique index ix_shadow_gadgets_code on shadow_gadgets (code)');
    }
}
