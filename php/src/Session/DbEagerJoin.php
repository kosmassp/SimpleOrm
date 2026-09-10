<?php

declare(strict_types=1);

namespace SimpleOrm\Session;

use PDO;
use PDOStatement;
use ReflectionProperty;
use SimpleOrm\Errors\SimpleOrmException;
use SimpleOrm\Mapping\RowSegment;
use SimpleOrm\Metadata\EntityMap;
use SimpleOrm\Metadata\KeyTuple;
use SimpleOrm\Metadata\PropertyMap;
use SimpleOrm\Metadata\RelationshipKind;
use SimpleOrm\Metadata\RelationshipMap;
use SimpleOrm\Query\JoinPair;
use SimpleOrm\Query\SelectAst;
use SimpleOrm\Query\SelectJoin;

use function array_key_exists;
use function array_map;
use function in_array;

/**
 * Join-mode eager loading (ADR-0022 add.1) — the PHP counterpart of the C# `Db`
 * partial in `DbEagerJoin.cs` (a collaborator here, CODING-STANDARD §10): one
 * SELECT with a LEFT JOIN per included navigation (a many-to-many joins its
 * link unprojected, then the target), the row partitioned into per-alias
 * segments that reuse the one mapping pipeline (§7.11, {@see RowSegment} +
 * {@see \SimpleOrm\Mapping\ResultMapper}), roots deduplicated by key in
 * first-appearance order, children attached by structural key equality
 * ({@see KeyTuple} — the one identity/ordering implementation this shares
 * with {@see DbLoading}, CODING-STANDARD §8). Refuses paging with a collection
 * include (`REL-005`), more than one collection include (`REL-006`), and a
 * navigation whose shape is unresolvable (`REL-003`: a keyless root/target to
 * deduplicate against, or — via {@see NavigationShape}, the same resolver
 * {@see DbLoading} uses — an unmapped FK/link property or an arity mismatch)
 * — never in-memory paging or a silent Cartesian product.
 *
 * Refusal precedence (spec/loading.md Clarifications, ADR-0031/0033): `REL-001`
 * (the caller), then `REL-006`, then `REL-005`, then `REL-003` for the root
 * and per navigation in include order.
 */
final class DbEagerJoin
{
    public function __construct(private readonly Db $db)
    {
    }

    /**
     * @param class-string $entityType
     * @param list<string> $includes navigation names, already validated (`REL-001`) by the caller
     * @return list<object>
     */
    public function load(SelectAst $ast, string $entityType, array $includes): array
    {
        $map = $ast->map;

        /** @var list<RelationshipMap> $relationships */
        $relationships = array_map(
            fn (string $navigation): RelationshipMap => $this->db->resolveNavigation($map, $navigation),
            $includes,
        );

        $collectionCount = 0;
        foreach ($relationships as $relationship) {
            if (self::isCollection($relationship)) {
                $collectionCount++;
            }
        }

        if ($collectionCount > 1) {
            throw new SimpleOrmException(
                'REL-006',
                $map->entityName(),
                'join-mode eager loading joins at most one collection navigation (a Cartesian product otherwise) — use MultiQuery or SubSelect for the rest',
            );
        }

        if ($collectionCount === 1 && ($ast->limit !== null || $ast->offset !== null)) {
            throw new SimpleOrmException(
                'REL-005',
                $map->entityName(),
                'a paged query cannot join-mode eager load a collection navigation (the join multiplies root rows) — use MultiQuery or SubSelect instead',
            );
        }

        if ($map->keyProperties === []) {
            throw new SimpleOrmException(
                'REL-003',
                $map->entityName(),
                'join-mode eager loading needs a keyed root to deduplicate; load via MultiQuery/SubSelect instead',
            );
        }

        [$joins, $navigationsByAlias] = $this->buildJoins($map, $relationships);

        $joinedAst = new SelectAst($map, $ast->where, $ast->orderings, $ast->limit, $ast->offset, $ast->projection, $joins);
        $queryName = Db::criteriaQueryName($map);
        $statement = $this->db->executeAstStatement($joinedAst, $queryName);

        return $this->readGraph($map, $entityType, $queryName, $navigationsByAlias, $statement);
    }

