<?php

declare(strict_types=1);

namespace SimpleOrm\Metadata;

/**
 * A trigger as the dialects and the migration tooling see it (Level 4, ADR-0042): the **neutral declaration** — name,
 * timing, events in the fixed order, the update-of **column** names, and one body with its tokens unexpanded.
 * Nothing in it names a dialect or a relation; the relation travels beside it (`TriggerSchema`, the dialect seam's
 * `relation` argument). Immutable; built by the loaders (resolved from an `#[Trigger]` declaration), by the
 * snapshot parser, and by the data-driven migration steps.
 */
final readonly class TriggerDefinition
{
    /** @var list<TriggerEvent> in the fixed order insert, update, delete, without repeats */
    public array $events;

    /** @var list<string> column names the update narrows to; empty when the trigger is not narrowed */
    public array $updateOf;

    /**
     * @param list<TriggerEvent> $events normalized to the fixed order
     * @param list<string> $updateOf
     */
    public function __construct(
        public string $name,
        public TriggerTiming $timing,
        array $events,
        public string $sql,
        array $updateOf = [],
    ) {
        $this->events = TriggerEvent::ordered($events);
        $this->updateOf = array_values($updateOf);
    }

    /** The body whitespace-collapsed with its tokens unexpanded and canonically spelled (layout is not schema). */
    public function normalizedSql(): string
    {
        return TriggerBody::normalize($this->sql);
    }

    public function hasEvent(TriggerEvent $event): bool
    {
        return in_array($event, $this->events, true);
    }

    /** The same declaration: timing, events, update-of columns, and normalized body (the name is the object's identity, compared by callers). */
    public function sameAs(self $other): bool
    {
        return $this->timing === $other->timing
            && $this->events === $other->events
            && $this->updateOf === $other->updateOf
            && $this->normalizedSql() === $other->normalizedSql();
    }
}
