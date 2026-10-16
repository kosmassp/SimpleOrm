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

use function array_key_exists;
use function array_keys;
use function array_map;
use function array_shift;
use function count;
use function in_array;
use function is_array;
use function max;
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
 * cascade succeeded: a refusal anywhere schedules nothing.
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
     * declare `persist`. `UOW-006` for an instance scheduled for removal; `CRUD-003`/`CRUD-002` for one
     * that is not table-backed or keyed.
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
     * persist cascade scheduled). A never-loaded collection under a remove cascade is `UOW-005`.
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
        // SPEC-GAP: the order of refusals for persist is not stated; assumed CRUD-003/CRUD-002 (the instance's type), then
        // UOW-006, then the cascade — each cascaded target passes the same checks, and a failure anywhere schedules nothing.
        $map = $this->db->requireWritableKeyed($entity::class);
        if ($draft->isRemovalScheduled($entity)) {
            throw new SimpleOrmException(
                'UOW-006',
                $map->entityName(),
                'is scheduled for removal; an instance cannot be persisted and removed in the same unit of work',
            );
        }

        // SPEC-GAP: a tracked instance schedules nothing (spec), but whether its persist cascade still runs is not
        // stated. Assumed yes — unit-of-work.md says "a never-loaded collection of a tracked instance cascades
        // nothing", which implies a loaded one does. A tracked child a cascade reaches is not scheduled and gets no
        // owner link, so a flush never rewrites its FK to a freshly inserted owner's key (it is an update's business).
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
            // Nothing was written, so nothing is deleted: the insert (and what only its cascade scheduled) goes.
            $draft->cancelInsert($entity);

            return;
        }

        $map = $this->db->requireWritableKeyed($entity::class);
        if (!$draft->isRemovalScheduled($entity)) {
            // SPEC-GAP: a remove cascade reaching an instance the session neither tracks nor has scheduled for insert
            // (a stranger in a loaded collection) is not covered; assumed to refuse like a direct remove — UOW-001 —
            // so the whole cascade schedules nothing, rather than skipping it silently.
            if (!$this->tracking->isTracked($entity)) {
                throw DbTracking::notTracked($entity);
            }

            $draft->scheduleRemoval($entity, $map);
        }

        foreach ($map->relationships as $relationship) {
            if (!in_array(CascadeOperation::Remove, $relationship->cascade, true)) {
                continue;
            }

            // SPEC-GAP: UOW-005 is stated for a never-loaded *collection*; a singular navigation cannot be told apart
            // from "unloaded" (spec: "null is also what unloaded looks like") and cascades nothing. A remove of an
            // instance already scheduled for removal runs its cascade again (idempotent for what is already scheduled);
            // a removed instance scheduled for insert is cancelled without any remove cascade over its navigations.
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
     * "Order of writes"). The order is planned — and a cycle is `UOW-004` — before any SQL. Inside the
     * session's transaction when one is active, else one this flush begins, commits, and rolls back on
     * failure; a failed flush keeps the schedule and leaves the map as its statements left it.
     */
    public function flush(): FlushSummary
    {
        $inserts = $this->schedule->inserts();
        $removals = $this->schedule->removals();
        $dirty = $this->dirtyEntries($removals);

        $ranks = $this->typeRanks($this->involvedTypes($inserts, $removals, $dirty));
        $insertOrder = $this->order($inserts, $this->insertEdges($inserts), static fn (array $n): int => $ranks[$n['map']->entityType] ?? 0, 'inserts');
        $deleteOrder = $this->order($removals, $this->deleteEdges($removals), static fn (array $n): int => -($ranks[$n['map']->entityType] ?? 0), 'deletes');
        usort($dirty, static fn (TrackedInstance $a, TrackedInstance $b): int => ($ranks[$a->map->entityType] ?? 0) <=> ($ranks[$b->map->entityType] ?? 0));

        // SPEC-GAP: nothing planned at all returns the zero summary without opening a transaction (the spec's
        // "begins one" does not say whether an empty flush does). The dirty set is decided at plan time, before any
        // insert runs; inserted instances are registered clean, so deciding earlier or later gives the same list.
        if ($insertOrder === [] && $dirty === [] && $deleteOrder === []) {
            return new FlushSummary(0, 0, 0);
        }

        $connection = $this->db->connection();
        $ownsTransaction = !$connection->inTransaction();
        if ($ownsTransaction) {
            $connection->beginTransaction();
        }

        try {
            foreach ($insertOrder as $id) {
                $this->flowKeys($inserts[$id]);
                $this->db->insert($inserts[$id]['instance']);
            }

            foreach ($dirty as $entry) {
                $this->db->update($entry->instance);
            }

            foreach ($deleteOrder as $id) {
                // SPEC-GAP: by-key or entity form of the delete is not stated; the entity form (identity-gated and, with a
                // version column, version-checked — CRUD-010) is used so a stale instance is never deleted blindly.
                $this->db->delete($removals[$id]['map']->entityType, $removals[$id]['instance']);
            }

            if ($ownsTransaction) {
                $connection->commit();
            }
        } catch (Throwable $failure) {
            // SPEC-GAP: "a failed flush ... does not touch the identity map or the snapshots" cannot hold literally when
            // every statement is the session's own insert/update/delete (they register, re-snapshot, and bump versions as
            // they run). Assumed: the map is left as the already-run statements left it (spec/session.md "transactions
            // do not touch the map"); Refresh is the way back. So a retry re-inserts rolled-back inserts (they are still
            // scheduled) but does not rewrite an update that ran and was rolled back — its snapshot already advanced.
            if ($ownsTransaction && $connection->inTransaction()) {
                $connection->rollBack();
            }

            throw $failure;
        }

        $this->schedule = new UnitOfWorkSchedule();

        return new FlushSummary(count($insertOrder), count($dirty), count($deleteOrder));
    }

    /**
     * The tracked instances an update would write something for, in registration order: not scheduled
     * for removal, with at least one dirty column that is not the key, the version, or generated.
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
     * a child a persist cascade reached takes its owner's key into the FK its relationship declares.
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
            $ownerMap = $this->db->maps()->load($link['owner']::class);
            $this->copyKey($ownerMap->getKeyValues($link['owner']), $map, $link['relationship']->foreignKeyProperties, $entity);
        }
    }

    /**
     * @param list<mixed> $keyValues
     * @param list<string> $foreignKeyProperties
     */
    private function copyKey(array $keyValues, EntityMap $into, array $foreignKeyProperties, object $entity): void
    {
        // SPEC-GAP: keys flow only before an insert (spec); a dirty tracked instance's navigation is not copied before its
        // update, so a navigation that disagrees with its FK there is CRUD-004 as in an immediate update. A target whose key
        // has no value yet (an untracked, unscheduled instance) copies nothing rather than a null.
        foreach ($keyValues as $value) {
            if ($value === null) {
                return;   // a key not assigned yet (an unscheduled, untracked target): leave the FK to the insert's own checks
            }
        }

        foreach ($foreignKeyProperties as $i => $name) {
            $into->property($name)?->setValue($entity, $keyValues[$i]);
        }
    }

    // --- ordering ---------------------------------------------------------------

    /**
     * Instance dependencies of the scheduled inserts: a parent before the child that references it
     * through a non-null many-to-one navigation, an owner before the child its persist cascade reached.
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
                if (isset($inserts[spl_object_id($link['owner'])])) {
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
        // SPEC-GAP: an instance referencing itself (a node whose parent is the node) is not a "cycle among the scheduled
        // instances" the spec describes; the self edge is ignored, so it is ordered like any other instance.
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

    /**
     * The foreign-key rank of every type reachable from `$types` (unit-of-work.md "Order of writes"):
     * a type after every type its foreign keys reference — its many-to-one targets and `#[ForeignKey]`
     * references, the owners whose one-to-many/one-to-one point at it, and (for a link entity) the two
     * sides it joins. Rank is the longest dependency chain below the type; types in a metadata cycle
     * ignore the edge that closes it (the instance-level order then decides, and a true instance
     * cycle is `UOW-004`).
     *
     * @param list<class-string> $types
     * @return array<class-string, int>
     */
    private function typeRanks(array $types): array
    {
        // SPEC-GAP: "types in foreign-key order" does not say over which set of types or how unrelated types compare.
        // Assumed: the rank of a type is the length of the longest chain of types it depends on, over every type
        // reachable from the involved ones through relationships and `#[ForeignKey]` references (so a link entity ranks
        // after both sides); unrelated types of equal rank interleave by schedule order, and the edge that closes a
        // metadata cycle between types is ignored.
        /** @var array<class-string, array<class-string, true>> $dependsOn */
        $dependsOn = [];
        $seen = [];
        $queue = $types;
        while ($queue !== []) {
            $type = array_shift($queue);
            if (isset($seen[$type])) {
                continue;
            }

            $seen[$type] = true;
            $dependsOn[$type] ??= [];
            $map = $this->db->maps()->load($type);

            foreach ($map->relationships as $relationship) {
                $queue[] = $relationship->targetType;
                match ($relationship->kind) {
                    RelationshipKind::ManyToOne => self::addTypeDependency($dependsOn, $type, $relationship->targetType),
                    RelationshipKind::OneToOne, RelationshipKind::OneToMany
                        => self::addTypeDependency($dependsOn, $relationship->targetType, $type),
                    RelationshipKind::ManyToMany => $this->linkDependencies($dependsOn, $queue, $type, $relationship),
                };
            }

            foreach ($map->properties as $property) {
                if ($property->foreignKeyReferences !== null) {
                    $queue[] = $property->foreignKeyReferences;
                    self::addTypeDependency($dependsOn, $type, $property->foreignKeyReferences);
                }
            }
        }

        $ranks = [];
        foreach (array_keys($dependsOn) as $type) {
            self::depth($type, $dependsOn, $ranks, []);
        }

        return $ranks;
    }

    /**
     * @param array<class-string, array<class-string, true>> $dependsOn
     * @param list<class-string> $queue
     * @param class-string $owner
     */
    private function linkDependencies(array &$dependsOn, array &$queue, string $owner, RelationshipMap $relationship): void
    {
        if ($relationship->linkType === null) {
            return;
        }

        $queue[] = $relationship->linkType;
        self::addTypeDependency($dependsOn, $relationship->linkType, $owner);
        self::addTypeDependency($dependsOn, $relationship->linkType, $relationship->targetType);
    }

    /**
     * @param array<class-string, array<class-string, true>> $dependsOn
     * @param class-string $type
     * @param class-string $dependency
     */
    private static function addTypeDependency(array &$dependsOn, string $type, string $dependency): void
    {
        if ($type !== $dependency) {
            $dependsOn[$type][$dependency] = true;
        }
    }

    /**
     * @param array<class-string, array<class-string, true>> $dependsOn
     * @param array<class-string, int> $ranks
     * @param array<class-string, true> $stack
     */
    private static function depth(string $type, array $dependsOn, array &$ranks, array $stack): int
    {
        if (isset($ranks[$type])) {
            return $ranks[$type];
        }

        $stack[$type] = true;
        $depth = 0;
        foreach (array_keys($dependsOn[$type] ?? []) as $dependency) {
            if (!isset($stack[$dependency])) {
                $depth = max($depth, 1 + self::depth($dependency, $dependsOn, $ranks, $stack));
            }
        }

        return $ranks[$type] = $depth;
    }
}
