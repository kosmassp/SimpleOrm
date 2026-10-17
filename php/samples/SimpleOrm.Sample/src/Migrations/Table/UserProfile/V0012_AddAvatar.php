<?php

declare(strict_types=1);

namespace SimpleOrm\Sample\Migrations\Table\UserProfile;

use SimpleOrm\Migrations\TableActions;
use SimpleOrm\Migrations\TableMigration;
use SimpleOrm\Sample\Models\Tables\UserProfile;

/**
 * The avatar image of UserProfile (ADR-0040 add.1) — the fixture's one bytes column, nullable,
 * so binary round trips are covered on every dialect. No down — the runner derives the rollback
 * from the snapshots (ADR-0018).
 */
final class V0012_AddAvatar extends TableMigration
{
    public function entityClass(): string
    {
        return UserProfile::class;
    }

    public function action(TableActions $actions): void
    {
        $actions->addColumn('avatar', 'BLOB');
    }
}
