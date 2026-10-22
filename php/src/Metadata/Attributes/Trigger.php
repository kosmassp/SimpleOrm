<?php

declare(strict_types=1);

namespace SimpleOrm\Metadata\Attributes;

use Attribute;
use SimpleOrm\Metadata\TriggerEvent;
use SimpleOrm\Metadata\TriggerTiming;

/**
 * Triggers in the mapping (Level 4, ADR-0042, spec/metadata-model.md "Triggers"). One attribute, two shapes:
 *
 *  - on a **trigger class** (a declaration object, never an entity), exactly one:
 *    `#[Trigger('trg_roles_audit', TriggerTiming::After, [TriggerEvent::Update], sql: '…{new: id}…', updateOf: ['name'])]`
 *    — the name, the timing, the events (a set: an array of {@see TriggerEvent}), the neutral body with its tokens
 *    (`{new: cols}`, `{old: cols}`, `{now}`), and optionally the entity **properties** an update narrows to;
 *  - on an **entity**, repeatable, the link: `#[Trigger(RoleAuditTrigger::class)]` — the one argument is the trigger
 *    class.
 *
 * A trigger attribute carrying only its first argument is a link ({@see self::isLink()}); anything else is a
 * declaration, and an incomplete one (no timing, no event, a blank body) is `MAP-029`, never a link.
 */
#[Attribute(Attribute::TARGET_CLASS | Attribute::IS_REPEATABLE)]
final readonly class Trigger
{
    /**
     * @param string $nameOrClass the trigger name (a declaration) or the trigger class (a link)
     * @param list<TriggerEvent> $events
     * @param list<string> $updateOf entity property names (resolved to columns); only with the update event
     */
    public function __construct(
        public string $nameOrClass,
        public ?TriggerTiming $timing = null,
        public array $events = [],
        public string $sql = '',
        public array $updateOf = [],
    ) {
    }

    /** The entity-side link (`#[Trigger(RoleAuditTrigger::class)]`): nothing but the class. */
    public function isLink(): bool
    {
        return $this->timing === null && $this->events === [] && $this->sql === '' && $this->updateOf === [];
    }
}