    /**
     * @param list<RelationshipMap> $relationships
     * @return array{0: list<SelectJoin>, 1: array<string, array{relationship: RelationshipMap, map: EntityMap, collection: bool}>}
     */
    private function buildJoins(EntityMap $map, array $relationships): array
    {
        $joins = [];
        $navigationsByAlias = [];
        $jCounter = 0;
        $lCounter = 0;

        foreach ($relationships as $relationship) {
            // Resolves and validates the navigation's target/link maps and
            // correlating property lists — the same check DbLoading applies,
            // so a malformed navigation (an unmapped FK name, an arity
            // mismatch) refuses `REL-003` here too, instead of reaching the
            // renderer and surfacing as `QRY-006` (spec/loading.md "Refusal
            // precedence": shape problems are always REL-003).
            $shape = NavigationShape::resolve($this->db->maps(), $map, $relationship);
            $targetMap = $shape->targetMap;
            if ($targetMap->keyProperties === []) {
                throw new SimpleOrmException(
                    'REL-003',
                    $map->navigationTarget($relationship),
                    'join-mode eager loading needs a keyed target to deduplicate; load via MultiQuery/SubSelect instead',
                );
            }

            if ($relationship->kind === RelationshipKind::ManyToMany) {
                $linkAlias = 'l' . $lCounter++;
                $joins[] = new SelectJoin(
                    $shape->linkMap,
                    $linkAlias,
                    null,
                    self::onPairs(self::names($shape->ownerProperties), self::names($shape->linkToOwner)),
                    false,
                );

                $targetAlias = 'j' . $jCounter++;
                $joins[] = new SelectJoin(
                    $targetMap,
                    $targetAlias,
                    $linkAlias,
                    self::onPairs(self::names($shape->linkToTarget), self::names($targetMap->keyProperties)),
                    true,
                );

                $navigationsByAlias[$targetAlias] = ['relationship' => $relationship, 'map' => $targetMap, 'collection' => true];
                continue;
            }

            // Many-to-one and one-to-one/one-to-many share the same ON-pair
            // shape once resolved: owner-side correlate = target-side
            // correlate, in order — NavigationShape already placed the FK on
            // whichever side declares it.
            $alias = 'j' . $jCounter++;
            $joins[] = new SelectJoin(
                $targetMap,
                $alias,
                null,
                self::onPairs(self::names($shape->ownerProperties), self::names($shape->targetProperties)),
                true,
            );
            $navigationsByAlias[$alias] = [
                'relationship' => $relationship,
                'map' => $targetMap,
                'collection' => $relationship->kind === RelationshipKind::OneToMany,
            ];
        }

        return [$joins, $navigationsByAlias];
    }

