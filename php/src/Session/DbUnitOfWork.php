<?php

declare(strict_types=1);

namespace SimpleOrm\Session;

use SimpleOrm\Errors\SimpleOrmException;
use SimpleOrm\Metadata\CascadeOperation;
use SimpleOrm\Metadata\EntityMap;
use SimpleOrm\Metadata\RelationshipKind;
use SimpleOrm\Metadata\RelationshipMap;
use SplMinHeap;
use Throwable;

use function array_fill_keys;
use function array_filter;
use function array_key_exists;
use function array_keys;
use function array_map;
use function array_values;
use function count;
use function implode;
use function in_array;
use function is_array;
use function spl_object_id;
use function usort;

/**
 * The unit of work of a tracked session (Level 3 M2, ADR-0034 §7c, spec/unit-of-work.md): `persist`
 * and `remove` schedule, `flush` writes — inserts in foreign-key order with generated keys flowing
 * into dependents, then the dirty tracked instances as column-list updates, then deletes children
 * first — inside the session's transaction (or one the flush owns). A collaborator beside
 * {@see DbTracking} (CODING-STANDARD §10), owned by {@see Db} only in `Tracked` mode.
 *
 * **Every statement is the session's own `insert`/`update`/`delete`** (CODING-STANDARD §8, spec
 * "every statement is the same generated CRUD"): the identity gate (`UOW-002`), snapshots, version
 * rules (`CRUD-010`), and `CRUD-004` apply unchanged — this class decides only *what* and *in
 * which order*, never *how*. Cascades are declared metadata ({@see RelationshipMap::$cascade});
 * nothing here loads, and "never loaded" is an uninitialized collection property, tested with
 * `ReflectionProperty::isInitialized()` so it never fires the `REL-004` guard.
 *
 * A `persist`/`remove` is planned on a clone of the schedule and swapped in only when its whole
 * cascade succeeded: a refusal anywhere schedules nothing. A failed flush keeps the schedule and
 * undoes its in-memory effects through a {@see FlushJournal} (Clarifications, ADR-0036).
 *
 * @internal
 */
final class DbUnitOfWork
{
    private UnitOfWorkSchedule $schedule;

    public function __construct(
        private readonly Db $db,
        private readonly DbTracking $tracking,
    ) {
        $this->schedule = new UnitOfWorkSchedule();
    }

    /** Whether anything is scheduled; dirty tracked instances flush regardless. */
    public function hasPendingWork(): bool
    {
        return !$this->schedule->isEmpty();
    }

    /** Drops the schedule: the session closed. */
    public function clear(): void
    {
        $this->schedule = new UnitOfWorkSchedule();
    }

    // --- scheduling -------------------------------------------------------------

    /**
     * Schedules an insert for an instance the session does not track (a tracked one is already
     * managed: its changes flush as updates) and cascades over the initialized navigations that
     * declare `persist`. The refusals come in the order of spec/unit-of-work.md Clarifications: the
     * instance's own shape (`CRUD-003`/`CRUD-002`), `UOW-006` for an instance scheduled for removal,
     * then the cascade — each target checked the same way; a refusal anywhere schedules nothing.
     */
    public function persist(object $entity): void
    {
        $draft = clone $this->schedule;
        $visited = [];
        $this->collectPersist($draft, $entity, null, null, $visited);
        $this->schedule = $draft;
    }

    /**
     * Schedules a delete for a tracked instance (`UOW-001` otherwise), cascading over the navigations
     * that declare `remove`; an instance scheduled for insert is cancelled instead (with the inserts its
     * persist cascade scheduled), and one already scheduled for removal is a no-op. A never-loaded
     * collection under a remove cascade is `UOW-005`.
     */
    public function remove(object $entity): void
    {
        $draft = clone $this->schedule;
        $visited = [];
        $this->collectRemove($draft, $entity, $visited);
        $this->schedule = $draft;
    }

