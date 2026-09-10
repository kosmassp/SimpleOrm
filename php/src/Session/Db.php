<?php

declare(strict_types=1);

namespace SimpleOrm\Session;

use Generator;
use LogicException;
use PDO;
use PDOStatement;
use ReflectionClass;
use ReflectionNamedType;
use ReflectionProperty;
use SimpleOrm\Errors\ConcurrencyException;
use SimpleOrm\Errors\SimpleOrmException;
use SimpleOrm\Mapping\ResultMapper;
use SimpleOrm\Mapping\TypeConverter;
use SimpleOrm\Metadata\ColumnType;
use SimpleOrm\Metadata\EntityMap;
use SimpleOrm\Metadata\EntityMapLoader;
use SimpleOrm\Metadata\KeyStrategy;
use SimpleOrm\Metadata\PropertyMap;
use SimpleOrm\Metadata\RelationKind;
use SimpleOrm\Metadata\RelationshipKind;
use SimpleOrm\Metadata\RelationshipMap;
use SimpleOrm\Parameters\ParameterBinder;
use SimpleOrm\Parameters\PdoBinder;
use SimpleOrm\Parameters\SqlPlaceholders;
use SimpleOrm\Query\CriteriaQuery;
use SimpleOrm\Query\SelectAst;
use Stringable;

/**
 * The session (§7.17): owns one PDO connection obtained from the dialect and,
 * at most, one active transaction (PDO's own, so every command enlists
 * automatically). No ambient state; every round trip is visible in user code.
 * Mirrors dotnet/src/SimpleOrm/Db.cs — the async/`CancellationToken` contract
 * there is a synchronous call here (CODING-STANDARD §10).
 */
final class Db
{
    private ?PDO $connection;

    private readonly EntityMapLoader $maps;

    private readonly TypeConverter $converter;

    private readonly ResultMapper $mapper;

    private readonly DbLoading $loading;

    private readonly DbEagerJoin $eagerJoin;

    private function __construct(
        PDO $connection,
        private readonly DbOptions $options,
    ) {
        $this->connection = $connection;
        $this->maps = new EntityMapLoader($options->mapping);
        $this->converter = new TypeConverter($options->typeHandlers, $options->dialect->bindsTemporalsNatively());
        $this->mapper = new ResultMapper($this->maps, $this->converter);
        $this->loading = new DbLoading($this);
        $this->eagerJoin = new DbEagerJoin($this);
    }

    /** Opens the connection now (§7.17): a failed open leaves nothing behind. */
    public static function open(string $connectionString, DbOptions $options): self
    {
        return new self($options->dialect->createConnection($connectionString), $options);
    }

    // --- accessors the migration runner, SchemaGuard, and the CLI need ----------

    public function connection(): PDO
    {
        return $this->connection ?? throw new LogicException('the session is closed');
    }

    public function options(): DbOptions
    {
        return $this->options;
    }

    public function maps(): EntityMapLoader
    {
        return $this->maps;
    }

    public function converter(): TypeConverter
    {
        return $this->converter;
    }

    public function mapper(): ResultMapper
    {
        return $this->mapper;
    }

    // --- registry surface (§6, spec/session.md) ---------------------------------

    /**
     * @return list<mixed> a registry query's result type may be an entity, a
     *     DTO, or a scalar (`int`, `string`, …) — mirrors dotnet's generic
     *     `TResult`, which C# lets be a value type too (CODING-STANDARD §10)
     */
    public function query(Query $query, object $args): array
    {
        $statement = $this->createStatement($query->source, $args);

        return $this->materializeAll($statement, $query->resultType, $query->source->description);
    }

    /** Exactly one row: zero throws `QRY-001`, more than one throws `QRY-002`. */
    public function querySingle(Query $query, object $args): mixed
    {
        $rows = $this->query($query, $args);

        return match (count($rows)) {
            1 => $rows[0],
            0 => throw new SimpleOrmException('QRY-001', $query->source->description, 'expected exactly one row, found none'),
            default => throw new SimpleOrmException(
                'QRY-002',
                $query->source->description,
                sprintf('expected exactly one row, found %d', count($rows)),
            ),
        };
    }

    /** At most one row: zero returns null, more than one throws `QRY-002`. */
    public function querySingleOrDefault(Query $query, object $args): mixed
    {
        $rows = $this->query($query, $args);

        return match (count($rows)) {
            0 => null,
            1 => $rows[0],
            default => throw new SimpleOrmException(
                'QRY-002',
                $query->source->description,
                sprintf('expected at most one row, found %d', count($rows)),
            ),
        };
    }

