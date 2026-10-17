<?php

declare(strict_types=1);

namespace SimpleOrm\Session;

use ReflectionClass;
use SimpleOrm\Errors\SimpleOrmException;
use SimpleOrm\Mapping\TypeConverter;
use SimpleOrm\Metadata\AuditRole;
use SimpleOrm\Metadata\EntityMap;
use SimpleOrm\Metadata\EntityMapLoader;
use SimpleOrm\Metadata\KeyTuple;
use SimpleOrm\Metadata\PropertyMap;
use SimpleOrm\Types\Decimal;

use function array_key_exists;
use function array_map;
use function in_array;
use function spl_object_id;

/**
 * The tracked session's identity map and change snapshots (Level 3 M1, ADR-0034 add.1,
 * spec/session.md "Tracking") — a collaborator beside {@see DbLoading}/{@see DbEagerJoin}
 * (CODING-STANDARD §10), owned by {@see Db} only when the session opened with
 * {@see TrackingMode::Tracked}.
 *
 * One instance per (entity class, key), the key compared **value-wise** through
 * {@see KeyTuple} — the one identity implementation (CODING-STANDARD §8): its `token()`
 * buckets the map, so a `Decimal` key `10` and `10.0` are one entry. Each entry carries a
 * snapshot of the instance's mapped values (every {@see EntityMap::$properties} entry,
 * owned members flat) in their **database-bound form** — {@see TypeConverter::toDatabase()}'s
 * output (spec/session.md Clarifications, ADR-0035) — so the dirty set is "the database would
 * receive a different value": a handler/JSON member compares by what would be written, an
 * in-place mutation of it counts, and equal decimals are clean whatever their text. The tracked instance
 * **wins** (ruling 2): {@see adopt()} hands back the held instance for a known key and the
 * row's values are never applied — only {@see Db::refresh()} overwrites.
 *
 * Plain DTO results, keyless entities, and rows whose key has a null part are never
 * tracked. Nothing here touches the database: tracking decides which instance a read returns
 * and which columns an update writes, never whether a round trip happens.
 *
 * @internal
 */
final class DbTracking
{
    /** @var array<class-string, array<string, TrackedInstance>> entity class → key token → entry */
    private array $byKey = [];

    /** @var array<int, TrackedInstance> spl_object_id → entry; holding the entry keeps the id from being reused */
    private array $byInstance = [];

    /** @var array<string, EntityMap|false> result type → its map when tracked, `false` when the type is never tracked */
    private array $trackable = [];

    public function __construct(
        private readonly EntityMapLoader $maps,
        private readonly TypeConverter $converter,
    ) {
    }

    // --- reads ------------------------------------------------------------------

    /**
     * Wraps a mapping plan so every entity it materializes passes through the identity map;
     * the plan itself is returned untouched for results that are never tracked (DTOs,
     * scalars, keyless entities).
     *
     * @param callable(array<string, mixed>): mixed $plan
     * @return callable(array<string, mixed>): mixed
     */
    public function adopting(callable $plan, string $resultType): callable
    {
        $map = $this->trackableMap($resultType);
        if ($map === null) {
            return $plan;
        }

        return fn (array $row): mixed => $this->adopt($map, $plan($row));
    }

    /** The held instance for the fresh one's key, or the fresh instance — registered with its snapshot — when the key is new. */
    public function adopt(EntityMap $map, object $fresh): object
    {
        $tuple = new KeyTuple($map->getKeyValues($fresh));
        if ($tuple->hasNullPart()) {
            return $fresh;
        }

        $held = $this->byKey[$map->entityType][$tuple->token()] ?? null;
        if ($held !== null) {
            // The plan already built the fresh instance, so a conversion/strictness error in a
            // re-read row fired before this point; only the values are discarded (spec/session.md
            // Clarifications, ADR-0035: "a re-read of a tracked key still maps the row").
            return $held->instance;
        }

        $this->store(new TrackedInstance($fresh, $map, $tuple->token(), $this->snapshot($map, $fresh)));

        return $fresh;
    }

