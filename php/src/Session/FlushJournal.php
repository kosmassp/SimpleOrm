<?php

declare(strict_types=1);

namespace SimpleOrm\Session;

use Closure;
use SimpleOrm\Metadata\EntityMap;
use SimpleOrm\Metadata\PropertyMap;

use function array_reverse;

/**
 * Puts the session's in-memory state back when a flush fails (Level 3 M2, spec/unit-of-work.md
 * Clarifications, ADR-0036: "a failed flush undoes its in-memory effects"). The database side is the
 * transaction's rollback; this is the rest: the identity map and the snapshots (a checkpoint of
 * {@see DbTracking}, whose entries are immutable), and the two values a statement writes onto an
 * instance — a generated or client-assigned key (an insert) and the bumped version (an update).
 * After {@see rollback()} the whole schedule flushes again as it was.
 *
 * @internal owned by {@see DbUnitOfWork}
 */
final class FlushJournal
{
    /** @var array{0: array<class-string, array<string, TrackedInstance>>, 1: array<int, TrackedInstance>} */
    private readonly array $checkpoint;

    /** @var list<Closure(): void> */
    private array $undo = [];

    public function __construct(private readonly DbTracking $tracking)
    {
        $this->checkpoint = $tracking->checkpoint();
    }

    /** Remembers the key values an insert is about to assign (or leave unset) and the `#[CreatedAt]` value it may fill. */
    public function recordInsert(EntityMap $map, object $entity): void
    {
        foreach ($map->keyProperties as $key) {
            $this->recordValue($key, $entity);
        }

        // The #[CreatedAt] value an undone insert filled goes back to unset too, so the retry takes the clock again
        // (spec/hooks.md Clarifications: the fill of an undone insert's created-at).
        $this->recordProperties([$map->createdAtProperty()], $entity);
    }

    /** Remembers the version an update is about to bump and the `#[UpdatedAt]` stamp it takes. */
    public function recordUpdate(EntityMap $map, object $entity): void
    {
        $this->recordProperties([$map->versionProperty, $map->updatedAtProperty()], $entity);
    }

    /**
     * Remembers what a delete may move on the instance: nothing for a hard delete, the flag, the stamp, and the
     * version for a soft delete (spec/hooks.md) — all three are set on the instance by the statement.
     */
    public function recordDelete(EntityMap $map, object $entity): void
    {
        if ($map->softDeleteProperty() !== null) {
            $this->recordProperties([$map->softDeleteProperty(), $map->updatedAtProperty(), $map->versionProperty], $entity);
        }
    }

    public function rollback(): void
    {
        $this->tracking->rollbackTo($this->checkpoint);
        foreach (array_reverse($this->undo) as $undo) {
            $undo();
        }

        $this->undo = [];
    }

    /** @param list<PropertyMap|null> $properties */
    private function recordProperties(array $properties, object $entity): void
    {
        foreach ($properties as $property) {
            if ($property !== null) {
                $this->recordValue($property, $entity);
            }
        }
    }

    /** An uninitialized property goes back to uninitialized (an `int $id` has no value before its insert), not to null. */
    private function recordValue(PropertyMap $property, object $entity): void
    {
        $this->undo[] = $property->remember($entity);
    }
}