    /**
     * Lazy row iteration (CODING-STANDARD §10: the PHP shape of `IAsyncEnumerable`).
     * The result plan is built — so `MAP-001/002/003` fire — as soon as iteration
     * starts, before the first row is produced, exactly like the registry's list
     * form.
     */
    public function stream(Query $query, object $args): Generator
    {
        $statement = $this->createStatement($query->source, $args);
        $plan = $this->planFor($statement, $query->resultType, $query->source->description);
        while (($row = $statement->fetch(PDO::FETCH_ASSOC)) !== false) {
            yield $plan($row);
        }
    }

    /** A non-query command; returns the affected-row count. */
    public function execute(Command $command, object $args): int
    {
        return $this->createStatement($command->source, $args)->rowCount();
    }

    // --- transactions (§7.17) ---------------------------------------------------

    /** Begins the session's transaction scope; a second concurrent scope throws `TX-001`. */
    public function begin(): DbTransactionScope
    {
        if ($this->connection()->inTransaction()) {
            throw new SimpleOrmException('TX-001', 'session', 'a transaction is already active on this session');
        }

        $this->connection()->beginTransaction();

        return new DbTransactionScope($this);
    }

    /** @internal called by {@see DbTransactionScope} only. */
    public function commitTransaction(): void
    {
        $this->connection()->commit();
    }

    /** @internal called by {@see DbTransactionScope} only. */
    public function rollbackTransaction(): void
    {
        if ($this->connection !== null && $this->connection->inTransaction()) {
            $this->connection->rollBack();
        }
    }

    // --- criteria queries and key reads (ADR-0006, ADR-0012) --------------------

    /** Starts a criteria query over a named readable source; statements/procedures throw `QRY-005`. */
    public function from(string $entityType): CriteriaQuery
    {
        $map = $this->maps->load($entityType);
        $this->requireNamedRelation($map, 'criteria queries need a named relation (statements execute via the statement API)');

        return new CriteriaQuery($this, $entityType);
    }

    /**
     * Runs a criteria AST through the dialect and the one mapping pipeline
     * (§7.11) — the single execution path for `CriteriaQuery`, the loading
     * engines, and the eager-join engine. Criteria parameter values bind in
     * render order (`@c0…`); the compared property's conversion rules apply —
     * an enum against an `#[EnumAsInt]` column binds as its number, not its name.
     *
     * @param class-string $entityType
     * @return list<object>
     * @internal used by {@see CriteriaQuery} and the loading engines
     */
    public function executeAst(SelectAst $ast, string $entityType): array
    {
        $queryName = self::shortName($entityType) . ' criteria';
        $statement = $this->executeAstStatement($ast, $queryName);
        $plan = $this->planFor($statement, $entityType, $queryName);

        $results = [];
        while (($row = $statement->fetch(PDO::FETCH_ASSOC)) !== false) {
            $results[] = $plan($row);
        }

        return $results;
    }

    /**
     * Renders and executes a criteria AST, returning the open statement — the
     * join engine reads its columns itself (segmented by alias), everything
     * else goes through {@see executeAst()}.
     *
     * @internal
     */
    public function executeAstStatement(SelectAst $ast, string $queryName): PDOStatement
    {
        $converter = $this->converter;
        $parameters = [];
        $bindParameter = static function (mixed $value, ?PropertyMap $property) use (&$parameters, $converter, $queryName): string {
            $name = 'c' . count($parameters);
            $parameters[$name] = $converter->toDatabase($value, "{$queryName} @{$name}", $property?->enumAsInt() ?? false);

            return '@' . $name;
        };

        $sql = SqlPlaceholders::toPdo($this->options->dialect->selectSql($ast, $bindParameter));
        $statement = $this->connection()->prepare($sql);
        // PdoBinder::bindAndExecute, not a bare execute($parameters): the latter binds
        // everything as PDO::PARAM_STR, which a column SQLite gives no
        // declared affinity (a view's aggregate expression) never coerces back.
        PdoBinder::bindAndExecute($statement, $parameters);

        return $statement;
    }

    // --- relationship loading (ADR-0021/0022, spec/loading.md) -------------------

    /** Loads one declared navigation of one entity (ADR-0021). */
    public function load(object $entity, string $navigation): void
    {
        $this->loadEach([$entity], $navigation);
    }

