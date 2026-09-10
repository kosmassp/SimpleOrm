<?php

declare(strict_types=1);

namespace SimpleOrm\Session;

use DateTimeImmutable;
use PDO;
use PDOStatement;
use ReflectionProperty;
use SimpleOrm\Errors\SimpleOrmException;
use SimpleOrm\Mapping\RowSegment;
use SimpleOrm\Metadata\EntityMap;
use SimpleOrm\Metadata\PropertyMap;
use SimpleOrm\Metadata\RelationshipKind;
use SimpleOrm\Metadata\RelationshipMap;
use SimpleOrm\Query\JoinPair;
use SimpleOrm\Query\SelectAst;
use SimpleOrm\Query\SelectJoin;
use SimpleOrm\Types\Decimal;

use function array_key_exists;
use function array_map;
use function in_array;
use function is_bool;
use function is_float;
use function is_int;
use function usort;

/**
 * Join-mode eager loading (ADR-0022 add.1) — the PHP counterpart of the C# `Db`
 * partial in `DbEagerJoin.cs` (a collaborator here, CODING-STANDARD §10): one
 * SELECT with a LEFT JOIN per included navigation (a many-to-many joins its
 * link unprojected, then the target), the row partitioned into per-alias
 * segments that reuse the one mapping pipeline (§7.11, {@see RowSegment} +
 * {@see \SimpleOrm\Mapping\ResultMapper}), roots deduplicated by key in
 * first-appearance order, children attached by structural key equality.
 * Refuses paging with a collection include (`REL-005`), more than one
 * collection include (`REL-006`), and keyless roots/targets (`REL-003`) —
 * never in-memory paging or a silent Cartesian product.
 *
 * // SPEC-GAP: spec/loading.md's "Refusal precedence" clarification orders
 * // REL-001, then REL-005/REL-006, then REL-003 — the task brief listed
 * // REL-003 first instead. This class follows the spec text (the more
 * // detailed, explicitly-titled ruling): REL-006, then REL-005, then REL-003.
 * // Between REL-005 and REL-006 themselves the spec gives no order; this
 * // checks REL-006 (several collections) before REL-005 (paging), the
 * // structurally broader problem first.
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
        $queryName = self::shortName($entityType) . ' criteria';
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
            $targetMap = $this->db->maps()->load($relationship->targetType);
            if ($targetMap->keyProperties === []) {
                throw new SimpleOrmException(
                    'REL-003',
                    "{$map->entityName()}.{$relationship->propertyName}",
                    'join-mode eager loading needs a keyed target to deduplicate; load via MultiQuery/SubSelect instead',
                );
            }

            if ($relationship->kind === RelationshipKind::ManyToMany) {
                $linkType = $relationship->linkType
                    ?? throw new SimpleOrmException('REL-003', "{$map->entityName()}.{$relationship->propertyName}", 'a many-to-many navigation with no declared link');
                $linkMap = $this->db->maps()->load($linkType);

                $linkAlias = 'l' . $lCounter++;
                $joins[] = new SelectJoin(
                    $linkMap,
                    $linkAlias,
                    null,
                    self::onPairs(self::names($map->keyProperties), $relationship->linkForeignKeysToOwner),
                    false,
                );

                $targetAlias = 'j' . $jCounter++;
                $joins[] = new SelectJoin(
                    $targetMap,
                    $targetAlias,
                    $linkAlias,
                    self::onPairs($relationship->linkForeignKeysToTarget, self::names($targetMap->keyProperties)),
                    true,
                );

                $navigationsByAlias[$targetAlias] = ['relationship' => $relationship, 'map' => $targetMap, 'collection' => true];
                continue;
            }

            $alias = 'j' . $jCounter++;
            $on = $relationship->kind === RelationshipKind::ManyToOne
                ? self::onPairs($relationship->foreignKeyProperties, self::names($targetMap->keyProperties))
                : self::onPairs(self::names($map->keyProperties), $relationship->foreignKeyProperties);

            $joins[] = new SelectJoin($targetMap, $alias, null, $on, true);
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
            $navigationProperties[$alias] = new ReflectionProperty($map->entityType, $meta['relationship']->propertyName);
            $targetColumnNamesByAlias[$alias] = self::columnNames($meta['map']->properties);
            $targetKeyColumnNamesByAlias[$alias] = self::columnNames($meta['map']->keyProperties);
            $targetPlanByAlias[$alias] = $mapper->createPlan(
                $meta['relationship']->targetType,
                $targetColumnNamesByAlias[$alias],
                self::shortName($meta['relationship']->targetType) . ' criteria',
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
            $rootToken = self::token(RowSegment::extract($row, 't_', $rootKeyColumnNames));

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

                $childToken = self::token(RowSegment::extract($row, $alias . '_', $targetKeyColumnNamesByAlias[$alias]));

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
                        // SPEC-GAP: with several included navigations the spec notes
                        // identity-dedup can mask a genuine duplicate ("a same-key
                        // duplicate source row is then indistinguishable from it",
                        // spec/loading.md "Join mode aliases"). This still throws
                        // whenever a *different* target key is observed for the same
                        // root under one one-to-one alias — correct with one included
                        // navigation (the pinned case), best-effort alongside a
                        // collection navigation's cross-join fan-out.
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
     * (spec/loading.md "In every mode"). // SPEC-GAP: the AST's `Ordering`
     * vocabulary resolves only root properties (query-ast.md "Joins": every
     * ordering qualifies with `t.`) — a joined collection has no way to carry
     * its own ORDER BY, so this sorts after fetching instead of rendering one.
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
                $children = $property->getValue($entity);
                usort($children, static fn (object $a, object $b): int => self::compareKeyTuples(
                    $targetMap->getKeyValues($a),
                    $targetMap->getKeyValues($b),
                ));
                $property->setValue($entity, $children);
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
     * A type-tagged correlation key for one row's raw (pre-conversion) column
     * values — NOT the "stringified token" spec/loading.md rules out (ADR-0021
     * add.1): that warning is about collapsing *typed* values to one common
     * string and losing the type distinction (so date/blob keys collide with
     * unrelated values); this tags every value with its raw PDO type first, so
     * an int and a string never collapse into the same token, which is all
     * structural equality needs for deduplication (the entities themselves are
     * still built through the ordinary conversion pipeline).
     *
     * @param array<string, mixed> $rawValues
     */
    private static function token(array $rawValues): string
    {
        $parts = [];
        foreach ($rawValues as $value) {
            $parts[] = match (true) {
                $value === null => 'N',
                is_bool($value) => 'b:' . ($value ? '1' : '0'),
                is_int($value) => 'i:' . $value,
                is_float($value) => 'f:' . $value,
                default => 's:' . (string) $value,
            };
        }

        return implode("\x1f", $parts);
    }

    /** @param list<mixed> $left @param list<mixed> $right */
    private static function compareKeyTuples(array $left, array $right): int
    {
        foreach ($left as $i => $value) {
            $cmp = self::compareValue($value, $right[$i]);
            if ($cmp !== 0) {
                return $cmp;
            }
        }

        return 0;
    }

    /**
     * Value-wise comparison (spec/loading.md "Clarifications"): numbers
     * numerically — decimals included — strings ordinally, temporals by
     * instant, booleans false before true. GUIDs and byte arrays are PHP
     * strings here (CODING-STANDARD §10); ordinal string comparison is byte
     * comparison for them, matching "by their bytes".
     */
    private static function compareValue(mixed $a, mixed $b): int
    {
        if ($a instanceof DateTimeImmutable && $b instanceof DateTimeImmutable) {
            return $a <=> $b;
        }

        if ($a instanceof Decimal && $b instanceof Decimal) {
            return (float) $a->value <=> (float) $b->value;
        }

        if (is_bool($a) && is_bool($b)) {
            return ($a ? 1 : 0) <=> ($b ? 1 : 0);
        }

        if (is_numeric($a) && is_numeric($b)) {
            return $a <=> $b;
        }

        return strcmp((string) $a, (string) $b);
    }

    private static function shortName(string $entityType): string
    {
        $slash = strrpos($entityType, '\\');

        return $slash === false ? $entityType : substr($entityType, $slash + 1);
    }
}
