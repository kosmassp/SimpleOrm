<?php

declare(strict_types=1);

namespace SimpleOrm\Sample\Migrations;

use SimpleOrm\Migrations\MigrationVersion;
use SimpleOrm\Migrations\VersionBuilder;
use SimpleOrm\Sample\Migrations\Table\Role\V0001_CreateRoles;
use SimpleOrm\Sample\Migrations\Table\TransactionDetail\V0001_CreateTransactionDetails;
use SimpleOrm\Sample\Migrations\Table\Transaction\V0001_CreateTransactions;
use SimpleOrm\Sample\Migrations\Table\UserRole\V0001_CreateUserRoles;
use SimpleOrm\Sample\Migrations\Table\User\V0001_CreateUsers;
use SimpleOrm\Sample\Migrations\View\UserTransactionTotal\V0001_CreateTotalsView;

/**
 * The initial schema. Root versions are the recorded, checksummed unit; they
 * compose per-object steps in explicit order: tables in FK order, views last.
 */
final class V0001 extends MigrationVersion
{
    public function compose(VersionBuilder $version): void
    {
        $version
            ->apply(V0001_CreateUsers::class)
            ->apply(V0001_CreateRoles::class)
            ->apply(V0001_CreateUserRoles::class)
            ->apply(V0001_CreateTransactions::class)
            ->apply(V0001_CreateTransactionDetails::class)
            ->apply(V0001_CreateTotalsView::class);
    }
}