    /**
     * The batch form (ADR-0019 M3): loads one declared navigation for every
     * entity in the list with one query per chunk — never one per entity.
     * Within one call, owners sharing a many-to-one target share the same
     * loaded instance. Every entity must be of one class (`REL-001` names the
     * navigation against the first entity's class).
     *
     * @param list<object> $entities
     */
    public function loadEach(array $entities, string $navigation): void
    {
        $this->loadEachFrom($entities, $navigation, null);
    }

    /**
     * With `$ownerSubquery` (ADR-0022 add.1, SubSelect mode) the owner set is
     * expressed as `in (select …)` over the root query instead of a client-side
     * key list — one query per navigation, no chunking.
     *
     * @param list<object> $entities
     * @internal used by {@see CriteriaQuery}
     */
    public function loadEachFrom(array $entities, string $navigation, ?SelectAst $ownerSubquery): void
    {
        // The navigation validates even for an empty batch: a wrong name is a
        // bug regardless of how many entities happened to be in the list. An
        // empty batch names no class, so the owner subquery's map (SubSelect)
        // is the only one available; a plain empty batch has nothing to check.
        if ($entities === [] && $ownerSubquery === null) {
            return;
        }

        $map = $ownerSubquery?->map ?? $this->maps->load($entities[0]::class);
        $relationship = $this->resolveNavigation($map, $navigation);
        if ($entities === []) {
            return;
        }

        $this->loading->loadEach($map, $relationship, array_values($entities), $ownerSubquery);
    }

    /** A navigation by exact property name, or `REL-001` listing the declared ones. */
    public function resolveNavigation(EntityMap $map, string $navigation): RelationshipMap
    {
        foreach ($map->relationships as $relationship) {
            if ($relationship->propertyName === $navigation) {
                return $relationship;
            }
        }

        $declared = $map->relationships === []
            ? 'none'
            : implode(', ', array_map(static fn (RelationshipMap $r): string => $r->propertyName, $map->relationships));

        throw new SimpleOrmException(
            'REL-001',
            $map->entityName(),
            "'{$navigation}' is not a declared navigation (declared: {$declared})",
        );
    }

    /**
     * Join-mode eager loading (ADR-0022 add.1): one SELECT with LEFT JOINs.
     *
     * @param class-string $entityType
     * @param list<string> $includes
     * @return list<object>
     * @internal used by {@see CriteriaQuery}
     */
    public function eagerJoinLoad(SelectAst $ast, string $entityType, array $includes): array
    {
        return $this->eagerJoin->load($ast, $entityType, $includes);
    }

    /** Read by key (ADR-0006): a missing row throws `CRUD-001`; a composite key is a list in key order. */
    public function get(string $entityType, mixed $key): object
    {
        return $this->getOrDefault($entityType, $key)
            ?? throw new SimpleOrmException(
                'CRUD-001',
                $this->maps->load($entityType)->entityName(),
                sprintf('no row with key (%s)', self::formatKey($key)),
            );
    }

    /** Read by key (ADR-0006): a missing row returns null. */
    public function getOrDefault(string $entityType, mixed $key): ?object
    {
        $map = $this->maps->load($entityType);
        $this->requireNamedRelation($map, 'key reads need a named relation');

        $keyValues = self::validateKey($map, $key);
        $dialect = $this->options->dialect;

        $predicates = [];
        $parameters = [];
        foreach ($keyValues as $i => $value) {
            $predicates[] = $dialect->quoteIdentifier($map->keyProperties[$i]->columnName) . " = :k{$i}";
            $parameters["k{$i}"] = $this->converter->toDatabase($value, "{$map->entityName()} key[{$i}]");
        }

        $sql = 'select ' . implode(', ', array_map(
            static fn (PropertyMap $p): string => $dialect->quoteIdentifier($p->columnName),
            $map->properties,
        )) . ' from ' . $dialect->quoteIdentifier((string) $map->relationName)
            . ' where ' . implode(' and ', $predicates);

        $statement = $this->connection()->prepare($sql);
        PdoBinder::bindAndExecute($statement, $parameters);
        $plan = $this->planFor($statement, $entityType, $map->entityName() . ' get');

        $result = null;
        while (($row = $statement->fetch(PDO::FETCH_ASSOC)) !== false) {
            if ($result !== null) {
                throw new SimpleOrmException('QRY-002', $map->entityName(), 'the key matched more than one row');
            }

            $result = $plan($row);
        }

        return $result;
    }

