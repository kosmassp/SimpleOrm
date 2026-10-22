<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Dialect\Fixtures\ShadowTree\View\ShadowGadgetCodes;

use SimpleOrm\Migrations\ViewActions;
use SimpleOrm\Migrations\ViewMigration;
use SimpleOrm\Tests\Dialect\Fixtures\ShadowTree\Models\ShadowGadgetCodes;

final class V0002_Create extends ViewMigration
{
    public function entityClass(): string
    {
        return ShadowGadgetCodes::class;
    }

    public function action(ViewActions $actions): void
    {
        $actions->createView();
    }
}