    /**
     * @param array<int, true> $visited
     * @param-out array<int, true> $visited
     */
    private function collectPersist(UnitOfWorkSchedule $draft, object $entity, ?object $owner, ?RelationshipMap $via, array &$visited): void
    {
        $map = $this->db->requireWritableKeyed($entity::class);
        if ($draft->isRemovalScheduled($entity)) {
            throw new SimpleOrmException(
                'UOW-006',
                $map->entityName(),
                'is scheduled for removal; an instance cannot be persisted and removed in the same unit of work',
            );
        }

        // A tracked instance schedules nothing for itself but still cascades over its initialized navigations: a
        // new child in a loaded collection is scheduled by persisting its loaded owner (Clarifications). A tracked
        // child a cascade reaches is not scheduled and gets no owner link, so a flush never rewrites its FK.
        if (!$this->tracking->isTracked($entity)) {
            $draft->scheduleInsert($entity, $map, $owner, $via);
        }

        $id = spl_object_id($entity);
        if (isset($visited[$id])) {
            return;
        }

        $visited[$id] = true;
        foreach ($map->relationships as $relationship) {
            if (!in_array(CascadeOperation::Persist, $relationship->cascade, true)) {
                continue;
            }

            foreach ($this->navigationTargets($map, $relationship, $entity) as $target) {
                $this->collectPersist($draft, $target, $entity, $relationship, $visited);
            }
        }
    }

    /**
     * @param array<int, true> $visited
     * @param-out array<int, true> $visited
     */
    private function collectRemove(UnitOfWorkSchedule $draft, object $entity, array &$visited): void
    {
        $id = spl_object_id($entity);
        if (isset($visited[$id])) {
            return;
        }

        $visited[$id] = true;

        if ($draft->isInsertScheduled($entity)) {
            // Nothing was written, so nothing is deleted and no remove cascade runs: the insert (and what only
            // its persist cascade scheduled) goes.
            $draft->cancelInsert($entity);

            return;
        }

        if ($draft->isRemovalScheduled($entity)) {
            return;   // already scheduled for removal: a no-op
        }

        // Shape, then UOW-001 (an instance the session neither tracks nor has scheduled — a stranger in a loaded
        // collection too, so the whole cascade schedules nothing), then the cascade (Clarifications).
        $map = $this->db->requireWritableKeyed($entity::class);
        if (!$this->tracking->isTracked($entity)) {
            throw DbTracking::notTracked($entity);
        }

        $draft->scheduleRemoval($entity, $map);

        foreach ($map->relationships as $relationship) {
            if (!in_array(CascadeOperation::Remove, $relationship->cascade, true)) {
                continue;
            }

            // UOW-005 is for a never-loaded *collection* only; a singular navigation cannot be told apart from
            // "unloaded" (null is both) and cascades nothing (Clarifications).
            if ($relationship->kind === RelationshipKind::OneToMany
                && !$map->navigationProperty($relationship)->isInitialized($entity)) {
                throw new SimpleOrmException(
                    'UOW-005',
                    $map->navigationTarget($relationship),
                    'the collection was never loaded and nothing loads implicitly: load it first ($db->load()) or remove the children yourself',
                );
            }

            foreach ($this->navigationTargets($map, $relationship, $entity) as $target) {
                $this->collectRemove($draft, $target, $visited);
            }
        }
    }

    /**
     * The entities a navigation currently holds: none for an uninitialized property (never loaded),
     * none for null, the one reference, or every member of a collection — read without ever
     * triggering the `REL-004` guard.
     *
     * @return list<object>
     */
    private function navigationTargets(EntityMap $map, RelationshipMap $relationship, object $entity): array
    {
        $property = $map->navigationProperty($relationship);
        if (!$property->isInitialized($entity)) {
            return [];
        }

        $value = $property->getValue($entity);
        if ($value === null) {
            return [];
        }

        return is_array($value) ? array_values($value) : [$value];
    }

    private function singularTarget(EntityMap $map, RelationshipMap $relationship, object $entity): ?object
    {
        return $this->navigationTargets($map, $relationship, $entity)[0] ?? null;
    }

    // --- flush ------------------------------------------------------------------

