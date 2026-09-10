<?php

declare(strict_types=1);

namespace SimpleOrm\Session;

use SimpleOrm\Errors\SimpleOrmException;
use SimpleOrm\Metadata\EntityMap;
use SimpleOrm\Metadata\KeyTuple;
use SimpleOrm\Metadata\PropertyMap;
use SimpleOrm\Metadata\RelationshipKind;
use SimpleOrm\Metadata\RelationshipMap;
use SimpleOrm\Query\Criteria;
use SimpleOrm\Query\Ordering;
use SimpleOrm\Query\SelectAst;

/**
 * Explicit and batch relationship loading (Level 2 milestone 3, ADR-0021) —
 * the PHP counterpart of the C# `Db` partial in `DbLoading.cs` (PHP has no
 * partial classes, so the engine is a collaborator the session delegates to;
 * CODING-STANDARD §10). Nothing loads implicitly (§2, ADR-0019 add.1): a
 * navigation stays unloaded until one of these calls populates it, and
 * touching an unloaded navigation never fires SQL. Every call is a visible,
 * bounded set of round trips — one criteria query per navigation (two for
 * many-to-many: link rows, then targets), chunked only when a batch exceeds
 * the parameter budget.
 *
 * Key/FK tuples match by **structural equality of their values** — the §7.4
 * entity-identity rule, realized by {@see KeyTuple} — never by string tokens
 * (ADR-0021 add.1: lossy stringification silently loaded the wrong entity for
 * date and blob keys). Collection results order by the target key, compared
 * value-wise: the SQL `ORDER BY` the criteria query renders is not itself
 * enough (SQLite sorts a TEXT-affinity column — every `Decimal` key, per §7.9
 * — lexicographically, so "10" sorts before "2"), so every collection gets a
 * final client-side {@see KeyTuple::compare()} sort after fetching, the same
 * approach {@see DbEagerJoin} uses for its own joined collections (§8: one
 * ordering implementation). With an owner subquery (ADR-0022 add.1, SubSelect
 * mode) the owner set is `in (select …)` over the root query instead of a
 * client-side key list — one query per navigation, no chunking.
 */
final class DbLoading
{
    /** How many owners bind into one query before chunking (SQLite's parameter budget). */
    public const int LOAD_CHUNK_SIZE = 500;

    public function __construct(private readonly Db $db)
    {
    }

    /**
     * Loads one declared navigation for every entity in the list with one query
     * per chunk — never one per entity. Within one call, owners sharing a
     * many-to-one target share the same loaded instance. The session has
     * already resolved the navigation (`REL-001`) — a wrong name is a bug
     * regardless of how many entities happened to be in the list — and
     * returned early for an empty batch.
     *
     * @param list<object> $entities
     */
    public function loadEach(EntityMap $map, RelationshipMap $relationship, array $entities, ?SelectAst $ownerSubquery): void
    {
        $shape = NavigationShape::resolve($this->db->maps(), $map, $relationship);

        match ($relationship->kind) {
            RelationshipKind::ManyToOne => $this->loadManyToOne($map, $relationship, $shape, $entities, $ownerSubquery),
            RelationshipKind::OneToOne => $this->loadOneToOne($map, $relationship, $shape, $entities, $ownerSubquery),
            RelationshipKind::OneToMany => $this->loadOneToMany($map, $relationship, $shape, $entities, $ownerSubquery),
            RelationshipKind::ManyToMany => $this->loadManyToMany($map, $relationship, $shape, $entities, $ownerSubquery),
        };
    }

    // --- many-to-one: the single target for the owner's FK tuple, or null ------