    /**
     * Reads the joined statement row by row: segments each row into the root
     * and every navigation's slice ({@see RowSegment}), maps each segment
     * through the one entity plan, deduplicates roots and children by
     * structural key equality, and attaches navigations by reflection
     * (`public private(set)` — {@see ReflectionProperty::setValue} bypasses
     * set-visibility, same as the C# reference's only-writer rule realized in
     * PHP, CODING-STANDARD §2).
     *
     * @param class-string $entityType
     * @param array<string, array{relationship: RelationshipMap, map: EntityMap, collection: bool}> $navigationsByAlias
     * @return list<object>
     */
    private function readGraph(
        EntityMap $map,
        string $entityType,
        string $queryName,
        array $navigationsByAlias,
        PDOStatement $statement,
    ): array {
        $mapper = $this->db->mapper();
        $rootColumnNames = self::columnNames($map->properties);
        $rootKeyColumnNames = self::columnNames($map->keyProperties);
        $rootPlan = $mapper->createPlan($entityType, $rootColumnNames, $queryName);

        /** @var array<string, ReflectionProperty> $navigationProperties */
        $navigationProperties = [];
        /** @var array<string, list<string>> $targetColumnNamesByAlias */
        $targetColumnNamesByAlias = [];
        /** @var array<string, list<string>> $targetKeyColumnNamesByAlias */
        $targetKeyColumnNamesByAlias = [];
        /** @var array<string, callable> $targetPlanByAlias */
        $targetPlanByAlias = [];
        foreach ($navigationsByAlias as $alias => $meta) {
            $navigationProperties[$alias] = $map->navigationProperty($meta['relationship']);
            $targetColumnNamesByAlias[$alias] = self::columnNames($meta['map']->properties);
            $targetKeyColumnNamesByAlias[$alias] = self::columnNames($meta['map']->keyProperties);
            $targetPlanByAlias[$alias] = $mapper->createPlan(
                $meta['relationship']->targetType,
                $targetColumnNamesByAlias[$alias],
                Db::criteriaQueryName($meta['map']),
            );
        }

        /** @var array<string, object> $rootsByKey */
        $rootsByKey = [];
        /** @var list<string> $rootOrder */
        $rootOrder = [];
        /** @var array<string, array<string, string>> $singularChildToken alias => rootToken => childToken */
        $singularChildToken = [];
        /** @var array<string, array<string, list<string>>> $collectionSeenTokens alias => rootToken => list<childToken> */
        $collectionSeenTokens = [];
        /** @var array<string, array<string, object>> $sharedChildren alias => childToken => entity, shared across every owner (spec: "owners sharing a target ... share the same instance") */
        $sharedChildren = [];

        while (($row = $statement->fetch(PDO::FETCH_ASSOC)) !== false) {
            $rootToken = $this->keyTupleFromSegment(RowSegment::extract($row, 't_', $rootKeyColumnNames), $map->keyProperties, $queryName)->token();

            if (!array_key_exists($rootToken, $rootsByKey)) {
                $entity = $rootPlan(RowSegment::extract($row, 't_', $rootColumnNames));
                foreach ($navigationsByAlias as $alias => $meta) {
                    if ($meta['collection']) {
                        // Re-initialize the included collection (spec/loading.md:
                        // "a loaded-but-empty collection reads as empty") — every
                        // root gets `[]`, filled below as rows match.
                        $navigationProperties[$alias]->setValue($entity, []);
                        $collectionSeenTokens[$alias][$rootToken] = [];
                    }
                }

                $rootsByKey[$rootToken] = $entity;
                $rootOrder[] = $rootToken;
            }

            $entity = $rootsByKey[$rootToken];

            foreach ($navigationsByAlias as $alias => $meta) {
                $childSegment = RowSegment::extract($row, $alias . '_', $targetColumnNamesByAlias[$alias]);
                if (RowSegment::isAbsent($childSegment, $targetKeyColumnNamesByAlias[$alias])) {
                    continue;   // a null FK owner or a dead link: no child this row (spec/loading.md "Join mode aliases")
                }

                $childToken = $this->keyTupleFromSegment($childSegment, $meta['map']->keyProperties, $queryName)->token();

                if ($meta['collection']) {
                    if (in_array($childToken, $collectionSeenTokens[$alias][$rootToken], true)) {
                        continue;   // the same target row, repeated by another navigation's fan-out or the many-to-many link hop
                    }

                    $collectionSeenTokens[$alias][$rootToken][] = $childToken;
                    $child = $sharedChildren[$alias][$childToken] ??= $targetPlanByAlias[$alias]($childSegment);
                    $current = $navigationProperties[$alias]->getValue($entity);
                    $current[] = $child;
                    $navigationProperties[$alias]->setValue($entity, $current);
                    continue;
                }

                $existingToken = $singularChildToken[$alias][$rootToken] ?? null;
                if ($existingToken !== null) {
                    if ($meta['relationship']->kind === RelationshipKind::OneToOne && $existingToken !== $childToken) {
                        // A second distinct target key for one root under a one-to-one
                        // alias is REL-002 in every include combination: children
                        // deduplicate by key, and a keyed target cannot repeat one
                        // (spec/loading.md Clarifications, ADR-0033).
                        throw new SimpleOrmException(
                            'REL-002',
                            "{$map->entityName()}.{$meta['relationship']->propertyName}",
                            'the one-to-one navigation matched more than one row; the target foreign key needs a unique index',
                        );
                    }

                    continue;
                }

                $child = $sharedChildren[$alias][$childToken] ??= $targetPlanByAlias[$alias]($childSegment);
                $singularChildToken[$alias][$rootToken] = $childToken;
                $navigationProperties[$alias]->setValue($entity, $child);
            }
        }

        $this->sortCollections($navigationsByAlias, $navigationProperties, $rootsByKey);

        return array_map(static fn (string $token): object => $rootsByKey[$token], $rootOrder);
    }