    /**
     * Writes the schedule: inserts, then every dirty tracked instance, then deletes (unit-of-work.md
     * "Order of writes"). The order is planned — and a cycle is `UOW-004` — before any SQL. Work already
     * done immediately is dropped; nothing to write returns the zero summary without a transaction.
     * Inside the session's transaction when one is active, else one this flush begins, commits, and rolls
     * back on failure; a failed flush keeps the schedule and undoes its in-memory effects.
     */
    public function flush(): FlushSummary
    {
        $inserts = $this->pendingInserts();
        $removals = $this->pendingRemovals();
        $dirty = $this->dirtyEntries($removals);

        $ranks = TypeRanks::of($this->db->maps(), $this->involvedTypes($inserts, $removals, $dirty));
        $insertOrder = $this->order($inserts, $this->insertEdges($inserts), static fn (array $n): int => $ranks[$n['map']->entityType] ?? 0, 'inserts');
        $deleteOrder = $this->order($removals, $this->deleteEdges($removals), static fn (array $n): int => -($ranks[$n['map']->entityType] ?? 0), 'deletes');
        usort($dirty, static fn (TrackedInstance $a, TrackedInstance $b): int => ($ranks[$a->map->entityType] ?? 0) <=> ($ranks[$b->map->entityType] ?? 0));

        if ($insertOrder === [] && $dirty === [] && $deleteOrder === []) {
            $this->schedule = new UnitOfWorkSchedule();

            return new FlushSummary(0, 0, 0);
        }

        $connection = $this->db->connection();
        $ownsTransaction = !$connection->inTransaction();
        if ($ownsTransaction) {
            $connection->beginTransaction();
        }

        $journal = new FlushJournal($this->tracking);
        try {
            $this->write($inserts, $insertOrder, $dirty, $removals, $deleteOrder, $journal);
            if ($ownsTransaction) {
                $connection->commit();
            }
        } catch (Throwable $failure) {
            $journal->rollback();
            if ($ownsTransaction && $connection->inTransaction()) {
                $connection->rollBack();
            }

            throw $failure;
        }

        $this->schedule = new UnitOfWorkSchedule();

        return new FlushSummary(count($insertOrder), count($dirty), count($deleteOrder));
    }

    /**
     * The statements, in order — each the session's own insert/update/delete, journaled so a failure can undo
     * what it moved in memory.
     *
     * @param array<int, array{instance: object, map: EntityMap, seq: int, direct: bool, links: array<string, array{owner: object, relationship: RelationshipMap}>}> $inserts
     * @param list<int> $insertOrder
     * @param list<TrackedInstance> $dirty
     * @param array<int, array{instance: object, map: EntityMap, seq: int}> $removals
     * @param list<int> $deleteOrder
     */
    private function write(array $inserts, array $insertOrder, array $dirty, array $removals, array $deleteOrder, FlushJournal $journal): void
    {
        foreach ($insertOrder as $id) {
            $entry = $inserts[$id];
            $this->flowKeys($entry);
            $journal->recordInsert($entry['map'], $entry['instance']);
            $this->db->insert($entry['instance']);
        }

        foreach ($dirty as $entry) {
            $journal->recordUpdate($entry->map, $entry->instance);
            $this->db->update($entry->instance);
        }

        foreach ($deleteOrder as $id) {
            // The entity form: identity-gated and version-checked (CRUD-010), so a stale instance is never deleted blindly.
            $this->db->delete($removals[$id]['map']->entityType, $removals[$id]['instance']);
        }
    }

    /**
     * The scheduled inserts still to write: one already inserted immediately (it is tracked now) is done, not
     * work (Clarifications).
     *
     * @return array<int, array{instance: object, map: EntityMap, seq: int, direct: bool, links: array<string, array{owner: object, relationship: RelationshipMap}>}>
     */
    private function pendingInserts(): array
    {
        return array_filter(
            $this->schedule->inserts(),
            fn (array $entry): bool => !$this->tracking->isTracked($entry['instance']),
        );
    }

    /**
     * The scheduled removals still to write: one already deleted immediately (it is no longer tracked) is done.
     *
     * @return array<int, array{instance: object, map: EntityMap, seq: int}>
     */
    private function pendingRemovals(): array
    {
        return array_filter(
            $this->schedule->removals(),
            fn (array $entry): bool => $this->tracking->isTracked($entry['instance']),
        );
    }

