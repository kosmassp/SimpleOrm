<?php

declare(strict_types=1);

namespace SimpleOrm\Migrations;

use SimpleOrm\Dialect\Dialect;
use SimpleOrm\Metadata\TriggerDefinition;

/**
 * A trigger's declaration with the relation it is on (Level 4, ADR-0042): what a trigger snapshot records, what the
 * diff compares, and what the dialect renders. Dialect-neutral on every side — nothing in it is a rendering — so one
 * snapshot tree serves every dialect. The counterpart of {@see TableSchema} for the `Trigger/<Class>/` object kind.
 */
final readonly class TriggerSchema
{
    public function __construct(
        public string $relation,
        public bool $onView,
        public TriggerDefinition $trigger,
    ) {
    }

    /** The trigger's declared name — the migration object's name and the `schema_version` row's object. */
    public function name(): string
    {
        return $this->trigger->name;
    }

    /** The snapshot token: `table` or `view`. */
    public function relationKind(): string
    {
        return $this->onView ? 'view' : 'table';
    }

    /** The same trigger declared the same way: relation, its kind, timing, events, update-of columns, and normalized body. */
    public function sameAs(self $other): bool
    {
        return $this->relation === $other->relation
            && $this->onView === $other->onView
            && $this->trigger->sameAs($other->trigger);
    }

    /**
     * The create statements this declaration renders to under a dialect (`DDL-005` where it has no form).
     *
     * @return list<string>
     */
    public function createStatements(Dialect $dialect): array
    {
        return $dialect->createTriggerSql($this->relation, $this->onView, $this->trigger);
    }
}
