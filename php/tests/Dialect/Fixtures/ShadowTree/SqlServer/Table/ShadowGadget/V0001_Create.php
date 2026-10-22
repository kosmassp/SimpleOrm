<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Dialect\Fixtures\ShadowTree\SqlServer\Table\ShadowGadget;

use SimpleOrm\Migrations\TableActions;
use SimpleOrm\Migrations\TableMigration;
use SimpleOrm\Tests\Dialect\Fixtures\ShadowTree\Models\ShadowGadget;

/** SQL Server's literal create (frozen: V0004 adds `note`): an identity key, nvarchar columns (the indexed one at the indexable length), and a unique index. */
final class V0001_Create extends TableMigration
{
    public function entityClass(): string
    {
        return ShadowGadget::class;
    }

    public function action(TableActions $actions): void
    {
        $actions->sql('create table [shadow_gadgets] ([id] bigint identity(1,1) primary key, [name] nvarchar(max) not null, [code] nvarchar(450) not null)');
        $actions->sql('create unique index [ix_shadow_gadgets_code] on [shadow_gadgets] ([code])');
    }
}
