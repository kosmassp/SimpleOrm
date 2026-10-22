<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Dialect\Fixtures\ShadowTree\Trigger\ShadowGadgetTrigger;

use SimpleOrm\Migrations\TriggerActions;
use SimpleOrm\Migrations\TriggerMigration;
use SimpleOrm\Tests\Dialect\Fixtures\ShadowTree\Models\ShadowGadget;
use SimpleOrm\Tests\Dialect\Fixtures\ShadowTree\Models\ShadowGadgetTrigger;

final class V0003_Create extends TriggerMigration
{
    public function entityClass(): string
    {
        return ShadowGadget::class;
    }

    public function triggerClass(): string
    {
        return ShadowGadgetTrigger::class;
    }

    public function action(TriggerActions $actions): void
    {
        $actions->createTrigger();
    }
}