    /** ADR-0006: the key (or list, in key order) must match the EntityMap key in arity and type. */
    private static function validateKey(EntityMap $map, mixed $key): array
    {
        $target = $map->entityName();
        if ($map->keyProperties === []) {
            throw new SimpleOrmException('CRUD-002', $target, 'the entity defines no key');
        }

        $provided = is_array($key) ? array_values($key) : [$key];
        if (count($provided) !== count($map->keyProperties)) {
            throw new SimpleOrmException(
                'CRUD-002',
                $target,
                sprintf('the key has %d part(s), %d value(s) were provided', count($map->keyProperties), count($provided)),
            );
        }

        $coerced = [];
        foreach ($provided as $i => $value) {
            $keyProperty = $map->keyProperties[$i];
            if (!self::matchesPhpType($value, $keyProperty->phpType)) {
                throw new SimpleOrmException(
                    'CRUD-002',
                    $target,
                    sprintf(
                        'key part %d (%s) expects %s, got %s',
                        $i,
                        $keyProperty->propertyName(),
                        $keyProperty->phpType ?? 'unknown',
                        get_debug_type($value),
                    ),
                );
            }

            $coerced[] = $value;
        }

        return $coerced;
    }

    private static function matchesPhpType(mixed $value, ?string $phpType): bool
    {
        return match (true) {
            $phpType === null => true,
            $phpType === 'int' => is_int($value),
            $phpType === 'float' => is_float($value) || is_int($value),
            $phpType === 'string' => is_string($value),
            $phpType === 'bool' => is_bool($value),
            default => $value instanceof $phpType,
        };
    }

    private function requireNamedRelation(EntityMap $map, string $reason): void
    {
        if ($map->kind === RelationKind::Statement || $map->kind === RelationKind::Procedure) {
            throw new SimpleOrmException(
                'QRY-005',
                $map->entityName(),
                "is {$map->kind->value}-backed; {$reason}",
            );
        }
    }

    // --- generated DDL and CRUD (ADR-0011, §7.14) -------------------------------

    /** Creates the entity's table and declared indexes from its metadata (idempotent). Non-table sources throw `DDL-001`. */
    public function createTable(string $entityType): void
    {
        $map = $this->maps->load($entityType);
        if ($map->kind !== RelationKind::Table) {
            throw new SimpleOrmException(
                'DDL-001',
                $map->entityName(),
                "is {$map->kind->value}-backed; only tables can be created from metadata",
            );
        }

        $this->connection()->exec($this->options->dialect->createTableSql($map));
        foreach ($this->options->dialect->createIndexSql($map) as $indexSql) {
            $this->connection()->exec($indexSql);
        }
    }

    /** Creates a view (or materialized view) from the entity's defining SQL. Other sources throw `DDL-001`; unsupported materialized views throw `DDL-002`. */
    public function createView(string $entityType): void
    {
        $map = $this->maps->load($entityType);
        if ($map->kind !== RelationKind::View && $map->kind !== RelationKind::MaterializedView) {
            throw new SimpleOrmException(
                'DDL-001',
                $map->entityName(),
                "is {$map->kind->value}-backed; createView applies to views only",
            );
        }

        if ($map->kind === RelationKind::MaterializedView && !$this->options->dialect->supportsMaterializedViews()) {
            throw new SimpleOrmException(
                'DDL-002',
                $map->entityName(),
                'the dialect has no materialized views (SQLite; Level 4 Postgres will)',
            );
        }

        $this->connection()->exec($this->options->dialect->createViewSql($map));
        foreach ($this->options->dialect->createIndexSql($map) as $indexSql) {
            $this->connection()->exec($indexSql);
        }
    }

    /** Generated select-all: explicit column list, ordered by the key when one exists. `QRY-005` for statements/procedures. */
    public function queryAll(string $entityType): array
    {
        $map = $this->maps->load($entityType);
        $this->requireNamedRelation($map, 'select-all needs a named relation (statements execute via the statement API)');

        $dialect = $this->options->dialect;
        $sql = 'select ' . implode(', ', array_map(
            static fn (PropertyMap $p): string => $dialect->quoteIdentifier($p->columnName),
            $map->properties,
        )) . ' from ' . $dialect->quoteIdentifier((string) $map->relationName);
        if ($map->keyProperties !== []) {
            $sql .= ' order by ' . implode(', ', array_map(
                static fn (PropertyMap $k): string => $dialect->quoteIdentifier($k->columnName),
                $map->keyProperties,
            ));
        }

        $statement = $this->connection()->prepare($sql);
        $statement->execute();

        return $this->materializeAll($statement, $entityType, $map->entityName() . ' select-all');
    }

