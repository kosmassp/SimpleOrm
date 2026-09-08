<?php

declare(strict_types=1);

namespace SimpleOrm\Sample\Migrations\View\UserTransactionTotal;

use SimpleOrm\Migrations\ViewActions;
use SimpleOrm\Migrations\ViewMigration;
use SimpleOrm\Sample\Models\Views\UserTransactionTotal;

final class V0001_CreateTotalsView extends ViewMigration
{
    public function entityClass(): string
    {
        return UserTransactionTotal::class;
    }

    public function action(ViewActions $actions): void
    {
        $actions->createView();
    }
}