    // --- questions --------------------------------------------------------------

    public function isTracked(object $entity): bool
    {
        return array_key_exists(spl_object_id($entity), $this->byInstance);
    }

    /**
     * The mapped property names whose value differs from the snapshot, dotted for owned
     * members, in `EntityMap::$properties` order. `UOW-001` for an instance the session does not track.
     *
     * @return list<string>
     */
    public function changesOf(object $entity): array
    {
        // Metadata order (spec/session.md Clarifications, ADR-0035).
        $entry = $this->byInstance[spl_object_id($entity)] ?? throw self::notTracked($entity);

        return array_map(
            static fn (PropertyMap $property): string => $property->propertyName(),
            $this->dirtyProperties($entry),
        );
    }

    /** @return list<PropertyMap> the entry's properties whose database-bound value differs from its snapshot */
    public function dirtyProperties(TrackedInstance $entry): array
    {
        $dirty = [];
        foreach ($entry->map->properties as $i => $property) {
            $current = $this->bound($entry->map, $property, $entry->instance);
            if (!KeyTuple::valueEquals($entry->snapshot[$i], $current)) {
                $dirty[] = $property;
            }
        }

        return $dirty;
    }

    /**
     * The dirty properties a column-list update may write — never a key, the version, a
     * database-generated column (those are never in a SET), or the `#[UpdatedAt]` stamp: the library writes
     * that one itself and it never makes an instance dirty on its own (spec/hooks.md). The one filter both the tracked
     * `Db::update()` and the unit of work's flush use (CODING-STANDARD §8), so "dirty" means the
     * same thing to both.
     *
     * @return list<PropertyMap>
     */
    public function writableDirtyProperties(TrackedInstance $entry): array
    {
        return array_values(array_filter(
            $this->dirtyProperties($entry),
            static fn (PropertyMap $p): bool => !$p->key && !$p->version && !$p->generated && $p->audit !== AuditRole::UpdatedAt,
        ));
    }

    /**
     * The session's instance for a key, or null — what `Db::merge()` copies onto. The key compares value-wise
     * ({@see KeyTuple}); a key with a null part is never held.
     *
     * @param list<mixed> $keyValues
     */
    public function heldInstance(EntityMap $map, array $keyValues): ?object
    {
        $tuple = new KeyTuple($keyValues);

        return $tuple->hasNullPart() ? null : ($this->byKey[$map->entityType][$tuple->token()]->instance ?? null);
    }

    /**
     * Forgets exactly this instance (detach): its entry only — never whichever instance now holds a key it once had.
     * False when the session did not track it.
     */
    public function detach(object $entity): bool
    {
        // By instance, never by key (spec/unit-of-work.md "Clarifications on detach and merge"): an instance whose key was
        // changed after it was tracked is still forgotten (under the key it was registered with), and an instance that only
        // shares a key with a tracked one is not this one — UOW-001 for the caller.
        $entry = $this->byInstance[spl_object_id($entity)] ?? null;
        if ($entry === null) {
            return false;
        }

        $this->remove($entry);

        return true;
    }

    /**
     * Every identity-map entry in registration order (an instance keeps its position when it is
     * re-snapshotted; a re-registered key or a newly read instance goes last) — the order the flush
     * writes dirty instances in within one type (unit-of-work.md "Order of writes").
     *
     * @return list<TrackedInstance>
     */
    public function entries(): array
    {
        return array_values($this->byInstance);
    }

    /** The `UOW-001` refusal for an instance this tracked session does not hold. */
    public static function notTracked(object $entity): SimpleOrmException
    {
        return new SimpleOrmException(
            'UOW-001',
            (new ReflectionClass($entity))->getShortName(),
            'the session does not track this instance: it was not read, inserted, or updated through this session (or was deleted)',
        );
    }