    /**
     * Generated insert (§7.14): explicit non-generated column list; a
     * database-generated key is read back via RETURNING and written onto the
     * entity; an empty client-GUID key is assigned first. A non-null
     * `[ManyToOne]` navigation whose key disagrees with its FK property throws
     * `CRUD-004`; read-only sources throw `CRUD-003`.
     */
    public function insert(object $entity): void
    {
        $map = $this->maps->load($entity::class);
        if (!$map->kind->isWritable()) {
            throw new SimpleOrmException(
                'CRUD-003',
                $map->entityName(),
                "is {$map->kind->value}-backed and read-only; writes need a table",
            );
        }

        $this->checkNavigationConsistency($map, $entity);

        if ($map->keyStrategy === KeyStrategy::ClientGuid) {
            $keyProperty = $map->keyProperties[0];
            $current = $keyProperty->getValue($entity);
            if ($current === null || $current === '') {
                $keyProperty->setValue($entity, self::newGuid());
            }
        }

        $sql = SqlPlaceholders::toPdo($this->options->dialect->insertSql($map));
        $statement = $this->connection()->prepare($sql);

        $parameters = [];
        foreach ($map->properties as $property) {
            if ($property->generated) {
                continue;
            }

            $parameters[$property->columnName] = $this->converter->toDatabase(
                $property->getValue($entity),
                "{$map->entityName()}.{$property->propertyName()}",
                $property->enumAsInt(),
            );
        }

        PdoBinder::bindAndExecute($statement, $parameters);

        if ($map->keyStrategy === KeyStrategy::DatabaseGenerated) {
            $row = $statement->fetch(PDO::FETCH_ASSOC);
            $keyProperty = $map->keyProperties[0];
            $generated = is_array($row) ? ($row[$keyProperty->columnName] ?? null) : null;
            $keyProperty->setValue($entity, $this->converter->fromDatabase(
                $generated,
                $keyProperty->type,
                $keyProperty->phpType,
                "{$map->entityName()}.{$keyProperty->propertyName()}",
            ));
        }
    }

    /**
     * Generated full-row update by key (§7.15): every mapped non-key column.
     * With a version column (§7.16): `version = version + 1`, requires the
     * entity's version in the WHERE, throws {@see ConcurrencyException}
     * (`CRUD-010`) on zero rows, and bumps the entity's version on success.
     * Without one, zero rows is `CRUD-001`. For a narrower SET see {@see updateOnly()}.
     */
    public function update(object $entity): void
    {
        $map = $this->requireWritableKeyed($entity::class);
        $this->checkNavigationConsistency($map, $entity);

        // Bind SET values, key values, and the current version for the WHERE;
        // database-generated non-key columns are never written.
        $this->executeUpdate($map, $entity, $this->options->dialect->updateSql($map), array_values(array_filter(
            $map->properties,
            static fn (PropertyMap $p): bool => $p->key || $p->version || !$p->generated,
        )));
    }

    /**
     * Update by column list (ADR-0028): writes only the named properties — the
     * caller says what changed — with every other rule of {@see update()}
     * intact: keyed WHERE, version bump and check (`CRUD-010`), `CRUD-001`
     * without a version column. Names are property names (the criteria
     * vocabulary). An unmapped name is `CRUD-005`; a key, version, or generated
     * property is `CRUD-006`; an empty or repeating list is `CRUD-007`. A
     * separate method, not an optional argument: the spec names one operation
     * per concept.
     *
     * @param list<string> $properties
     */
    public function updateOnly(object $entity, array $properties): void
    {
        $map = $this->requireWritableKeyed($entity::class);
        $set = self::resolveUpdateList($map, $properties);
        $listed = array_map(static fn (PropertyMap $p): string => $p->propertyName(), $set);
        $this->checkNavigationConsistency($map, $entity, $listed);

        $bound = array_merge($set, $map->keyProperties);
        if ($map->versionProperty !== null) {
            $bound[] = $map->versionProperty;
        }

        $this->executeUpdate($map, $entity, $this->options->dialect->updateOnlySql($map, $set), $bound);
    }

