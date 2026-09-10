<?php

declare(strict_types=1);

namespace SimpleOrm\Mapping;

use Closure;
use SimpleOrm\Metadata\EntityMapLoader;
use SimpleOrm\Metadata\RelationshipKind;

/**
 * The unloaded-collection marker (ADR-0021 add.2, ADR-0032 ruling 2): an entity
 * **read from the database** gets its collection navigations `unset()` — the
 * foreign keys prove related rows may exist, so reading a navigation nobody
 * loaded is a bug, not an empty result. The property is then uninitialized:
 * a read throws `REL-004` through the opt-in {@see \SimpleOrm\Session\Navigations}
 * trait, or PHP's own `Error` without it — never a silent `[]`. Loading
 * (explicit, batch, or eager) re-initializes the property. Entities constructed
 * by user code keep their own initializers (a new entity genuinely has
 * nothing). Singular navigations stay null until loaded — a plain property
 * cannot throw on read without proxies, which §2 forbids; after loading, null
 * means a null FK or a dead link. Mirrors the C# `UnloadedNavigations`, whose
 * sentinel list PHP arrays cannot express.
 */
final class UnloadedNavigations
{
    /** @var array<class-string, (Closure(object): void)|null> */
    private array $markers = [];

    public function __construct(private readonly EntityMapLoader $maps)
    {
    }

    /**
     * The marker that flags a freshly materialized entity's collection navigations, or null when the type has none.
     *
     * @param class-string $type
     * @return (Closure(object): void)|null
     */
    public function markerFor(string $type): ?Closure
    {
        if (!array_key_exists($type, $this->markers)) {
            $this->markers[$type] = $this->build($type);
        }

        return $this->markers[$type];
    }

    /** @param class-string $type */
    private function build(string $type): ?Closure
    {
        $names = [];
        foreach ($this->maps->load($type)->relationships as $relationship) {
            if ($relationship->kind === RelationshipKind::OneToMany || $relationship->kind === RelationshipKind::ManyToMany) {
                $names[] = $relationship->propertyName;
            }
        }

        if ($names === []) {
            return null;
        }

        // `unset()` counts as a write, so a `private(set)` navigation refuses it
        // from outside the class; binding the closure to the entity's own scope
        // is what a library-side unset needs (the route a method of the class
        // itself would take).
        $unset = function (string ...$names): void {
            foreach ($names as $name) {
                unset($this->{$name});
            }
        };

        return static function (object $entity) use ($unset, $names, $type): void {
            Closure::bind($unset, $entity, $type)(...$names);
        };
    }
}
