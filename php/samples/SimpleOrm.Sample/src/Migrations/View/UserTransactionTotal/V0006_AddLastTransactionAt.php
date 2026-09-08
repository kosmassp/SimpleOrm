<?php

declare(strict_types=1);

namespace SimpleOrm\Sample\Migrations\View\UserTransactionTotal;

use SimpleOrm\Migrations\ViewActions;
use SimpleOrm\Migrations\ViewMigration;
use SimpleOrm\Sample\Models\Views\UserTransactionTotal;

/**
 * Views self-reflect (ADR-0008/0013): the defining SQL lives in the attribute, so a
 * view change is simply "recreate at this version" from the current definition.
 */
final class V0006_AddLastTransactionAt extends ViewMigration
{
    public function entityClass(): string
    {
        return UserTransactionTotal::class;
    }

    public function action(ViewActions $actions): void
    {
        $actions->recreateView();
    }
}