    /**
     * The tracked instances an update would write something for, in registration order: not scheduled
     * for removal, with at least one dirty column that is not the key, the version, or generated. A dirty
     * instance of a read-only source is not skipped: its update refuses `CRUD-003` and the flush fails.
     *
     * @param array<int, array{instance: object, map: EntityMap, seq: int}> $removals
     * @return list<TrackedInstance>
     */
    private function dirtyEntries(array $removals): array
    {
        $dirty = [];
        foreach ($this->tracking->entries() as $entry) {
            if (!array_key_exists(spl_object_id($entry->instance), $removals)
                && $this->tracking->writableDirtyProperties($entry) !== []) {
                $dirty[] = $entry;
            }
        }

        return $dirty;
    }

    /**
     * Keys flow before each insert (unit-of-work.md): every non-null many-to-one navigation copies its
     * target's key into the FK properties (the navigation is authoritative inside the unit of work), and
     * a child a persist cascade reached through a one-to-many/one-to-one takes its owner's key into the FK
     * its relationship declares.
     *
     * @param array{instance: object, map: EntityMap, seq: int, direct: bool, links: array<string, array{owner: object, relationship: RelationshipMap}>} $entry
     */
    private function flowKeys(array $entry): void
    {
        $entity = $entry['instance'];
        $map = $entry['map'];

        foreach ($map->relationships as $relationship) {
            if ($relationship->kind !== RelationshipKind::ManyToOne) {
                continue;
            }

            $target = $this->singularTarget($map, $relationship, $entity);
            if ($target !== null) {
                $this->copyKey($this->db->maps()->load($relationship->targetType)->getKeyValues($target), $map, $relationship->foreignKeyProperties, $entity);
            }
        }

        foreach ($entry['links'] as $link) {
            if ($link['relationship']->kind === RelationshipKind::ManyToOne) {
                continue;   // the owner references this parent; the owner's own navigation carries the key
            }

            $ownerMap = $this->db->maps()->load($link['owner']::class);
            $this->copyKey($ownerMap->getKeyValues($link['owner']), $map, $link['relationship']->foreignKeyProperties, $entity);
        }
    }

    /**
     * Copies the key values as they are — a null part included, the database foreign key is the authority
     * (Clarifications: keys flow only before inserts). The one thing a typed PHP property cannot take is a
     * null into a non-nullable FK property: that part is left as it is and the insert's own checks
     * (`CRUD-004`, the database) refuse it (CODING-STANDARD §10).
     *
     * @param list<mixed> $keyValues
     * @param list<string> $foreignKeyProperties
     */
    private function copyKey(array $keyValues, EntityMap $into, array $foreignKeyProperties, object $entity): void
    {
        foreach ($foreignKeyProperties as $i => $name) {
            $property = $into->property($name);
            if ($property === null || ($keyValues[$i] === null && !$property->nullable)) {
                continue;
            }

            $property->setValue($entity, $keyValues[$i]);
        }
    }

    // --- ordering ---------------------------------------------------------------

    /**
     * Instance dependencies of the scheduled inserts: a parent before the child that references it
     * through a non-null many-to-one navigation, an owner before the child its persist cascade reached
     * through a one-to-many/one-to-one. Navigations and cascade links only — never FK values.
     *
     * @param array<int, array{instance: object, map: EntityMap, seq: int, direct: bool, links: array<string, array{owner: object, relationship: RelationshipMap}>}> $inserts
     * @return array<int, array<int, int>> from id → ids that must come after it
     */
    private function insertEdges(array $inserts): array
    {
        $edges = [];
        foreach ($inserts as $id => $entry) {
            foreach ($entry['map']->relationships as $relationship) {
                if ($relationship->kind !== RelationshipKind::ManyToOne) {
                    continue;
                }

                $target = $this->singularTarget($entry['map'], $relationship, $entry['instance']);
                if ($target !== null && isset($inserts[spl_object_id($target)])) {
                    self::addEdge($edges, spl_object_id($target), $id);
                }
            }

            foreach ($entry['links'] as $link) {
                if ($link['relationship']->kind !== RelationshipKind::ManyToOne && isset($inserts[spl_object_id($link['owner'])])) {
                    self::addEdge($edges, spl_object_id($link['owner']), $id);
                }
            }
        }

        return $edges;
    }

