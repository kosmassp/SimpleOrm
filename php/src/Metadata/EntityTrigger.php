<?php

declare(strict_types=1);

namespace SimpleOrm\Metadata;

/**
 * A trigger an entity links (Level 4, ADR-0042): the trigger class (the declaration object, which names the
 * migration object folder) and its resolved declaration. Entities carry them in link order (`EntityMap::$triggers`).
 */
final readonly class EntityTrigger
{
    /** @param class-string $triggerClass */
    public function __construct(
        public string $triggerClass,
        public TriggerDefinition $definition,
    ) {
    }
}
