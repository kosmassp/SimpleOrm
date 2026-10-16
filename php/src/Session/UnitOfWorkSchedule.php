<?php

declare(strict_types=1);

namespace SimpleOrm\Session;

use SimpleOrm\Metadata\EntityMap;
use SimpleOrm\Metadata\RelationshipMap;

use function array_key_exists;
use function spl_object_id;

/**
 * The unit of work's pending schedule (Level 3 M2, unit-of-work.md): the instances scheduled for
 * insert (with the cascade links recording which owner's key each cascaded child takes) and for
 * removal, each with its schedule sequence number. A deliberately small mutable value: the unit
 * of work plans a `persist`/`remove` on a **clone** and swaps it in only when the whole cascade
 * succeeded, so a refusal anywhere schedules nothing. Entries are arrays (value semantics), so a
 * clone never shares a mutation with the committed schedule.
 *
 * @internal owned by {@see DbUnitOfWork}
 */
final class UnitOfWorkSchedule
{
    /**
     * @var array<int, array{instance: object, map: EntityMap, seq: int, direct: bool, links: array<string, array{owner: object, relationship: RelationshipMap}>}>
     *      keyed by spl_object_id; `direct` is true once the user persisted the instance itself, `links` the owners whose persist cascade reached it
     */
    private array $inserts = [];

    /** @var array<int, array{instance: object, map: EntityMap, seq: int}> keyed by spl_object_id */
    private array $removals = [];

    private int $nextSeq = 0;

    public function isEmpty(): bool
    {
        return $this->inserts === [] && $this->removals === [];
    }

    public function isInsertScheduled(object $instance): bool
    {
        return array_key_exists(spl_object_id($instance), $this->inserts);
    }

    public function isRemovalScheduled(object $instance): bool
    {
        return array_key_exists(spl_object_id($instance), $this->removals);
    }

    /**
     * Schedules an insert, or — for an instance already scheduled — records what newly reached it: the user's
     * own `persist` (no `$via`) marks it direct; a persist cascade records the link (owner and navigation) that
     * reached it. A one-to-many/one-to-one link is where the child takes its owner's key at flush; a many-to-one
     * link only records that the owner scheduled the parent, so cancelling the owner can cancel the parent too
     * (spec/unit-of-work.md Clarifications, ADR-0036: cancelling a scheduled insert).
     */
    public function scheduleInsert(object $instance, EntityMap $map, ?object $owner, ?RelationshipMap $via): void
    {
        $id = spl_object_id($instance);
        $entry = $this->inserts[$id] ?? [
            'instance' => $instance,
            'map' => $map,
            'seq' => $this->nextSeq++,
            'direct' => false,
            'links' => [],
        ];

        // A child persisted directly by name stays scheduled when its owner is cancelled: the user asked for it.
        if ($via === null) {
            $entry['direct'] = true;
        } elseif ($owner !== null) {
            $entry['links'][spl_object_id($owner) . ':' . $via->propertyName] = ['owner' => $owner, 'relationship' => $via];
        }

        $this->inserts[$id] = $entry;
    }

    /**
     * Cancels a scheduled insert and, transitively, the inserts only its persist cascade reached:
     * a child with another remaining owner link, or one the user persisted directly, stays.
     */
    public function cancelInsert(object $instance): void
    {
        $id = spl_object_id($instance);
        if (!array_key_exists($id, $this->inserts)) {
            return;
        }

        unset($this->inserts[$id]);

        foreach ($this->inserts as $childId => $entry) {
            if (!array_key_exists($childId, $this->inserts)) {
                continue;   // cancelled by a deeper recursion already
            }

            $remaining = array_filter(
                $entry['links'],
                static fn (array $link): bool => spl_object_id($link['owner']) !== $id,
            );
            if (count($remaining) === count($entry['links'])) {
                continue;
            }

            $this->inserts[$childId]['links'] = $remaining;
            if ($remaining === [] && !$entry['direct']) {
                $this->cancelInsert($entry['instance']);
            }
        }
    }

    public function scheduleRemoval(object $instance, EntityMap $map): void
    {
        $id = spl_object_id($instance);
        if (!array_key_exists($id, $this->removals)) {
            $this->removals[$id] = ['instance' => $instance, 'map' => $map, 'seq' => $this->nextSeq++];
        }
    }

    /** Drops a scheduled removal (detach); a no-op for an instance not scheduled for one. */
    public function cancelRemoval(object $instance): void
    {
        unset($this->removals[spl_object_id($instance)]);
    }

    /** @return array<int, array{instance: object, map: EntityMap, seq: int, direct: bool, links: array<string, array{owner: object, relationship: RelationshipMap}>}> */
    public function inserts(): array
    {
        return $this->inserts;
    }

    /** @return array<int, array{instance: object, map: EntityMap, seq: int}> */
    public function removals(): array
    {
        return $this->removals;
    }
}
