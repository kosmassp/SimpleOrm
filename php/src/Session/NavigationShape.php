<?php

declare(strict_types=1);

namespace SimpleOrm\Session;

use SimpleOrm\Errors\SimpleOrmException;
use SimpleOrm\Metadata\EntityMap;
use SimpleOrm\Metadata\EntityMapLoader;
use SimpleOrm\Metadata\PropertyMap;
use SimpleOrm\Metadata\RelationshipKind;
use SimpleOrm\Metadata\RelationshipMap;

/**
 * Resolves and validates a declared navigation's target/link maps and
 * correlating property lists (`REL-003`) — the **one** resolver for both
 * relationship-loading engines (CODING-STANDARD §8): {@see DbLoading}
 * (explicit/batch/MultiQuery/SubSelect) and {@see DbEagerJoin} (join mode)
 * previously re-derived this per kind, and only `DbLoading` validated FK
 * property names and arity (spec/loading.md "Shape errors") — join mode built
 * its ON pairs straight from the declaration, so a malformed navigation
 * refused as `QRY-006` (an unknown renderer property) there instead of
 * `REL-003`, in front of the spec's "Refusal precedence" ruling that shape
 * problems are always `REL-003`, in every mode. Both engines now build their
 * correlates through this class, so they refuse identically.
 *
 * @internal a collaborator of {@see DbLoading} and {@see DbEagerJoin}, not part of the public API
 */
final readonly class NavigationShape
{
    /**
     * @param list<PropertyMap> $ownerProperties the owner-side correlate, in
     *        order: the owner's FK columns (many-to-one) or its key
     *        (one-to-one/one-to-many/many-to-many)
     * @param list<PropertyMap> $targetProperties the target-side correlate, in
     *        the same order: the target's key (many-to-one) or its FK columns
     *        (one-to-one/one-to-many); empty for many-to-many (`$linkMap` and
     *        the two link lists carry it instead)
     * @param list<PropertyMap> $linkToOwner many-to-many only, in declaration order
     * @param list<PropertyMap> $linkToTarget many-to-many only, in declaration order
     */
    private function __construct(
        public EntityMap $targetMap,
        public array $ownerProperties,
        public array $targetProperties,
        public ?EntityMap $linkMap = null,
        public array $linkToOwner = [],
        public array $linkToTarget = [],
    ) {
    }

    public static function resolve(EntityMapLoader $maps, EntityMap $map, RelationshipMap $relationship): self
    {
        $targetMap = $maps->load($relationship->targetType);
        $navTarget = $map->navigationTarget($relationship);

        return match ($relationship->kind) {
            RelationshipKind::ManyToOne => self::resolveManyToOne($map, $targetMap, $relationship, $navTarget),
            RelationshipKind::OneToOne, RelationshipKind::OneToMany
                => self::resolveToTarget($map, $targetMap, $relationship, $navTarget),
            RelationshipKind::ManyToMany => self::resolveManyToMany($maps, $map, $targetMap, $relationship, $navTarget),
        };
    }

    // REL-003 applies to every kind at load time, many-to-one included
    // (spec/loading.md Clarifications, ADR-0033): declaration-time validation
    // proves a target/link FK property *exists* (a raw reflection check, never
    // force-loading that type's map), not that it is *mapped* — an `#[Ignore]`d
    // property passes declaration and refuses here; for many-to-one against a
    // keyed owner the check is a backstop of `MAP-016`.
    private static function resolveManyToOne(EntityMap $map, EntityMap $targetMap, RelationshipMap $relationship, string $navTarget): self
    {
        $fk = self::resolveProperties($map, $relationship->foreignKeyProperties, $navTarget, 'foreign-key');
        self::checkArity(count($fk), count($targetMap->keyProperties), $navTarget, 'foreign-key count vs. target key arity');

        return new self($targetMap, $fk, $targetMap->keyProperties);
    }

    /** Shared shape for one-to-one and one-to-many: the FK lives on the target, correlating to the owner's key. */
    private static function resolveToTarget(EntityMap $map, EntityMap $targetMap, RelationshipMap $relationship, string $navTarget): self
    {
        $targetFk = self::resolveProperties($targetMap, $relationship->foreignKeyProperties, $navTarget, 'target foreign-key');
        self::checkArity(count($targetFk), count($map->keyProperties), $navTarget, 'target foreign-key count vs. owner key arity');

        return new self($targetMap, $map->keyProperties, $targetFk);
    }

    private static function resolveManyToMany(
        EntityMapLoader $maps,
        EntityMap $map,
        EntityMap $targetMap,
        RelationshipMap $relationship,
        string $navTarget,
    ): self {
        $linkType = $relationship->linkType
            ?? throw new SimpleOrmException('REL-003', $navTarget, 'a many-to-many navigation needs a declared link entity');
        $linkMap = $maps->load($linkType);

        $linkToOwner = self::resolveProperties($linkMap, $relationship->linkForeignKeysToOwner, $navTarget, 'link foreign-key (owner side)');
        self::checkArity(count($linkToOwner), count($map->keyProperties), $navTarget, 'link foreign-key (owner side) count vs. owner key arity');
        $linkToTarget = self::resolveProperties($linkMap, $relationship->linkForeignKeysToTarget, $navTarget, 'link foreign-key (target side)');
        self::checkArity(count($linkToTarget), count($targetMap->keyProperties), $navTarget, 'link foreign-key (target side) count vs. target key arity');

        return new self($targetMap, $map->keyProperties, [], $linkMap, $linkToOwner, $linkToTarget);
    }

    /**
     * Resolves declared FK/link property names to mapped columns on `$on`
     * (`REL-003` when a name is not a mapped property — a shape the loader
     * could not validate at declaration time, spec/loading.md "Shape errors").
     *
     * @param list<string> $names
     * @return list<PropertyMap>
     */
    private static function resolveProperties(EntityMap $on, array $names, string $navTarget, string $what): array
    {
        $resolved = [];
        foreach ($names as $name) {
            $property = $on->property($name);
            if ($property === null) {
                throw new SimpleOrmException(
                    'REL-003',
                    $navTarget,
                    "{$what} property '{$name}' is not a mapped column of {$on->entityName()}",
                );
            }

            $resolved[] = $property;
        }

        return $resolved;
    }

    /** `REL-003`: an FK/link declaration whose arity disagrees with the key it correlates to, or a keyless owner/target (`$expected === 0`). */
    private static function checkArity(int $actual, int $expected, string $navTarget, string $what): void
    {
        if ($expected === 0 || $actual !== $expected) {
            throw new SimpleOrmException(
                'REL-003',
                $navTarget,
                "{$what}: expected {$expected} key part(s), found {$actual}",
            );
        }
    }
}