    /**
     * Validates an update-by-column-list (ADR-0028) and resolves it to property maps, in the caller's order.
     *
     * @param list<string> $properties
     * @return list<PropertyMap>
     */
    private static function resolveUpdateList(EntityMap $map, array $properties): array
    {
        $entityName = $map->entityName();
        if ($properties === []) {
            throw new SimpleOrmException('CRUD-007', $entityName, 'update by column list needs at least one property');
        }

        $resolved = [];
        $seen = [];
        foreach ($properties as $propertyName) {
            $target = "{$entityName}.{$propertyName}";
            $property = $map->property($propertyName);
            if ($property === null) {
                foreach ($map->ownedTypes as $ownedNavigation) {
                    if ($ownedNavigation->propertyName() === $propertyName) {
                        // An owned navigation's name stands for all its members (ADR-0030).
                        foreach ($ownedNavigation->members() as $member) {
                            if (isset($seen[$member->propertyName()])) {
                                throw new SimpleOrmException('CRUD-007', "{$entityName}.{$member->propertyName()}", 'is listed more than once');
                            }

                            $seen[$member->propertyName()] = true;
                            $resolved[] = $member;
                        }

                        continue 2;
                    }
                }

                throw new SimpleOrmException('CRUD-005', $target, 'is not a mapped property; the list takes property names');
            }

            if ($property->key) {
                throw new SimpleOrmException('CRUD-006', $target, 'is a key property; an update never writes the key');
            }

            if ($property->version) {
                throw new SimpleOrmException('CRUD-006', $target, 'is the version column; the database computes it');
            }

            if ($property->generated) {
                throw new SimpleOrmException('CRUD-006', $target, 'is database-generated and never written');
            }

            if (isset($seen[$propertyName])) {
                throw new SimpleOrmException('CRUD-007', $target, 'is listed more than once');
            }

            $seen[$propertyName] = true;
            $resolved[] = $property;
        }

        return $resolved;
    }

    /**
     * The shared tail of both updates (§7.15–16): bind, execute, judge the row count, bump the version.
     *
     * @param list<PropertyMap> $bound
     */
    private function executeUpdate(EntityMap $map, object $entity, string $sql, array $bound): void
    {
        $statement = $this->connection()->prepare(SqlPlaceholders::toPdo($sql));

        $parameters = [];
        foreach ($bound as $property) {
            $parameters[$property->columnName] = $this->converter->toDatabase(
                $property->getValue($entity),
                "{$map->entityName()}.{$property->propertyName()}",
                $property->enumAsInt(),
            );
        }

        PdoBinder::bindAndExecute($statement, $parameters);

        if ($statement->rowCount() === 0) {
            if ($map->versionProperty !== null) {
                throw new ConcurrencyException(
                    $map->entityName(),
                    sprintf(
                        'update affected no rows: version %s is stale or the row is gone',
                        self::stringifyKeyPart($map->versionProperty->getValue($entity)),
                    ),
                );
            }

            throw new SimpleOrmException(
                'CRUD-001',
                $map->entityName(),
                sprintf('update affected no rows: no row with key (%s)', self::formatKeyOf($map, $entity)),
            );
        }

        if ($map->versionProperty !== null) {
            $current = (int) $map->versionProperty->getValue($entity);
            $map->versionProperty->setValue($entity, $current + 1);
        }
    }

    /**
     * Generated delete (§7.16). A key (or list) deletes by key — a missing row
     * throws `CRUD-001`. The entity gives the version-checked form when a
     * version column is mapped: zero rows throws {@see ConcurrencyException}
     * (`CRUD-010`).
     */
    public function delete(string $entityType, mixed $keyOrEntity): void
    {
        $map = $this->requireWritableKeyed($entityType);

        $byEntity = is_object($keyOrEntity) && $keyOrEntity instanceof $entityType;
        $checkVersion = $byEntity && $map->versionProperty !== null;

        $sql = SqlPlaceholders::toPdo($this->options->dialect->deleteSql($map, $checkVersion));
        $statement = $this->connection()->prepare($sql);

        $parameters = [];
        if ($byEntity) {
            foreach ($map->keyProperties as $key) {
                $parameters[$key->columnName] = $this->converter->toDatabase(
                    $key->getValue($keyOrEntity),
                    "{$map->entityName()}.{$key->propertyName()}",
                );
            }

            if ($checkVersion) {
                $version = $map->versionProperty;
                $parameters[$version->columnName] = $this->converter->toDatabase(
                    $version->getValue($keyOrEntity),
                    "{$map->entityName()}.{$version->propertyName()}",
                );
            }
        } else {
            $keyValues = self::validateKey($map, $keyOrEntity);
            foreach ($keyValues as $i => $value) {
                $parameters[$map->keyProperties[$i]->columnName] = $this->converter->toDatabase(
                    $value,
                    "{$map->entityName()} key[{$i}]",
                );
            }
        }

        PdoBinder::bindAndExecute($statement, $parameters);

        if ($statement->rowCount() === 0) {
            if ($checkVersion) {
                throw new ConcurrencyException($map->entityName(), 'delete affected no rows: the version is stale or the row is gone');
            }

            throw new SimpleOrmException(
                'CRUD-001',
                $map->entityName(),
                sprintf(
                    'delete affected no rows: no row with key (%s)',
                    $byEntity ? self::formatKeyOf($map, $keyOrEntity) : self::formatKey($keyOrEntity),
                ),
            );
        }
    }

