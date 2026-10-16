<?php

declare(strict_types=1);

namespace SimpleOrm\Session;

use DateTimeInterface;
use ReflectionClass;
use SimpleOrm\Errors\SimpleOrmException;
use SimpleOrm\Metadata\EntityMap;
use SimpleOrm\Metadata\EntityMapLoader;
use SimpleOrm\Metadata\KeyTuple;
use SimpleOrm\Metadata\PropertyMap;
use SimpleOrm\Types\Decimal;
use UnitEnum;

use function array_key_exists;
use function array_map;
use function count;
use function in_array;
use function is_array;
use function is_object;
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
 * owned members flat); the dirty set is the value-wise difference. The tracked instance
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

    public function __construct(private readonly EntityMapLoader $maps)
    {
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
            // SPEC-GAP: "does not apply the row's values" — the row is still mapped before it is
            // discarded (the plan builds the fresh instance), so a conversion/strictness error
            // (MAP-*, VAL-*) in a re-read row still fires; the spec does not say whether it may.
            return $held->instance;
        }

        $this->store(new TrackedInstance($fresh, $map, $tuple->token(), self::snapshot($map, $fresh)));

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
        // SPEC-GAP: the order of the returned names is unspecified; this port returns
        // `EntityMap::$properties` order (the runner compares as sets).
        $entry = $this->byInstance[spl_object_id($entity)] ?? throw self::notTracked($entity);

        return array_map(
            static fn (PropertyMap $property): string => $property->propertyName(),
            $this->dirtyProperties($entry),
        );
    }

    /** @return list<PropertyMap> the entry's properties whose current value differs from its snapshot */
    public function dirtyProperties(TrackedInstance $entry): array
    {
        $dirty = [];
        foreach ($entry->map->properties as $i => $property) {
            if (!self::sameValue($entry->snapshot[$i], $property->getValue($entry->instance))) {
                $dirty[] = $property;
            }
        }

        return $dirty;
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

    /** Registers (or re-registers) an instance under its current key with a fresh snapshot — after an insert, or a full-row update of an unknown instance. A key with a null part is never tracked. */
    public function register(EntityMap $map, object $entity): void
    {
        $tuple = new KeyTuple($map->getKeyValues($entity));
        if ($tuple->hasNullPart()) {
            return;
        }

        $this->store(new TrackedInstance($entity, $map, $tuple->token(), self::snapshot($map, $entity)));
    }

    /** Resets the whole snapshot to the instance's current values (refresh). */
    public function resnapshot(TrackedInstance $entry): void
    {
        $this->store($entry->withSnapshot(self::snapshot($entry->map, $entry->instance)));
    }

    /**
     * After a column-list write: the written properties and the version (bumped in memory) move
     * forward; every other dirty value stays dirty — a write must never make an unwritten change look clean.
     *
     * @param list<PropertyMap> $written
     */
    public function resnapshotWritten(TrackedInstance $entry, array $written): void
    {
        // SPEC-GAP: the spec says `Update`/`UpdateOnly` "refresh the snapshot afterwards" without
        // saying whether that is the whole snapshot or only what was written. Whole-snapshot
        // refresh would turn an unwritten change (e.g. `UpdateOnly(['Name'])` while `Email` is
        // dirty) clean — a silently lost update — so only the written properties and the
        // version move forward here.
        $snapshot = $entry->snapshot;
        foreach ($entry->map->properties as $i => $property) {
            if ($property->version || in_array($property, $written, true)) {
                $snapshot[$i] = self::copy($property->getValue($entry->instance));
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
            // Another instance held this key (a stale one, e.g. its row was deleted outside
            // the session and the key reused): the instance just written is the truth.
            unset($this->byInstance[spl_object_id($holder->instance)]);
        }

        // SPEC-GAP: the spec does not say what an `Insert` does when its (database-generated or
        // client-assigned) key already belongs to another tracked instance after the write —
        // reachable when a row was deleted outside the session and its key reused. The
        // instance just written replaces the stale holder (no error after a committed write).
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
            // SPEC-GAP: the spec excludes "keyless entities" and "plain result types" but is silent on
            // (a) keyed views (tracked here — writes refuse CRUD-003 as ever) and (b) PHP types
            // loaded by convention or an explicit map with no mapping attributes: the mapper builds
            // those through the DTO path (§7.8 construction), so they count as plain result
            // types and are never tracked.
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

    /** @return list<mixed> */
    private static function snapshot(EntityMap $map, object $entity): array
    {
        return array_map(
            static fn (PropertyMap $property): mixed => self::copy($property->getValue($entity)),
            $map->properties,
        );
    }

    /**
     * A value the instance cannot later change under the snapshot: scalars, enums, and the immutable
     * value objects (`Decimal`, `DateTimeImmutable`) are kept as they are; arrays and any other object
     * (a JSON-handler value) are copied deeply, since a mutation inside them is still a change.
     */
    private static function copy(mixed $value): mixed
    {
        // SPEC-GAP: "a snapshot of each tracked instance's mapped values" does not say how a
        // value that is a mutable object or array (a JSON/handler-typed property) is captured.
        // Deep copy here, and structural equality in sameValue(): an in-place mutation counts as a change.
        return match (true) {
            is_array($value) => array_map(self::copy(...), $value),
            !is_object($value), $value instanceof Decimal, $value instanceof UnitEnum => $value,
            $value instanceof DateTimeInterface => clone $value,
            default => unserialize(serialize($value)),
        };
    }

    /**
     * Value-wise equality: {@see KeyTuple::valueEquals()} for scalars, `Decimal` (`10` equals `10.0`),
     * temporals (by instant), and enums; arrays and other objects compare structurally.
     */
    private static function sameValue(mixed $left, mixed $right): bool
    {
        if (is_array($left) && is_array($right)) {
            if (count($left) !== count($right)) {
                return false;
            }

            foreach ($left as $key => $value) {
                if (!array_key_exists($key, $right) || !self::sameValue($value, $right[$key])) {
                    return false;
                }
            }

            return true;
        }

        if (
            is_object($left) && is_object($right) && $left::class === $right::class
            && !$left instanceof Decimal && !$left instanceof DateTimeInterface && !$left instanceof UnitEnum
        ) {
            return self::sameValue((array) $left, (array) $right);
        }

        return KeyTuple::valueEquals($left, $right);
    }
}