    /**
     * Instance dependencies of the scheduled deletes, children first: a child before the parent it
     * references through a many-to-one navigation, and before the owner whose loaded one-to-one /
     * one-to-many navigation holds it.
     *
     * @param array<int, array{instance: object, map: EntityMap, seq: int}> $removals
     * @return array<int, array<int, int>>
     */
    private function deleteEdges(array $removals): array
    {
        $edges = [];
        foreach ($removals as $id => $entry) {
            foreach ($entry['map']->relationships as $relationship) {
                foreach ($this->navigationTargets($entry['map'], $relationship, $entry['instance']) as $target) {
                    $targetId = spl_object_id($target);
                    if (!isset($removals[$targetId])) {
                        continue;
                    }

                    match ($relationship->kind) {
                        RelationshipKind::ManyToOne => self::addEdge($edges, $id, $targetId),
                        RelationshipKind::OneToOne, RelationshipKind::OneToMany => self::addEdge($edges, $targetId, $id),
                        RelationshipKind::ManyToMany => null,
                    };
                }
            }
        }

        return $edges;
    }

    /** @param array<int, array<int, int>> $edges */
    private static function addEdge(array &$edges, int $from, int $to): void
    {
        // An instance referencing itself is not a cycle among the scheduled instances: the edge is ignored (Clarifications).
        if ($from !== $to) {
            $edges[$from][$to] = $to;
        }
    }

    /**
     * Kahn's algorithm with a deterministic tie-break: among the instances whose dependencies are
     * written, the lowest (type rank, schedule order) goes next. A cycle leaves instances unwritten
     * and is `UOW-004` — raised before any SQL.
     *
     * @param array<int, array{instance: object, map: EntityMap, seq: int}> $nodes keyed by spl_object_id
     * @param array<int, array<int, int>> $edges
     * @param callable(array{instance: object, map: EntityMap, seq: int}): int $rank
     * @return list<int> ids in write order
     */
    private function order(array $nodes, array $edges, callable $rank, string $what): array
    {
        $incoming = array_fill_keys(array_keys($nodes), 0);
        foreach ($edges as $targets) {
            foreach ($targets as $to) {
                $incoming[$to]++;
            }
        }

        $ready = new SplMinHeap();
        foreach ($nodes as $id => $node) {
            if ($incoming[$id] === 0) {
                $ready->insert([$rank($node), $node['seq'], $id]);
            }
        }

        $order = [];
        while (!$ready->isEmpty()) {
            $id = $ready->extract()[2];
            $order[] = $id;
            foreach ($edges[$id] ?? [] as $to) {
                if (--$incoming[$to] === 0) {
                    $ready->insert([$rank($nodes[$to]), $nodes[$to]['seq'], $to]);
                }
            }
        }

        if (count($order) !== count($nodes)) {
            $stuck = array_map(
                static fn (array $node): string => $node['map']->entityName(),
                array_filter($nodes, static fn (int $id): bool => !in_array($id, $order, true), ARRAY_FILTER_USE_KEY),
            );

            throw new SimpleOrmException(
                'UOW-004',
                'session',
                "the scheduled {$what} cannot be ordered: " . implode(', ', array_values($stuck))
                    . ' reference each other in a cycle through their navigations; break the cycle (persist one side first, set its reference after the flush)',
            );
        }

        return $order;
    }

    /**
     * @param array<int, array{map: EntityMap}> $inserts
     * @param array<int, array{map: EntityMap}> $removals
     * @param list<TrackedInstance> $dirty
     * @return list<class-string>
     */
    private function involvedTypes(array $inserts, array $removals, array $dirty): array
    {
        $types = [];
        foreach ([...$inserts, ...$removals] as $entry) {
            $types[$entry['map']->entityType] = true;
        }

        foreach ($dirty as $entry) {
            $types[$entry->map->entityType] = true;
        }

        return array_keys($types);
    }
}