    private function requireWritableKeyed(string $entityType): EntityMap
    {
        $map = $this->maps->load($entityType);
        if (!$map->kind->isWritable()) {
            throw new SimpleOrmException(
                'CRUD-003',
                $map->entityName(),
                "is {$map->kind->value}-backed and read-only; writes need a table",
            );
        }

        if ($map->keyProperties === []) {
            throw new SimpleOrmException('CRUD-002', $map->entityName(), 'the entity defines no key');
        }

        return $map;
    }

    /**
     * The FK property is what is written; a non-null `[ManyToOne]` navigation
     * must agree with it (ADR-0005 add.1) — composite-aware, pairwise in key
     * order (ADR-0019 add.1). An update by column list (ADR-0028) passes the
     * written property names and checks only those FK parts.
     *
     * @param list<string>|null $written
     */
    private function checkNavigationConsistency(EntityMap $map, object $entity, ?array $written = null): void
    {
        foreach ($map->relationships as $relationship) {
            if ($relationship->kind !== RelationshipKind::ManyToOne) {
                continue;
            }

            $navigationProperty = new ReflectionProperty($map->entityType, $relationship->propertyName);
            $navigation = $navigationProperty->isInitialized($entity) ? $navigationProperty->getValue($entity) : null;
            if ($navigation === null) {
                continue;
            }

            $targetMap = $this->maps->load($relationship->targetType);
            if (count($targetMap->keyProperties) !== count($relationship->foreignKeyProperties)) {
                continue;   // arity problems are loader errors, not write-time ones
            }

            foreach ($targetMap->keyProperties as $i => $targetKey) {
                $fkPropertyName = $relationship->foreignKeyProperties[$i];
                if ($written !== null && !in_array($fkPropertyName, $written, true)) {
                    continue;
                }

                $navigationKey = $targetKey->getValue($navigation);
                $foreignKey = $map->property($fkPropertyName)?->getValue($entity);
                if ($navigationKey !== $foreignKey) {
                    throw new SimpleOrmException(
                        'CRUD-004',
                        "{$map->entityName()}.{$relationship->propertyName}",
                        "navigation key {$navigationKey} disagrees with {$fkPropertyName} = {$foreignKey}",
                    );
                }
            }
        }
    }

    private static function newGuid(): string
    {
        $bytes = random_bytes(16);
        $bytes[6] = chr((ord($bytes[6]) & 0x0f) | 0x40);
        $bytes[8] = chr((ord($bytes[8]) & 0x3f) | 0x80);
        $hex = bin2hex($bytes);

        return sprintf(
            '%s-%s-%s-%s-%s',
            substr($hex, 0, 8),
            substr($hex, 8, 4),
            substr($hex, 12, 4),
            substr($hex, 16, 4),
            substr($hex, 20, 12),
        );
    }

    // --- statement-backed entities (ADR-0010): the type IS the query ------------

    /** Runs a `[Statement]`-backed entity's own SQL; args bind against its declared parameters. */
    public function statement(string $resultType, object $args): array
    {
        $statement = $this->createStatementStatement($resultType, $args);

        return $this->materializeAll($statement, $resultType, self::statementName($resultType));
    }

    public function statementSingle(string $resultType, object $args): object
    {
        $rows = $this->statement($resultType, $args);

        return match (count($rows)) {
            1 => $rows[0],
            0 => throw new SimpleOrmException('QRY-001', self::statementName($resultType), 'expected exactly one row, found none'),
            default => throw new SimpleOrmException(
                'QRY-002',
                self::statementName($resultType),
                sprintf('expected exactly one row, found %d', count($rows)),
            ),
        };
    }

    public function statementSingleOrDefault(string $resultType, object $args): ?object
    {
        $rows = $this->statement($resultType, $args);

        return match (count($rows)) {
            0 => null,
            1 => $rows[0],
            default => throw new SimpleOrmException(
                'QRY-002',
                self::statementName($resultType),
                sprintf('expected at most one row, found %d', count($rows)),
            ),
        };
    }

    public function streamStatement(string $resultType, object $args): Generator
    {
        $statement = $this->createStatementStatement($resultType, $args);
        $plan = $this->planFor($statement, $resultType, self::statementName($resultType));
        while (($row = $statement->fetch(PDO::FETCH_ASSOC)) !== false) {
            yield $plan($row);
        }
    }