    /**
     * Collections order by target key, compared value-wise, in every mode
     * (spec/loading.md "In every mode") through {@see EntityMap::sortByKey()}
     * — the same ordering {@see DbLoading} applies to its own collections
     * (CODING-STANDARD §8), so both engines agree regardless of what a
     * dialect's own `ORDER BY` would have done with the stored representation.
     * The AST orders root properties only, so every collection is sorted
     * value-wise after fetching — the same post-fetch sort as the other modes
     * (spec/loading.md Clarifications, ADR-0033).
     *
     * @param array<string, array{relationship: RelationshipMap, map: EntityMap, collection: bool}> $navigationsByAlias
     * @param array<string, ReflectionProperty> $navigationProperties
     * @param array<string, object> $rootsByKey
     */
    private function sortCollections(array $navigationsByAlias, array $navigationProperties, array $rootsByKey): void
    {
        foreach ($navigationsByAlias as $alias => $meta) {
            if (!$meta['collection']) {
                continue;
            }

            $targetMap = $meta['map'];
            $property = $navigationProperties[$alias];
            foreach ($rootsByKey as $entity) {
                $property->setValue($entity, $targetMap->sortByKey($property->getValue($entity)));
            }
        }
    }

    private static function isCollection(RelationshipMap $relationship): bool
    {
        return $relationship->kind === RelationshipKind::OneToMany || $relationship->kind === RelationshipKind::ManyToMany;
    }

    /** @param list<PropertyMap> $properties @return list<string> */
    private static function names(array $properties): array
    {
        return array_map(static fn (PropertyMap $p): string => $p->propertyName(), $properties);
    }

    /** @param list<PropertyMap> $properties @return list<string> */
    private static function columnNames(array $properties): array
    {
        return array_map(static fn (PropertyMap $p): string => $p->columnName, $properties);
    }

    /**
     * @param list<string> $parentNames properties of the parent alias, in order
     * @param list<string> $targetNames properties of the target alias, in the same order
     * @return list<JoinPair>
     */
    private static function onPairs(array $parentNames, array $targetNames): array
    {
        $pairs = [];
        foreach ($parentNames as $i => $parentName) {
            $pairs[] = new JoinPair($parentName, $targetNames[$i]);
        }

        return $pairs;
    }

    /**
     * Converts a raw row segment's key columns into a {@see KeyTuple} through
     * the ordinary conversion pipeline, so identity is decided on the same
     * **values** {@see \SimpleOrm\Mapping\ResultMapper} would build the entity
     * from — never on the raw PDO cell. A type-tagged token over the raw cell
     * (this class's previous approach) happened to be safe for the exact
     * dedup this method needs (a physical row's own column always fetches the
     * same PHP scalar twice), but a converted `Decimal`/temporal value is the
     * one identity implementation shared with {@see DbLoading} and
     * {@see \SimpleOrm\Metadata\EntityMap::keysEqual()} (CODING-STANDARD §8),
     * so this pays one conversion per row rather than keeping a second,
     * narrower equality rule alive.
     *
     * @param array<string, mixed> $rawSegment keyed by plain column name (e.g. {@see RowSegment::extract()})
     * @param list<PropertyMap> $properties the key properties owning those columns, in order
     */
    private function keyTupleFromSegment(array $rawSegment, array $properties, string $context): KeyTuple
    {
        $converter = $this->db->converter();

        return new KeyTuple(array_map(
            static fn (PropertyMap $p): mixed => $converter->fromDatabase(
                $rawSegment[$p->columnName] ?? null,
                $p->type,
                $p->phpType,
                $context,
            ),
            $properties,
        ));
    }
}