    /** The `UOW-001` refusal in an untracked session: every instance is untracked, so no tracking question has an answer. */
    public static function untrackedSession(object $entity): SimpleOrmException
    {
        return new SimpleOrmException(
            'UOW-001',
            (new ReflectionClass($entity))->getShortName(),
            'the session is untracked: open it with TrackingMode::Tracked to ask what changed',
        );
    }

    // --- writes -----------------------------------------------------------------

    /**
     * The write gate (`UOW-002`, before any SQL): the instance must be the session's instance for
     * its key. Returns its entry when tracked, null for an instance the session does not know
     * whose key is free; throws when another instance holds the key or a tracked instance's key
     * was changed (a key is identity and never updates).
     */
    public function identityFor(EntityMap $map, object $entity): ?TrackedInstance
    {
        $tuple = new KeyTuple($map->getKeyValues($entity));
        $entry = $this->byInstance[spl_object_id($entity)] ?? null;
        if ($entry !== null) {
            if ($entry->token !== $tuple->token()) {
                throw new SimpleOrmException(
                    'UOW-002',
                    $map->entityName(),
                    'the key of a tracked instance was changed; a key is identity and never updates — delete and insert instead',
                );
            }

            return $entry;
        }

        if (!$tuple->hasNullPart() && array_key_exists($tuple->token(), $this->byKey[$map->entityType] ?? [])) {
            throw new SimpleOrmException(
                'UOW-002',
                $map->entityName(),
                'another instance is the session\'s instance for this key; write through that instance (or refresh it) instead of a second copy',
            );
        }

        return null;
    }

    /**
     * Registers (or re-registers) an instance under its current key with a fresh snapshot — after an insert, or a
     * full-row update of an unknown instance. A key with a null part is never tracked. A re-registration takes a
     * new position in the registration order (spec/unit-of-work.md Clarifications, ADR-0036: latest registration);
     * a re-snapshot keeps it.
     */
    public function register(EntityMap $map, object $entity): void
    {
        $tuple = new KeyTuple($map->getKeyValues($entity));
        if ($tuple->hasNullPart()) {
            return;
        }

        $previous = $this->byInstance[spl_object_id($entity)] ?? null;
        if ($previous !== null) {
            $this->remove($previous);
        }

        $this->store(new TrackedInstance($entity, $map, $tuple->token(), $this->snapshot($map, $entity)));
    }

    /** Resets the whole snapshot to the instance's current values (refresh). */
    public function resnapshot(TrackedInstance $entry): void
    {
        $this->store($entry->withSnapshot($this->snapshot($entry->map, $entry->instance)));
    }

    /**
     * After a column-list write: the written properties and the version (bumped in memory) move
     * forward; every other dirty value stays dirty — a write must never make an unwritten change look clean.
     *
     * @param list<PropertyMap> $written
     */
    public function resnapshotWritten(TrackedInstance $entry, array $written): void
    {
        // spec/session.md Clarifications (ADR-0035): a column-list update moves the written columns
        // and the version only — an unwritten change stays dirty.
        $snapshot = $entry->snapshot;
        foreach ($entry->map->properties as $i => $property) {
            if ($property->version || in_array($property, $written, true)) {
                $snapshot[$i] = $this->bound($entry->map, $property, $entry->instance);
            }
        }

        $this->store($entry->withSnapshot($snapshot));
    }

    /**
     * A delete forgets the key (and the entity instance, when one was given): neither is tracked afterwards.
     *
     * @param list<mixed> $keyValues
     */
    public function forget(EntityMap $map, array $keyValues, ?object $entity): void
    {
        $tuple = new KeyTuple($keyValues);
        if (!$tuple->hasNullPart()) {
            $held = $this->byKey[$map->entityType][$tuple->token()] ?? null;
            if ($held !== null) {
                $this->remove($held);
            }
        }

        if ($entity !== null) {
            $entry = $this->byInstance[spl_object_id($entity)] ?? null;
            if ($entry !== null) {
                $this->remove($entry);
            }
        }
    }