    private function createStatementStatement(string $resultType, object $args): PDOStatement
    {
        $map = $this->maps->load($resultType);
        if ($map->kind !== RelationKind::Statement) {
            throw new SimpleOrmException(
                'QRY-004',
                $map->entityName(),
                "is {$map->kind->value}-backed, not statement-backed; use the registry or generated CRUD for it",
            );
        }

        // The loader already proved declared parameters == SQL placeholders
        // (PRM-010/011); here the args object must match the declaration in
        // type as well as name.
        $reflection = new ReflectionClass($args);
        foreach ($map->statementParameters as $parameter) {
            $property = null;
            foreach ($reflection->getProperties(ReflectionProperty::IS_PUBLIC) as $candidate) {
                if (!$candidate->isStatic() && strcasecmp($candidate->getName(), $parameter->name) === 0) {
                    $property = $candidate;
                    break;
                }
            }

            if ($property === null) {
                continue;   // PRM-001 fires below, during binding
            }

            $type = $property->getType();
            $phpTypeName = $type instanceof ReflectionNamedType ? $type->getName() : null;
            if ($phpTypeName === null) {
                continue;
            }

            $actual = ColumnType::fromPhpType($phpTypeName);
            if ($actual !== $parameter->type) {
                throw new SimpleOrmException(
                    'PRM-012',
                    self::statementName($resultType) . '.' . $parameter->name,
                    "declared as {$parameter->type->value}, args supply {$phpTypeName}",
                );
            }
        }

        $bound = ParameterBinder::bind(
            (string) $map->definingSql,
            $args,
            self::statementName($resultType),
            $this->converter,
            $this->options->dialect->supportsArrayParameters(),
        );
        $statement = $this->connection()->prepare($bound->sql);
        PdoBinder::bindAndExecute($statement, $bound->parameters);

        return $statement;
    }

    private static function statementName(string $resultType): string
    {
        return self::shortName($resultType) . ' [Statement]';
    }

    // --- shared plumbing ---------------------------------------------------------

    private function createStatement(SqlSource $source, object $args): PDOStatement
    {
        $bound = ParameterBinder::bind(
            $source->sql,
            $args,
            $source->description,
            $this->converter,
            $this->options->dialect->supportsArrayParameters(),
        );
        $statement = $this->connection()->prepare($bound->sql);
        PdoBinder::bindAndExecute($statement, $bound->parameters);

        return $statement;
    }

    /**
     * The one list-materialization loop (§7.11): the plan is built from the
     * statement's column schema before the first row, so strictness
     * (`MAP-001/002/003`) fires even for empty results.
     */
    private function materializeAll(PDOStatement $statement, string $resultType, string $queryName): array
    {
        $plan = $this->planFor($statement, $resultType, $queryName);
        $results = [];
        while (($row = $statement->fetch(PDO::FETCH_ASSOC)) !== false) {
            $results[] = $plan($row);
        }

        return $results;
    }

    private function planFor(PDOStatement $statement, string $resultType, string $queryName): callable
    {
        $columns = [];
        for ($i = 0; $i < $statement->columnCount(); $i++) {
            $meta = $statement->getColumnMeta($i);
            $columns[] = $meta !== false ? $meta['name'] : (string) $i;
        }

        return $this->mapper->createPlan($resultType, $columns, $queryName);
    }

    private static function formatKeyOf(EntityMap $map, object $entity): string
    {
        $values = $map->getKeyValues($entity);

        return implode(', ', array_map(self::stringifyKeyPart(...), $values));
    }

    private static function formatKey(mixed $key): string
    {
        return is_array($key)
            ? implode(', ', array_map(self::stringifyKeyPart(...), $key))
            : self::stringifyKeyPart($key);
    }

    private static function stringifyKeyPart(mixed $value): string
    {
        return match (true) {
            is_scalar($value) => (string) $value,
            $value instanceof Stringable => (string) $value,
            default => get_debug_type($value),
        };
    }

    private static function shortName(string $className): string
    {
        $slash = strrpos($className, '\\');

        return $slash === false ? $className : substr($className, $slash + 1);
    }

    /** Rolls back an active transaction and releases the connection (PHP has no explicit PDO close; dropping the reference does). */
    public function close(): void
    {
        if ($this->connection === null) {
            return;
        }

        if ($this->connection->inTransaction()) {
            $this->connection->rollBack();
        }

        $this->connection = null;
    }

    public function __destruct()
    {
        $this->close();
    }
}
