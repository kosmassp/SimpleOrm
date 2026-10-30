<?php

declare(strict_types=1);

namespace SimpleOrm\Session;

use SimpleOrm\Metadata\EntityMap;
use SimpleOrm\Metadata\PropertyMap;
use WeakMap;

/**
 * The names and error labels the session derives from one entity map, built once (ADR-0048 item 3) instead of being
 * interpolated at every bind site: a property's bind label (`Entity.property`), a key part's (`Entity key[i]`), the
 * statement log's and the plan's names (`Entity.insert`, `Entity criteria`, `Entity get`), and the criteria parameter
 * labels (`Entity criteria @c0`). Every string is the one the call sites built before.
 *
 * Keyed by identity — a `WeakMap` on the {@see EntityMap} instance, a property by its object id within that map (the map
 * holds its properties, so the id is stable for the entry's lifetime) — never by a name that could collide across models
 * (CODING-STANDARD §10).
 *
 * @internal the session's cache, not public API
 */
final class MapLabels
{
    /** @var WeakMap<EntityMap, self>|null */
    private static ?WeakMap $cache = null;

    /** `Entity criteria`: the name criteria queries, includes, and their plans report (§7.11). */
    public readonly string $criteria;

    /** @var array<int, string> bind labels by the property's object id */
    private array $properties = [];

    /** @var array<int, string> */
    private array $keys = [];

    /** @var array<string, string> */
    private array $names = [];

    /** @var array<int, string> criteria parameter labels by index */
    private array $criteriaParameters = [];

    private function __construct(public readonly string $entity)
    {
        $this->criteria = $entity . ' criteria';
    }

    public static function for(EntityMap $map): self
    {
        self::$cache ??= new WeakMap();

        return self::$cache[$map] ??= new self($map->entityName());
    }

    /** `Entity.property` — the label a bound property value reports in a conversion error. */
    public function property(PropertyMap $property): string
    {
        return $this->properties[spl_object_id($property)] ??= $this->entity . '.' . $property->propertyName();
    }

    /** `Entity key[i]` — the label a key part given by the caller reports. */
    public function key(int $index): string
    {
        return $this->keys[$index] ??= "{$this->entity} key[{$index}]";
    }

    /** `Entity.<operation>` (`Entity.insert`, `Entity.get`, …) — the statement log's name. */
    public function name(string $operation): string
    {
        return $this->names[$operation] ??= "{$this->entity}.{$operation}";
    }

    /** `Entity <suffix>` (`Entity get`, `Entity refresh`, `Entity select-all`) — the name a plan reports. */
    public function plan(string $suffix): string
    {
        return $this->names[' ' . $suffix] ??= "{$this->entity} {$suffix}";
    }

    /** `Entity criteria @c<i>` — the label the i-th criteria parameter reports. */
    public function criteriaParameter(int $index): string
    {
        return $this->criteriaParameters[$index] ??= "{$this->criteria} @c{$index}";
    }
}