    /**
     * @param list<object> $entities
     */
    private function loadManyToOne(EntityMap $map, RelationshipMap $relationship, NavigationShape $shape, array $entities, ?SelectAst $ownerSubquery): void
    {
        $navigationProperty = $map->navigationProperty($relationship);
        [$ownerTokens, $tuplesByToken] = self::groupOwnersByCorrelate($entities, $shape->ownerProperties);

        $targetsByToken = [];
        $rows = $this->queryCorrelated(
            $relationship->targetType,
            self::propertyNames($shape->targetProperties),
            array_values($tuplesByToken),
            [],
            $ownerSubquery,
            $ownerSubquery !== null ? $map : null,
            $ownerSubquery !== null ? $shape->ownerProperties : null,
        );
        foreach ($rows as $row) {
            $token = self::rowTuple($shape->targetProperties, $row)->token();
            $targetsByToken[$token] = $row;
        }

        foreach ($entities as $owner) {
            $token = $ownerTokens[spl_object_id($owner)] ?? null;
            $navigationProperty->setValue($owner, $token === null ? null : ($targetsByToken[$token] ?? null));
        }
    }

    // --- one-to-one: the single target whose FK equals the owner's key, or null; REL-002 on drift ---

    /**
     * @param list<object> $entities
     */
    private function loadOneToOne(EntityMap $map, RelationshipMap $relationship, NavigationShape $shape, array $entities, ?SelectAst $ownerSubquery): void
    {
        $navTarget = $map->navigationTarget($relationship);
        $navigationProperty = $map->navigationProperty($relationship);
        [$ownerTokens, $tuplesByToken] = self::groupOwnersByCorrelate($entities, $shape->ownerProperties);

        $rows = $this->queryCorrelated(
            $relationship->targetType,
            self::propertyNames($shape->targetProperties),
            array_values($tuplesByToken),
            [],
            $ownerSubquery,
            $ownerSubquery !== null ? $map : null,
            $ownerSubquery !== null ? $shape->ownerProperties : null,
        );

        $byToken = [];
        foreach ($rows as $row) {
            $token = self::rowTuple($shape->targetProperties, $row)->token();
            if (isset($byToken[$token])) {
                throw new SimpleOrmException(
                    'REL-002',
                    $navTarget,
                    "more than one {$shape->targetMap->entityName()} row references the same owner; "
                        . 'the target foreign key needs a unique index',
                );
            }

            $byToken[$token] = $row;
        }

        foreach ($entities as $owner) {
            $token = $ownerTokens[spl_object_id($owner)] ?? null;
            $navigationProperty->setValue($owner, $token === null ? null : ($byToken[$token] ?? null));
        }
    }

    // --- one-to-many: a fresh list of the targets whose FK equals the owner's key, ordered by target key ---

    /**
     * @param list<object> $entities
     */
    private function loadOneToMany(EntityMap $map, RelationshipMap $relationship, NavigationShape $shape, array $entities, ?SelectAst $ownerSubquery): void
    {
        $navigationProperty = $map->navigationProperty($relationship);
        [$ownerTokens, $tuplesByToken] = self::groupOwnersByCorrelate($entities, $shape->ownerProperties);

        $rows = $this->queryCorrelated(
            $relationship->targetType,
            self::propertyNames($shape->targetProperties),
            array_values($tuplesByToken),
            self::keyOrderings($shape->targetMap),
            $ownerSubquery,
            $ownerSubquery !== null ? $map : null,
            $ownerSubquery !== null ? $shape->ownerProperties : null,
        );

        $byToken = [];
        foreach ($rows as $row) {
            $token = self::rowTuple($shape->targetProperties, $row)->token();
            $byToken[$token][] = $row;
        }

        foreach ($entities as $owner) {
            $token = $ownerTokens[spl_object_id($owner)] ?? null;
            $collection = $token === null ? [] : ($byToken[$token] ?? []);
            $navigationProperty->setValue($owner, $shape->targetMap->sortByKey($collection));
        }
    }

    // --- many-to-many: the targets referenced by the declared link's rows for this owner ---