    /**
     * A restore point for a failed flush (spec/unit-of-work.md Clarifications, ADR-0036): the identity map and
     * the snapshots as they are now. Entries are immutable and arrays copy on write, so holding the two maps
     * is the whole undo of what the flush's statements register, re-snapshot, and forget.
     *
     * @return array{0: array<class-string, array<string, TrackedInstance>>, 1: array<int, TrackedInstance>}
     */
    public function checkpoint(): array
    {
        return [$this->byKey, $this->byInstance];
    }

    /** @param array{0: array<class-string, array<string, TrackedInstance>>, 1: array<int, TrackedInstance>} $checkpoint */
    public function rollbackTo(array $checkpoint): void
    {
        [$this->byKey, $this->byInstance] = $checkpoint;
    }

    /** Drops everything: the session closed. */
    public function clear(): void
    {
        $this->byKey = [];
        $this->byInstance = [];
    }

    // --- internals ----------------------------------------------------------------

    private function store(TrackedInstance $entry): void
    {
        $type = $entry->map->entityType;

        $holder = $this->byKey[$type][$entry->token] ?? null;
        if ($holder !== null && $holder->instance !== $entry->instance) {
            // A key reused after an external delete: the instance that wrote the row is the
            // session's instance, the stale holder is forgotten (spec/session.md Clarifications, ADR-0035).
            unset($this->byInstance[spl_object_id($holder->instance)]);
        }

        $previous = $this->byInstance[spl_object_id($entry->instance)] ?? null;
        if ($previous !== null && $previous->token !== $entry->token) {
            unset($this->byKey[$type][$previous->token]);
        }

        $this->byKey[$type][$entry->token] = $entry;
        $this->byInstance[spl_object_id($entry->instance)] = $entry;
    }

    private function remove(TrackedInstance $entry): void
    {
        unset($this->byKey[$entry->map->entityType][$entry->token], $this->byInstance[spl_object_id($entry->instance)]);
    }

    /** The map of a type the session tracks — a mapped entity with a key — or null (DTOs, scalars, keyless entities). */
    private function trackableMap(string $resultType): ?EntityMap
    {
        if (!array_key_exists($resultType, $this->trackable)) {
            // Keyed views are tracked (writes still refuse CRUD-003); types with no mapping
            // attributes are built through the DTO path (§7.8) and are plain result types
            // (spec/session.md Clarifications, ADR-0035: "which types are tracked").
            $this->trackable[$resultType] = class_exists($resultType) && EntityMapLoader::hasMappingAttributes($resultType)
                ? self::keyedOrFalse($this->maps->load($resultType))
                : false;
        }

        return $this->trackable[$resultType] ?: null;
    }

    private static function keyedOrFalse(EntityMap $map): EntityMap|false
    {
        return $map->keyProperties === [] ? false : $map;
    }

    /** @return list<mixed> one database-bound value per `$map->properties` entry, in that order */
    private function snapshot(EntityMap $map, object $entity): array
    {
        return array_map(fn (PropertyMap $property): mixed => $this->bound($map, $property, $entity), $map->properties);
    }

    /**
     * The value the database would receive for the property — {@see TypeConverter::toDatabase()}, the
     * one conversion every write binds through (spec/session.md Clarifications, ADR-0035). Strings
     * (bytes, JSON text) are immutable, so the snapshot cannot alias the instance. A `Decimal` the
     * converter rendered as its own text stays the `Decimal`, so two decimals equal in value are
     * clean whatever their text; compared through {@see KeyTuple::valueEquals()} (temporals by instant).
     */
    private function bound(EntityMap $map, PropertyMap $property, object $entity): mixed
    {
        $value = $property->getValue($entity);
        $bound = $this->converter->toDatabase(
            $value,
            "{$map->entityName()}.{$property->propertyName()}",
            $property->enumAsInt(),
            $property->type,
        );

        return $value instanceof Decimal && $bound === (string) $value ? $value : $bound;
    }
}
