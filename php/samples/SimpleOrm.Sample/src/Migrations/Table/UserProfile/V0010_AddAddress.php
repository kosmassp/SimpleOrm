<?php

declare(strict_types=1);

namespace SimpleOrm\Sample\Migrations\Table\UserProfile;

use SimpleOrm\Migrations\TableActions;
use SimpleOrm\Migrations\TableMigration;
use SimpleOrm\Sample\Models\Tables\UserProfile;

/**
 * The #[Owned] Address of UserProfile (ADR-0030) as three nullable columns —
 * nullable because the navigation is: an all-NULL row reads back as no address.
 * No down — the runner derives the rollback from the snapshots (ADR-0018).
 */
final class V0010_AddAddress extends TableMigration
{
    public function entityClass(): string
    {
        return UserProfile::class;
    }

    public function action(TableActions $actions): void
    {
        $actions->addColumn('address_street', 'TEXT');
        $actions->addColumn('address_city', 'TEXT');
        $actions->addColumn('address_postal_code', 'TEXT');
    }
}