    /**
     * @param list<object> $entities
     */
    private function loadManyToMany(EntityMap $map, RelationshipMap $relationship, NavigationShape $shape, array $entities, ?SelectAst $ownerSubquery): void
    {
        $navigationProperty = $map->navigationProperty($relationship);
        [$ownerTokens, $tuplesByToken] = self::groupOwnersByCorrelate($entities, $shape->ownerProperties);

        // Hop 1 (visible query 1 of 2): link rows for the owners in this batch —
        // an owner subquery replaces the key list in SubSelect mode.
        $linkRows = $this->queryCorrelated(
            (string) $relationship->linkType,
            self::propertyNames($shape->linkToOwner),
            array_values($tuplesByToken),
            [],
            $ownerSubquery,
            $ownerSubquery !== null ? $map : null,
            $ownerSubquery !== null ? $shape->ownerProperties : null,
        );

        /** @var array<string, array<string, true>> $ownerToTargetTokens owner token -> set of target tokens */
        $ownerToTargetTokens = [];
        /** @var array<string, KeyTuple> $targetTuplesByToken distinct target tuples to fetch in hop 2 */
        $targetTuplesByToken = [];
        foreach ($linkRows as $linkRow) {
            $ownerToken = self::rowTuple($shape->linkToOwner, $linkRow)->token();
            $targetTuple = self::rowTuple($shape->linkToTarget, $linkRow);
            if ($targetTuple->hasNullPart()) {
                continue;   // a link row with no real target FK correlates to nothing
            }

            $targetToken = $targetTuple->token();
            $targetTuplesByToken[$targetToken] ??= $targetTuple;
            $ownerToTargetTokens[$ownerToken][$targetToken] = true;
        }

        // Hop 2 (visible query 2 of 2): targets by key — always a key list, in
        // every fetch mode (spec/loading.md "Clarifications": "the many-to-many
        // link→target hop is an ordinary key list: it chunks like any other, in
        // every mode").
        $targetRows = $this->queryCorrelated(
            $relationship->targetType,
            self::propertyNames($shape->targetMap->keyProperties),
            array_values($targetTuplesByToken),
            self::keyOrderings($shape->targetMap),
            null,
        );
        $targetRows = $shape->targetMap->sortByKey($targetRows);

        $orderedTargetTokens = [];
        $targetsByToken = [];
        foreach ($targetRows as $row) {
            $token = self::rowTuple($shape->targetMap->keyProperties, $row)->token();
            $orderedTargetTokens[] = $token;
            $targetsByToken[$token] = $row;
        }

        foreach ($entities as $owner) {
            $ownerToken = $ownerTokens[spl_object_id($owner)] ?? null;
            $wanted = $ownerToken !== null ? ($ownerToTargetTokens[$ownerToken] ?? []) : [];
            $collection = [];
            foreach ($orderedTargetTokens as $token) {
                if (isset($wanted[$token])) {
                    $collection[] = $targetsByToken[$token];
                }
            }

            $navigationProperty->setValue($owner, $collection);
        }
    }

    // --- shared plumbing ---------------------------------------------------------

    /**
     * Executes one or more criteria queries selecting `$entityType` rows whose
     * `$predicateProperties` (in order) match the distinct correlate `$tuples` —
     * chunked at {@see LOAD_CHUNK_SIZE} tuples per query — or, with
     * `$ownerSubquery`, one un-chunked `in (select …)` query correlating through
     * `$ownerMap`'s `$ownerProjection` properties (ADR-0022 add.1, SubSelect).
     * Every query goes through {@see Db::executeAst()}, the one execution path.
     *
     * @param list<string> $predicateProperties property names on `$entityType`
     * @param list<KeyTuple> $tuples distinct correlate tuples (ignored in subquery mode)
     * @param list<Ordering> $orderings
     * @param list<PropertyMap>|null $ownerProjection subquery-mode only: the owner properties to project, in order
     * @return list<object>
     */
    private function queryCorrelated(
        string $entityType,
        array $predicateProperties,
        array $tuples,
        array $orderings,
        ?SelectAst $ownerSubquery,
        ?EntityMap $ownerMap = null,
        ?array $ownerProjection = null,
    ): array {
        $map = $this->db->maps()->load($entityType);

        if ($ownerSubquery !== null) {
            $subquery = new SelectAst(
                $ownerMap,
                $ownerSubquery->where,
                $ownerSubquery->orderings,
                $ownerSubquery->limit,
                $ownerSubquery->offset,
                $ownerProjection,
            );
            $predicate = Criteria::inSelect($predicateProperties, $subquery);

            return $this->db->executeAst(new SelectAst($map, [$predicate], $orderings), $entityType);
        }

        if ($tuples === []) {
            return [];
        }

        // Chunking counts distinct complete correlate tuples, not owners
        // (spec/loading.md Clarifications, ADR-0033): never a larger query.
        $results = [];
        foreach (array_chunk($tuples, self::LOAD_CHUNK_SIZE) as $chunk) {
            $predicate = self::membershipPredicate($predicateProperties, $chunk);
            $ast = new SelectAst($map, [$predicate], $orderings);
            array_push($results, ...$this->db->executeAst($ast, $entityType));
        }

        return $results;
    }

