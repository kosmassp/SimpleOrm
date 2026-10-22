<?php

declare(strict_types=1);

namespace SimpleOrm\Migrations;

use SimpleOrm\Metadata\TriggerDefinition;
use SimpleOrm\Metadata\TriggerEvent;
use SimpleOrm\Metadata\TriggerTiming;

/**
 * A trigger declaration as **data** (Level 4, ADR-0042): the `trigger` / `expectTrigger` of a data-driven
 * {@see SqlVersionStep} — the conformance suite's form, and what a future generator can target. Relation, whether it is
 * a view, timing, events, update-of columns, and the neutral body; the name is the step's object.
 */
final readonly class SqlVersionTrigger
{
    /**
     * @param list<TriggerEvent> $events
     * @param list<string> $updateOf column names
     */
    public function __construct(
        public string $relation,
        public bool $onView,
        public TriggerTiming $timing,
        public array $events,
        public string $sql,
        public array $updateOf = [],
    ) {
    }

    /** The declaration under the step's object name. */
    public function schema(string $name): TriggerSchema
    {
        return new TriggerSchema($this->relation, $this->onView, new TriggerDefinition($name, $this->timing, $this->events, $this->sql, $this->updateOf));
    }
}