    /**
     * One key part: `Criteria::in`. Composite: an OR of ANDed equalities, parts
     * in key order (spec/loading.md: "FK tuples compare as OR-ed groups of
     * ANDed equalities, in key order").
     *
     * @param list<string> $propertyNames
     * @param list<KeyTuple> $tuples
     */
    private static function membershipPredicate(array $propertyNames, array $tuples): Criteria
    {
        if (count($propertyNames) === 1) {
            return Criteria::in($propertyNames[0], array_map(
                static fn (KeyTuple $tuple): mixed => $tuple->values[0],
                $tuples,
            ));
        }

        $ors = [];
        foreach ($tuples as $tuple) {
            $ands = [];
            foreach ($propertyNames as $i => $name) {
                $ands[] = Criteria::eq($name, $tuple->values[$i]);
            }

            $ors[] = Criteria::and(...$ands);
        }

        return Criteria::or(...$ors);
    }

    /**
     * Groups owners by their correlate tuple (owner key or FK, per caller) —
     * an owner with a null part is excluded from querying (the null-FK / null-key
     * symmetric rule, spec/loading.md) and left out of both maps.
     *
     * @param list<object> $entities
     * @param list<PropertyMap> $correlateProperties
     * @return array{0: array<int, string>, 1: array<string, KeyTuple>} owner (spl_object_id) -> token; distinct tuples by token
     */
    private static function groupOwnersByCorrelate(array $entities, array $correlateProperties): array
    {
        $ownerTokens = [];
        $tuplesByToken = [];
        foreach ($entities as $owner) {
            $tuple = self::rowTuple($correlateProperties, $owner);
            if ($tuple->hasNullPart()) {
                continue;
            }

            $token = $tuple->token();
            $ownerTokens[spl_object_id($owner)] = $token;
            $tuplesByToken[$token] ??= $tuple;
        }

        return [$ownerTokens, $tuplesByToken];
    }

    /** @param list<PropertyMap> $properties */
    private static function rowTuple(array $properties, object $entity): KeyTuple
    {
        return new KeyTuple(array_map(static fn (PropertyMap $p): mixed => $p->getValue($entity), $properties));
    }

    /** @param list<PropertyMap> $properties @return list<string> */
    private static function propertyNames(array $properties): array
    {
        return array_map(static fn (PropertyMap $p): string => $p->propertyName(), $properties);
    }

    /**
     * @return list<Ordering> ascending, in key order — a SQL-side ordering hint
     * kept for its usual determinism/plan benefit; {@see EntityMap::sortByKey()}
     * is what actually guarantees the spec's value-wise order (a `Decimal` key
     * stores as TEXT, §7.9, and SQLite's `ORDER BY` on a TEXT-affinity column
     * is lexicographic, not numeric — "10" would render before "2").
     */
    private static function keyOrderings(EntityMap $map): array
    {
        return array_map(static fn (PropertyMap $p): Ordering => new Ordering($p->propertyName()), $map->keyProperties);
    }
}
