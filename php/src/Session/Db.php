<?php

declare(strict_types=1);

namespace SimpleOrm\Session;

use Closure;
use Generator;
use InvalidArgumentException;
use LogicException;
use PDO;
use PDOStatement;
use ReflectionClass;
use ReflectionNamedType;
use ReflectionProperty;
use SimpleOrm\Dialect\BinaryText;
use SimpleOrm\Dialect\PdoConventions;
use SimpleOrm\Dialect\TemporalText;
use SimpleOrm\Errors\ConcurrencyException;
use SimpleOrm\Errors\SimpleOrmException;
use SimpleOrm\Mapping\ResultMapper;
use SimpleOrm\Mapping\TypeConverter;
use SimpleOrm\Metadata\ColumnType;
use SimpleOrm\Metadata\EntityMap;
use SimpleOrm\Metadata\EntityMapLoader;
use SimpleOrm\Metadata\KeyStrategy;
use SimpleOrm\Metadata\KeyTuple;
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
use Throwable;

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

    /** The identity map and snapshots; null in an untracked session (the default, ADR-0034 add.1). */
    private readonly ?DbTracking $tracking;

    /** The schedule and the ordered flush; null in an untracked session — the unit of work needs the map and the snapshots (ADR-0034 §7c M2). */
    private readonly ?DbUnitOfWork $unitOfWork;

    /** The test hook of {@see observeMessages()}; null in every real session. */
    private ?Closure $messageObserver = null;

    /** The statement log's one funnel (ADR-0045): every statement this session sends is written through it. */
    private readonly StatementLog $log;

    /** @var array<string, string> the registry names SchemaGuard learned, by entry kind and SQL text (see {@see declareEntryName()}) */
    private array $entryNames = [];

    private function __construct(
        PDO $connection,
        private readonly DbOptions $options,
    ) {
        $this->connection = $connection;
        $this->log = new StatementLog($options->log, $options->logParameterValues);
        $this->maps = new EntityMapLoader($options->mapping);
        $this->converter = new TypeConverter(
            $options->typeHandlers,
            $options->dialect->bindsTemporalsNatively(),
            $options->dialect instanceof PdoConventions ? $options->dialect->temporalText() : TemporalText::Iso8601,
            $options->dialect instanceof PdoConventions ? $options->dialect->binaryText() : BinaryText::Raw,
        );
        $this->mapper = new ResultMapper($this->maps, $this->converter);
        $this->loading = new DbLoading($this);
        $this->eagerJoin = new DbEagerJoin($this);
        $this->tracking = $options->tracking === TrackingMode::Tracked ? new DbTracking($this->maps, $this->converter) : null;
        $this->unitOfWork = $this->tracking !== null ? new DbUnitOfWork($this, $this->tracking) : null;
    }

    /**
     * Opens the connection now (§7.17): a failed open leaves nothing behind. The options are checked first — a
     * `maxStatementsPerMessage` below 1 (ADR-0044), a negative `openRetries`/`openRetryDelay`, a negative or non-finite
     * `commandTimeout` (ADR-0047) are the language's argument error, raised before any attempt.
     *
     * The open, as spec/session.md "Resilience" states it: each attempt asks the dialect for a fresh connection (in PDO the
     * constructor opens it); a failure is attempted again only while attempts remain (`openRetries` more after the first)
     * **and** the dialect calls it transient, after a fixed `openRetryDelay`; otherwise the failure is rethrown as raised —
     * the same object, the last attempt's when retries were spent. PHP has no cancellation token and no cancellation signal
     * (CODING-STANDARD §10), so nothing else ends the retries. Only the open is retried, never a statement — the command
     * timeout's one `set_config` on PostgreSQL included. Nothing is logged for an attempt: an attempt sends no statement.
     */
    public static function open(string $connectionString, DbOptions $options): self
    {
        if ($options->maxStatementsPerMessage < 1) {
            throw new InvalidArgumentException(
                "maxStatementsPerMessage must be at least 1, got {$options->maxStatementsPerMessage}",
            );
        }

        if ($options->openRetries < 0) {
            throw new InvalidArgumentException("openRetries must not be negative, got {$options->openRetries}");
        }

        if (is_float($options->openRetryDelay) && (is_nan($options->openRetryDelay) || is_infinite($options->openRetryDelay))) {
            throw new InvalidArgumentException('openRetryDelay must be a finite number of seconds');
        }

        if ($options->openRetryDelay < 0) {
            throw new InvalidArgumentException("openRetryDelay must not be negative, got {$options->openRetryDelay}");
        }

        CommandTimeout::validate($options->commandTimeout);

        $connection = self::openWithRetries($connectionString, $options);
        try {
            CommandTimeout::applyTo($connection, $options->commandTimeout);
        } catch (Throwable $failure) {
            $connection = null;   // released: the session never existed

            throw $failure;
        }

        return new self($connection, $options);
    }

    /** Steps 2.1–2.4 of the open algorithm (spec/session.md "Resilience"). */
    private static function openWithRetries(string $connectionString, DbOptions $options): PDO
    {
        for ($attempt = 0; ; $attempt++) {
            try {
                return $options->dialect->createConnection($connectionString);
            } catch (Throwable $failure) {
                // The failed attempt's connection never left the dialect: nothing to release here.
                if ($attempt >= $options->openRetries || !$options->dialect->isTransient($failure)) {
                    throw $failure;
                }
            }

            if ($options->openRetryDelay > 0) {
                usleep((int) round($options->openRetryDelay * 1_000_000));
            }
        }
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

    /**
     * The statement log of this session (ADR-0045): the funnel the migration runner, SchemaGuard, and the schema tooling
     * write through when they run on this session's connection.
     */
    public function log(): StatementLog
    {
        return $this->log;
    }

    /**
     * Teaches the session what the registry calls an entry (`Queries.userByEmail`), so the statement log names the lines of
     * later calls by it. SchemaGuard calls this when it enumerates the registry (spec/session.md "Statement log": the
     * implementations whose registry has no variable names learn them at the scan). Entries are looked up by their kind and SQL
     * text — a registry method builds a fresh `Query` on every call (CODING-STANDARD §10), so there is no entry object to stamp;
     * two entries with the same SQL keep the name learned first.
     *
     * @param 'query'|'command' $kind
     * @internal called by SchemaGuard
     */
    public function declareEntryName(string $kind, string $sql, string $name): void
    {
        $this->entryNames[$kind . "\0" . $sql] ??= $name;
    }

    /** What the log calls a registry entry: the name SchemaGuard learned, else the file path of a `fromFile` entry, else its kind. */
    private function entryName(string $kind, SqlSource $source): string
    {
        return $this->entryNames[$kind . "\0" . $source->sql] ?? $source->path ?? $kind;
    }

    // --- registry surface (§6, spec/session.md) ---------------------------------

    /**
     * @return list<mixed> a registry query's result type may be an entity, a
     *     DTO, or a scalar (`int`, `string`, …) — mirrors dotnet's generic
     *     `TResult`, which C# lets be a value type too (CODING-STANDARD §10)
     */
    public function query(Query $query, object $args): array
    {
        $statement = $this->createStatement($query->source, $args, 'query');

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
        $statement = $this->createStatement($query->source, $args, 'query');
        $plan = $this->planFor($statement, $query->resultType, $query->source->description);
        while (($row = $statement->fetch(PDO::FETCH_ASSOC)) !== false) {
            yield $plan($row);
        }
    }

    /** A non-query command; returns the affected-row count. */
    public function execute(Command $command, object $args): int
    {
        return $this->createStatement($command->source, $args, 'command')->rowCount();
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
        // The identity map and snapshots are untouched by a rollback; `refresh()` is the way back
        // (spec/session.md Clarifications, ADR-0035: "transactions do not touch the map").
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
        $queryName = self::criteriaQueryName($ast->map);
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
     * else goes through {@see executeAst()}. `$logKind` is the statement log's
     * suffix after the entity name (`criteria`, or `include` for a join-mode eager query).
     *
     * @internal
     */
    public function executeAstStatement(SelectAst $ast, string $queryName, string $logKind = 'criteria'): PDOStatement
    {
        $converter = $this->converter;
        $parameters = [];
        $bindParameter = static function (mixed $value, ?PropertyMap $property) use (&$parameters, $converter, $queryName): string {
            $name = 'c' . count($parameters);
            $parameters[$name] = $converter->toParameter($value, "{$queryName} @{$name}", $property?->enumAsInt() ?? false, $property?->type);

            return '@' . $name;
        };

        $sql = $this->options->dialect->selectSql($ast, $bindParameter);
        $statement = $this->connection()->prepare(SqlPlaceholders::toPdo($sql));
        $this->log->write("{$ast->map->entityName()}.{$logKind}", $sql, $parameters);
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
        $statement = $this->selectByKey($map, $key);

        return $this->singleRow($map, $statement, $this->planFor($statement, $entityType, $map->entityName() . ' get'));
    }

    /**
     * Executes the generated key select (ADR-0006): explicit column list, every key column ANDed.
     * Shared by {@see getOrDefault()} (through the identity map when tracked) and
     * {@see refresh()} (the raw row, never adopted).
     */
    private function selectByKey(EntityMap $map, mixed $key): PDOStatement
    {
        $this->requireNamedRelation($map, 'key reads need a named relation');

        $keyValues = self::validateKey($map, $key);
        $dialect = $this->options->dialect;

        $predicates = [];
        $parameters = [];
        foreach ($keyValues as $i => $value) {
            $predicates[] = $dialect->quoteIdentifier($map->keyProperties[$i]->columnName) . " = @k{$i}";
            $parameters["k{$i}"] = $this->converter->toParameter($value, "{$map->entityName()} key[{$i}]", type: $map->keyProperties[$i]->type);
        }

        $sql = 'select ' . implode(', ', array_map(
            static fn (PropertyMap $p): string => $dialect->quoteIdentifier($p->columnName),
            $map->properties,
        )) . ' from ' . $dialect->quoteIdentifier((string) $map->relationName)
            . ' where ' . implode(' and ', $predicates);

        $statement = $this->connection()->prepare(SqlPlaceholders::toPdo($sql));
        $this->log->write("{$map->entityName()}.get", $sql, $parameters);
        PdoBinder::bindAndExecute($statement, $parameters);

        return $statement;
    }

    /** The one row of a key select, or null; a key matching more than one row is `QRY-002`. */
    private function singleRow(EntityMap $map, PDOStatement $statement, callable $plan): ?object
    {
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

    // --- tracking: identity map and change snapshots (ADR-0034 add.1, spec/session.md "Tracking") -----

    /** Whether the session opened with {@see TrackingMode::Tracked}. */
    public function isTracking(): bool
    {
        return $this->tracking !== null;
    }

    /** Whether the session's identity map holds this very instance; never throws, false in an untracked session. */
    public function isTracked(object $entity): bool
    {
        return $this->tracking?->isTracked($entity) ?? false;
    }

    /**
     * The mapped property names whose value differs from the snapshot — dotted for owned members
     * (`address.city`), in declaration order. `UOW-001` in an untracked session and for an
     * instance the session does not track.
     *
     * @return list<string>
     */
    public function changesOf(object $entity): array
    {
        return ($this->tracking ?? throw DbTracking::untrackedSession($entity))->changesOf($entity);
    }

    /** True when {@see changesOf()} is not empty; the same `UOW-001` refusals. */
    public function isDirty(object $entity): bool
    {
        return $this->changesOf($entity) !== [];
    }

    /**
     * The explicit overwrite (ADR-0034 add.1 ruling 2): re-reads the row by the instance's key,
     * applies every mapped value onto the instance, and resets its snapshot; a missing row is
     * `CRUD-001`. Works in both modes. Loaded navigations stay as they are. An instance that is not
     * the session's instance for its key — a second copy, or a tracked instance whose key was
     * changed — is `UOW-002`; an unknown instance with a free key has the row applied and is not
     * registered.
     */
    public function refresh(object $entity): void
    {
        $map = $this->maps->load($entity::class);
        $keyValues = $map->getKeyValues($entity);
        // The identity gate (spec/session.md Clarifications, ADR-0035): a tracked instance whose key was
        // changed, or one that is not the session's instance for its key, is UOW-002; an instance the
        // session does not know, with a free key, has the row applied and is not registered.
        $entry = $this->tracking?->identityFor($map, $entity);

        $statement = $this->selectByKey($map, $keyValues);
        $fresh = $this->singleRow($map, $statement, $this->rawPlanFor($statement, $entity::class, $map->entityName() . ' refresh'));
        if ($fresh === null) {
            throw new SimpleOrmException(
                'CRUD-001',
                $map->entityName(),
                sprintf('refresh found no row with key (%s)', self::formatKey($keyValues)),
            );
        }

        self::copyMappedValues($map, $fresh, $entity);
        if ($entry !== null) {
            $this->tracking?->resnapshot($entry);
        }
    }

    // --- the unit of work (Level 3 M2, spec/unit-of-work.md) ----------------------

    /**
     * Schedules an insert for an instance the session does not track (a tracked one is already managed:
     * its changes flush as updates), cascading over the initialized navigations that declare `persist`.
     * Nothing is written until {@see flush()}. `UOW-003` in an untracked session; `UOW-006` for an
     * instance scheduled for removal; `CRUD-003`/`CRUD-002` for one that is not table-backed and keyed.
     */
    public function persist(object $entity): void
    {
        $this->unitOfWork()->persist($entity);
    }

    /**
     * Schedules a delete for a tracked instance (`UOW-001` for any other), cascading over the navigations
     * that declare `remove`; removing an instance scheduled for insert cancels the insert. A never-loaded
     * collection under a remove cascade is `UOW-005`; a refusal anywhere in the cascade schedules nothing.
     */
    public function remove(object $entity): void
    {
        $this->unitOfWork()->remove($entity);
    }

    /**
     * Writes the schedule in one explicit step: inserts in foreign-key order with generated keys flowing
     * into dependents, then every dirty tracked instance as a column-list update, then deletes children
     * first — through this session's own `insert`/`update`/`delete`, inside the active transaction, else
     * one the flush owns (rolled back on failure). A failed flush keeps the schedule and undoes its in-memory
     * effects (keys, tracking, snapshots, versions), so it flushes again as it was. `UOW-004` for a cycle,
     * before any SQL.
     */
    public function flush(): FlushSummary
    {
        return $this->unitOfWork()->flush();
    }

    /**
     * Whether anything is scheduled (dirty tracked instances flush regardless). A question, not a unit-of-work
     * call: false on an untracked session, never `UOW-003` (spec/unit-of-work.md Clarifications, ADR-0036).
     */
    public function hasPendingWork(): bool
    {
        return $this->unitOfWork?->hasPendingWork() ?? false;
    }

    // --- detach and merge (Level 3 M3, spec/unit-of-work.md "Detach and merge") ---------------------

    /**
     * Makes the session forget the instance: it is no longer the session's instance for its key, a scheduled
     * insert is cancelled (with the inserts its persist cascade scheduled) and a scheduled removal dropped. The
     * instance keeps its values. `UOW-003` in an untracked session; `UOW-001` for an instance the session neither
     * tracks nor has scheduled.
     */
    public function detach(object $entity): void
    {
        $this->unitOfWork()->detach($entity);
    }

    /**
     * Brings a detached copy's state back and returns the session's instance for its key: the tracked one when the
     * key is known, otherwise the row read now (one visible round trip; no row is `CRUD-001`). Every mapped non-key
     * value of the copy — the version included — is written onto that instance, which is dirty where the values
     * differed; navigations are not merged and the copy stays untracked. An instance without a complete key is
     * `CRUD-002` (new instances are persisted, not merged); merging the session's own instance returns it.
     * `UOW-003` in an untracked session.
     */
    public function merge(object $detached): object
    {
        $tracking = $this->tracking ?? throw self::untrackedUnitOfWork();
        if ($tracking->isTracked($detached)) {
            return $detached;
        }

        // A table-backed, keyed entity (CRUD-003/CRUD-002), decided at merge time — a view's copy cannot be merged
        // (spec/unit-of-work.md "Clarifications on detach and merge").
        $map = $this->requireWritableKeyed($detached::class);
        $keyValues = $map->getKeyValues($detached);
        if ((new KeyTuple($keyValues))->hasNullPart() || self::isUnsetGeneratedKey($map, $keyValues)) {
            throw new SimpleOrmException(
                'CRUD-002',
                $map->entityName(),
                'the instance has no complete key; merge brings back a copy of a row — new instances are persisted, not merged',
            );
        }

        $held = $tracking->heldInstance($map, $keyValues) ?? $this->get($detached::class, $keyValues);
        self::copyMappedValues($map, $detached, $held, $map->keyProperties);

        return $held;
    }

    /**
     * An unset key beyond a null part: a database-generated key at its default (an explicit `0` is not a key value) or an
     * empty client-generated GUID; a natural key's default value is a key (spec/unit-of-work.md "Clarifications on detach
     * and merge").
     *
     * @param list<mixed> $keyValues
     */
    private static function isUnsetGeneratedKey(EntityMap $map, array $keyValues): bool
    {
        return match ($map->keyStrategy) {
            KeyStrategy::DatabaseGenerated => $keyValues[0] === 0,
            KeyStrategy::ClientGuid => $keyValues[0] === '',
            default => false,
        };
    }

    private function unitOfWork(): DbUnitOfWork
    {
        return $this->unitOfWork ?? throw self::untrackedUnitOfWork();
    }

    /** The `UOW-003` refusal every unit-of-work, detach, and merge call shares in an untracked session. */
    private static function untrackedUnitOfWork(): SimpleOrmException
    {
        return new SimpleOrmException(
            'UOW-003',
            'session',
            'the unit of work needs the identity map and the snapshots: open the session with TrackingMode::Tracked',
        );
    }

    /**
     * Applies every mapped value of `$source` onto `$target` except the `$skip` properties (merge leaves the key alone); an
     * owned navigation is replaced as a whole (null stays null, ADR-0030) by a fresh owned instance whose members are copied
     * one by one, so nothing is aliased. A property the source never initialized has no value to copy and is left as it is
     * on the target (spec/hooks.md Clarifications).
     *
     * @param list<PropertyMap> $skip
     */
    private static function copyMappedValues(EntityMap $map, object $source, object $target, array $skip = []): void
    {
        foreach ($map->properties as $property) {
            if ($property->owner === null && !in_array($property, $skip, true) && $property->property->isInitialized($source)) {
                $property->setValue($target, $property->getValue($source));
            }
        }

        foreach ($map->ownedTypes as $owner) {
            $owned = $owner->property->isInitialized($source) ? $owner->property->getValue($source) : null;
            $fresh = $owned === null ? null : new ($owner->ownedType)();
            foreach ($owned === null ? [] : $owner->members() as $member) {
                if ($member->property->isInitialized($owned)) {
                    $member->property->setValue($fresh, $member->property->getValue($owned));
                }
            }

            $owner->property->setValue($target, $fresh);
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

        $this->exec("{$map->entityName()}.createTable", $this->options->dialect->createTableSql($map));
        foreach ($this->options->dialect->createIndexSql($map) as $indexSql) {
            $this->exec("{$map->entityName()}.createIndex", $indexSql);
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

        $this->exec("{$map->entityName()}.createView", $this->options->dialect->createViewSql($map));
        foreach ($this->options->dialect->createIndexSql($map) as $indexSql) {
            $this->exec("{$map->entityName()}.createIndex", $indexSql);
        }
    }

    /** One parameterless statement (metadata DDL) on the session's connection: logged as it is sent, then run. */
    private function exec(string $name, string $sql): void
    {
        $this->log->write($name, $sql);
        $this->connection()->exec($sql);
    }

    /**
     * Creates the triggers the entity links, from their declarations (Level 4, ADR-0042) — a dev/test utility like
     * {@see self::createTable()}, separate from it: each trigger is dropped if present, then created (**replace**
     * semantics, so it is safe to call again), through the dialect (`DDL-005` where it has no form for a declaration).
     * Versioned migrations stay the schema-evolution path (`TriggerMigration`). Only tables and views take triggers
     * (`DDL-001`); an entity that links none is a no-op.
     */
    public function createTriggers(string $entityType): void
    {
        $map = $this->maps->load($entityType);
        if ($map->kind !== RelationKind::Table && $map->kind !== RelationKind::View) {
            throw new SimpleOrmException(
                'DDL-001',
                $map->entityName(),
                "is {$map->kind->value}-backed; only tables (and views, for INSTEAD OF) take triggers",
            );
        }

        $dialect = $this->options->dialect;
        $relation = (string) $map->relationName;
        // Rendered before anything runs: a form the dialect lacks (DDL-005) must refuse with nothing half-created.
        $statements = [];
        foreach ($map->triggers as $trigger) {
            array_push(
                $statements,
                ...$dialect->dropTriggerSql($relation, $trigger->definition->name),
                ...$dialect->createTriggerSql($relation, $map->kind === RelationKind::View, $trigger->definition),
            );
        }

        foreach ($statements as $sql) {
            $this->exec("{$map->entityName()}.createTrigger", $sql);
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
        $this->log->write("{$map->entityName()}.selectAll", $sql);
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
        $this->runImmediate($this->prepareInsert($entity));
    }

    /**
     * Runs one prepared statement on its own round trip — a message of one, the Level 0–3 path of every immediate write
     * (ADR-0044): execute, read what the statement returns, let it judge and apply that. Anything that fails puts back what
     * preparing moved on the instance and propagates unchanged.
     */
    private function runImmediate(PreparedWrite $write): void
    {
        try {
            $statement = $this->connection()->prepare(SqlPlaceholders::toPdo($write->sql));
            $this->log->write($write->name, $write->sql, $write->parameters);
            PdoBinder::bindAndExecute($statement, $write->parameters);
            $write->complete(ResultReader::read($statement, $write->result, $this->options->dialect->reportsAffectedRowsBySelect()));
        } catch (Throwable $failure) {
            $write->undo();

            throw $failure;
        }
    }

    /**
     * The prepare half of an insert (ADR-0044): the gates, the client key, the created-at fill, the rendered SQL, and the
     * converted row. Completing it writes a database-generated key back onto the entity and registers the instance.
     */
    private function prepareInsert(object $entity): PreparedWrite
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

        if ($map->keyStrategy !== KeyStrategy::DatabaseGenerated) {
            // A caller-supplied key held by another instance is UOW-002 before any SQL; a generated key
            // has nothing to check yet (spec/session.md Clarifications, ADR-0035).
            $this->tracking?->identityFor($map, $entity);
        }

        $sql = $this->options->dialect->insertSql($map);

        // The hooks run after the gates (spec/hooks.md Clarifications); a failed statement puts created-at back to unset.
        $restoreCreatedAt = $this->fillCreatedAt($map, $entity);
        try {
            $parameters = $this->insertParameters($map, $entity);
        } catch (Throwable $failure) {
            $restoreCreatedAt();

            throw $failure;
        }

        // The key is column 0 of the first result that has columns: `returning` yields it first on SQLite and
        // PostgreSQL; SQL Server's unnamed `scope_identity()` scalar follows the insert's own row-count result
        // as the batch's next rowset (spec/dialects.md "Generated keys").
        $generatedKey = $map->keyStrategy === KeyStrategy::DatabaseGenerated ? $map->keyProperties[0] : null;
        $restoreKey = $generatedKey?->remember($entity);

        return new PreparedWrite(
            sql: $sql,
            parameters: $parameters,
            result: $generatedKey !== null ? ResultKind::Key : ResultKind::None,
            complete: function (mixed $key) use ($map, $entity, $generatedKey): int {
                if ($generatedKey !== null) {
                    $generatedKey->setValue($entity, $this->converter->fromDatabase(
                        $key,
                        $generatedKey->type,
                        $generatedKey->phpType,
                        "{$map->entityName()}.{$generatedKey->propertyName()}",
                    ));
                }

                $this->tracking?->register($map, $entity);

                return 1;
            },
            undo: static function () use ($restoreKey, $restoreCreatedAt): void {
                if ($restoreKey !== null) {
                    $restoreKey();
                }

                $restoreCreatedAt();
            },
            instance: $entity,
            insertsGeneratedKey: $generatedKey !== null,
            identity: $this->entityIdentity($map, $entity),
            name: "{$map->entityName()}.insert",
        );
    }

    /** The row an insert binds: every non-generated column, converted by its property's rules. @return array<string, mixed> */
    private function insertParameters(EntityMap $map, object $entity): array
    {
        $parameters = [];
        foreach ($map->properties as $property) {
            if ($property->generated) {
                continue;
            }

            $parameters[$property->columnName] = $this->converter->toParameter(
                $property->getValue($entity),
                "{$map->entityName()}.{$property->propertyName()}",
                $property->enumAsInt(),
                $property->type,
            );
        }

        return $parameters;
    }

    /**
     * Generated full-row update by key (§7.15): every mapped non-key column.
     * With a version column (§7.16): `version = version + 1`, requires the
     * entity's version in the WHERE, throws {@see ConcurrencyException}
     * (`CRUD-010`) on zero rows, and bumps the entity's version on success.
     * Without one, zero rows is `CRUD-001`. For a narrower SET see {@see updateOnly()}.
     *
     * On a **tracked** instance (ADR-0034 add.1) it writes only the dirty columns through the
     * column-list path and nothing at all — no statement, no version bump — when the instance is
     * clean; on an instance the tracked session does not know it writes the full row and then
     * tracks it. An instance that is not the session's instance for its key is `UOW-002`.
     */
    public function update(object $entity): void
    {
        $write = $this->prepareUpdate($entity);
        if ($write !== null) {
            $this->runImmediate($write);
        }
    }

    /** The prepare half of {@see update()}: null when a clean tracked instance issues no statement. */
    private function prepareUpdate(object $entity): ?PreparedWrite
    {
        $map = $this->requireWritableKeyed($entity::class);
        // Full-row semantics for validation: every navigation is checked (CRUD-004)
        // before the identity gate and before the snapshot decides what to write
        // (spec/session.md Clarifications, ADR-0035).
        $this->checkNavigationConsistency($map, $entity);
        $entry = $this->tracking?->identityFor($map, $entity);

        if ($entry !== null) {
            return $this->prepareDirtyUpdate($map, $entry);
        }

        // Bind SET values, key values, and the current version for the WHERE;
        // database-generated non-key columns are never written.
        return $this->updateWrite(
            $map,
            $entity,
            $entity,
            $this->options->dialect->updateSql($map),
            array_values(array_filter(
                $map->properties,
                static fn (PropertyMap $p): bool => $p->key || $p->version || !$p->generated,
            )),
            function () use ($map, $entity): void {
                $this->tracking?->register($map, $entity);
            },
        );
    }

    /**
     * The tracked update (ADR-0034 add.1): the dirty properties an update may write — never a
     * key, the version, or a database-generated column — through the ADR-0028 statement; none
     * dirty means no statement. The snapshot moves forward only for what was written (and the
     * bumped version), so an unwritten change can never look clean.
     */
    private function prepareDirtyUpdate(EntityMap $map, TrackedInstance $entry): ?PreparedWrite
    {
        $dirty = $this->tracking?->writableDirtyProperties($entry) ?? [];
        // Only key/version/generated values differing is clean: no statement, no snapshot change
        // (spec/session.md Clarifications, ADR-0035). The CRUD-004 check likewise covers only the
        // FK properties actually written, so a clean instance is never refused.
        if ($dirty === []) {
            return null;
        }

        // The [UpdatedAt] stamp joins the SET of a write that changes the row — but was never what made the
        // instance dirty (spec/hooks.md), so it is appended here, after the caller-visible columns.
        $set = $this->withUpdatedAt($map, $dirty);
        $entity = $entry->instance;
        $bound = array_merge($set, $map->keyProperties);
        if ($map->versionProperty !== null) {
            $bound[] = $map->versionProperty;
        }

        return $this->updateWrite(
            $map,
            $entity,
            $entity,
            $this->options->dialect->updateOnlySql($map, $set),
            $bound,
            function () use ($entry, $set): void {
                $this->tracking?->resnapshotWritten($entry, $set);
            },
        );
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
        $this->runImmediate($this->prepareUpdateOnly($entity, $properties));
    }

    /**
     * The prepare half of {@see updateOnly()}.
     *
     * @param list<string> $properties
     */
    private function prepareUpdateOnly(object $entity, array $properties): PreparedWrite
    {
        $map = $this->requireWritableKeyed($entity::class);
        $set = self::resolveUpdateList($map, $properties);
        $listed = array_map(static fn (PropertyMap $p): string => $p->propertyName(), $set);
        $this->checkNavigationConsistency($map, $entity, $listed);
        // List (CRUD-005/006/007), then navigation (CRUD-004), then identity (UOW-002): the order of
        // refusals of spec/session.md Clarifications (ADR-0035). An unknown instance is not registered —
        // the session saw only part of its row.
        $entry = $this->tracking?->identityFor($map, $entity);

        // The [UpdatedAt] column joins the SET even when not listed (spec/hooks.md); listed or not it takes the clock.
        $set = $this->withUpdatedAt($map, $set);
        $bound = array_merge($set, $map->keyProperties);
        if ($map->versionProperty !== null) {
            $bound[] = $map->versionProperty;
        }

        return $this->updateWrite(
            $map,
            $entity,
            $entity,
            $this->options->dialect->updateOnlySql($map, $set),
            $bound,
            function () use ($entry, $set): void {
                if ($entry !== null) {
                    $this->tracking?->resnapshotWritten($entry, $set);
                }
            },
            operation: 'updateOnly',
        );
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
     * The prepare half shared by every update and soft delete (§7.15–16): stamp `[UpdatedAt]` (every write that changes
     * a row takes the session clock, spec/hooks.md) and convert the bound values. Completing it judges the statement's
     * count — zero rows is `CRUD-010` with a version column, `CRUD-001` without — bumps the version, and runs `$applied`
     * (the tracking step). Undoing it puts the previous stamp back, so a refused write leaves no value behind that an
     * unwritten row never had, plus whatever `$undoExtra` restores.
     *
     * @param list<PropertyMap> $bound
     * @param object|null $planned the instance plan rule 1a keys on (null for a by-key soft delete's scratch instance)
     * @param Closure(): void $applied
     * @param (Closure(): void)|null $undoExtra
     * @param string $operation the log's suffix after the entity name: `update`, `updateOnly`, or `delete` (the soft delete)
     */
    private function updateWrite(
        EntityMap $map,
        object $entity,
        ?object $planned,
        string $sql,
        array $bound,
        Closure $applied,
        ?Closure $undoExtra = null,
        string $operation = 'update',
    ): PreparedWrite {
        // A failed write leaves the instance as it was (spec/hooks.md Clarifications): the previous stamp is put back, so a
        // refused write leaves nothing a row never held. The clock is read after the callers' gates.
        $stamp = $map->updatedAtProperty();
        $restoreStamp = $stamp?->remember($entity);
        $stamp?->setValue($entity, $this->options->now());
        try {
            $parameters = $this->updateParameters($map, $entity, $bound);
        } catch (Throwable $failure) {
            if ($restoreStamp !== null) {
                $restoreStamp();
            }

            throw $failure;
        }

        return new PreparedWrite(
            sql: $sql,
            parameters: $parameters,
            result: ResultKind::Count,
            complete: function (mixed $count) use ($map, $entity, $applied): int {
                $this->judgeUpdate($map, $entity, (int) $count);
                $applied();

                return (int) $count;
            },
            undo: static function () use ($restoreStamp, $undoExtra): void {
                if ($restoreStamp !== null) {
                    $restoreStamp();
                }

                if ($undoExtra !== null) {
                    $undoExtra();
                }
            },
            instance: $planned,
            identity: $this->entityIdentity($map, $entity),
            name: "{$map->entityName()}.{$operation}",
        );
    }

    /**
     * @param list<PropertyMap> $bound
     * @return array<string, mixed>
     */
    private function updateParameters(EntityMap $map, object $entity, array $bound): array
    {
        $parameters = [];
        foreach ($bound as $property) {
            $parameters[$property->columnName] = $this->converter->toParameter(
                $property->getValue($entity),
                "{$map->entityName()}.{$property->propertyName()}",
                $property->enumAsInt(),
                $property->type,
            );
        }

        return $parameters;
    }

    /**
     * Judges an update's affected-row count — the one number `CRUD-010` and `CRUD-001` read (ADR-0040 add.3; the driver's, or
     * the dialect's reported count, by the time it gets here) — and bumps the in-memory version on success.
     */
    private function judgeUpdate(EntityMap $map, object $entity, int $count): void
    {
        if ($count === 0) {
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
     * (`CRUD-010`). On an entity declaring `#[SoftDelete]` (Level 3 M3, spec/hooks.md) either form is
     * instead the column-list update that sets the flag and stamps `#[UpdatedAt]` — same refusals, and
     * `CRUD-012` for the by-key form of a versioned entity.
     */
    public function delete(string $entityType, mixed $keyOrEntity): void
    {
        $this->runImmediate($this->prepareDelete($entityType, $keyOrEntity));
    }

    /** The prepare half of {@see delete()}: both forms, and the soft-delete update where the entity declares the flag. */
    private function prepareDelete(string $entityType, mixed $keyOrEntity): PreparedWrite
    {
        $map = $this->requireWritableKeyed($entityType);

        $byEntity = is_object($keyOrEntity) && $keyOrEntity instanceof $entityType;
        $checkVersion = $byEntity && $map->versionProperty !== null;
        if ($byEntity) {
            // The entity form passes the identity gate; the by-key form has no instance to gate and
            // forgets whichever instance holds the key (spec/session.md Clarifications, ADR-0035).
            $this->tracking?->identityFor($map, $keyOrEntity);
        }

        $flag = $map->softDeleteProperty();
        if ($flag !== null) {
            return $this->prepareSoftDelete($map, $flag, $keyOrEntity, $byEntity);
        }

        $sql = $this->options->dialect->deleteSql($map, $checkVersion);

        $parameters = [];
        $identity = null;
        if ($byEntity) {
            $identity = $this->entityIdentity($map, $keyOrEntity);
            foreach ($map->keyProperties as $key) {
                $parameters[$key->columnName] = $this->converter->toParameter(
                    $key->getValue($keyOrEntity),
                    "{$map->entityName()}.{$key->propertyName()}",
                    type: $key->type,
                );
            }

            if ($checkVersion) {
                $version = $map->versionProperty;
                $parameters[$version->columnName] = $this->converter->toParameter(
                    $version->getValue($keyOrEntity),
                    "{$map->entityName()}.{$version->propertyName()}",
                    type: $version->type,
                );
            }
        } else {
            $keyValues = self::validateKey($map, $keyOrEntity);
            $identity = self::writeIdentity($map, $keyValues);
            foreach ($keyValues as $i => $value) {
                $parameters[$map->keyProperties[$i]->columnName] = $this->converter->toParameter(
                    $value,
                    "{$map->entityName()} key[{$i}]",
                    type: $map->keyProperties[$i]->type,
                );
            }
        }

        return new PreparedWrite(
            sql: $sql,
            parameters: $parameters,
            result: ResultKind::Count,
            complete: function (mixed $count) use ($map, $keyOrEntity, $byEntity, $checkVersion): int {
                if ((int) $count === 0) {
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

                // A delete forgets the key: the deleted instance is no longer tracked.
                $this->tracking?->forget(
                    $map,
                    $byEntity ? $map->getKeyValues($keyOrEntity) : self::validateKey($map, $keyOrEntity),
                    $byEntity ? $keyOrEntity : null,
                );

                return (int) $count;
            },
            undo: static function (): void {
                // A hard delete moves nothing on the instance.
            },
            instance: $byEntity ? $keyOrEntity : null,
            identity: $identity,
            name: "{$map->entityName()}.delete",
        );
    }

    /**
     * A delete on a `#[SoftDelete]` entity (spec/hooks.md): the column-list update of [flag, stamp] with the version
     * rules of an update, through the one update path. The entity form sets the flag (and stamp) on the instance —
     * restored if the statement is refused — and is version-checked (`CRUD-010`); the by-key form carries the key,
     * flag, and stamp on a scratch instance and refuses a versioned entity (`CRUD-012`: it cannot bump a version it
     * does not know). Zero rows is `CRUD-001`. Either way the key is forgotten afterwards, like any deleted row.
     */
    private function prepareSoftDelete(EntityMap $map, PropertyMap $flag, mixed $keyOrEntity, bool $byEntity): PreparedWrite
    {
        $instance = $byEntity ? $keyOrEntity : $this->scratchForKey($map, $keyOrEntity);
        $restoreFlag = $flag->remember($instance);
        $flag->setValue($instance, true);

        $set = $this->withUpdatedAt($map, [$flag]);
        $bound = array_merge($set, $map->keyProperties);
        if ($map->versionProperty !== null) {
            $bound[] = $map->versionProperty;
        }

        try {
            return $this->updateWrite(
                $map,
                $instance,
                $byEntity ? $instance : null,
                $this->options->dialect->updateOnlySql($map, $set),
                $bound,
                function () use ($map, $instance, $byEntity): void {
                    $this->tracking?->forget($map, $map->getKeyValues($instance), $byEntity ? $instance : null);
                },
                $restoreFlag,
                'delete',
            );
        } catch (Throwable $failure) {
            $restoreFlag();

            throw $failure;
        }
    }

    /**
     * The by-key soft delete's stand-in instance: the validated key on an instance built without its constructor, so the
     * flag and stamp have somewhere to live and the one update path binds them. `CRUD-012` for a versioned entity is
     * decided before the key validates (spec/hooks.md Clarifications: the call can never succeed there).
     */
    private function scratchForKey(EntityMap $map, mixed $key): object
    {
        if ($map->versionProperty !== null) {
            throw new SimpleOrmException(
                'CRUD-012',
                $map->entityName(),
                'is versioned: a soft delete by key cannot bump a version it does not know; pass the entity (the version-checked form)',
            );
        }

        $scratch = (new ReflectionClass($map->entityType))->newInstanceWithoutConstructor();
        foreach (self::validateKey($map, $key) as $i => $value) {
            $map->keyProperties[$i]->setValue($scratch, $value);
        }

        return $scratch;
    }

    /**
     * The `#[CreatedAt]` hook (spec/hooks.md): the session clock when the value is unset — null, which is also
     * what an uninitialized typed property reads as (a `DateTimeImmutable` has no default instant, CODING-STANDARD §10);
     * a value the caller set is kept. Returns the function that puts the previous state back when the insert fails.
     *
     * @return Closure(): void
     */
    private function fillCreatedAt(EntityMap $map, object $entity): Closure
    {
        $created = $map->createdAtProperty();
        if ($created === null || $created->getValue($entity) !== null) {
            return static function (): void {
            };
        }

        $restore = $created->remember($entity);
        $created->setValue($entity, $this->options->now());

        return $restore;
    }

    /**
     * The column list of a write that changes a row: `$set` plus the `#[UpdatedAt]` column when the entity
     * declares one and the list does not already carry it (so it comes after the caller's columns).
     *
     * @param list<PropertyMap> $set
     * @return list<PropertyMap>
     */
    private function withUpdatedAt(EntityMap $map, array $set): array
    {
        $stamp = $map->updatedAtProperty();

        // Listing the stamp column in a column-list update changes nothing: it takes the clock and is written once
        // (spec/hooks.md Clarifications) — the library is the column's writer, so the caller's value never reaches the row.
        return $stamp === null || in_array($stamp, $set, true) ? $set : [...$set, $stamp];
    }

    /**
     * The map of a table-backed, keyed entity — `CRUD-003` for a read-only source, `CRUD-002` for no key.
     * The one writability check the immediate operations and the unit of work share (CODING-STANDARD §8).
     *
     * @internal
     */
    public function requireWritableKeyed(string $entityType): EntityMap
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

    // --- statement batching (Level 4, ADR-0044, spec/session.md "Batching") --------------------------------

    /**
     * Whether the option is `Auto` and this implementation's driver for the dialect sends a planned message as **one**
     * round trip with a result per statement (`Dialect::supportsStatementBatching()`). Where it is false under `Auto` —
     * SQLite and PostgreSQL in PHP — the plan is formed all the same and each message's statements run one at a time, in
     * order, reusing one prepared statement per distinct text within the call: nothing refuses, one configuration serves
     * every database, and this question says which realization is in force.
     */
    public function batchingInEffect(): bool
    {
        return $this->options->batching === BatchingMode::Auto && $this->options->dialect->supportsStatementBatching();
    }

    /**
     * Starts an explicit batch (ADR-0044): a queue bound to this session of writes and registry commands, sent together by
     * {@see DbBatch::send()}. Queuing does no I/O; the send is the visible round trip(s). Reads are not batched.
     */
    public function batch(): DbBatch
    {
        return new DbBatch($this);
    }

    /**
     * The test hook: `$observer` is told the statement count of every message a flush or a batch send forms, as the
     * message is sent — registry commands counted too. It is how the conformance runner checks the plan
     * (`expectMessages`, spec/session.md "The step scripts"); **not part of the public API** — the plan is the spec's,
     * the grouping is not something application code branches on. Pass null to remove it.
     *
     * @param (Closure(int): void)|null $observer
     * @internal
     */
    public function observeMessages(?Closure $observer): void
    {
        $this->messageObserver = $observer;
    }

    /**
     * Runs the operations of one call — a flush, or the send of an explicit batch — as messages by the plan: inside the
     * session's transaction when one is active, else one this call begins, commits after the last message, and rolls back
     * on failure. A failed call undoes every in-memory effect of every statement it prepared (keys, versions, stamps, flags,
     * tracking), so the instances are as they were before it; inside a caller's transaction the rows of the statements that
     * ran are the caller's to roll back (ADR-0044). An empty call begins no transaction.
     *
     * @param list<WriteOperation> $operations
     * @internal used by {@see DbBatch} and {@see DbUnitOfWork}
     */
    public function runCall(array $operations): BatchResult
    {
        if ($operations === []) {
            return new BatchResult([], 0);
        }

        $connection = $this->connection();
        $ownsTransaction = !$connection->inTransaction();
        if ($ownsTransaction) {
            $connection->beginTransaction();
        }

        $journal = new FlushJournal($this->tracking);
        try {
            $dialect = $this->options->dialect;
            $result = (new WriteRun(
                $connection,
                $this->options->batching,
                $this->options->maxStatementsPerMessage,
                $dialect->maxParametersPerMessage(),
                $dialect->reportsAffectedRowsBySelect(),
                $this->batchingInEffect(),
                $this->messageObserver,
                $this->log,
            ))->run($operations, $journal);

            if ($ownsTransaction) {
                $connection->commit();
            }
        } catch (Throwable $failure) {
            $journal->rollback();
            if ($ownsTransaction && $connection->inTransaction()) {
                $connection->rollBack();
            }

            throw $failure;
        }

        return $result;
    }

    /**
     * An insert as one operation of a batched call: nothing runs until the call does. `$beforeRecording` is a step the
     * caller needs done to the instance before its key properties are remembered (a flush's key flow); `$owners` are the
     * cascade owners the instance takes its key from — dependencies plan rule 1b reads beside its many-to-one navigations.
     *
     * @param (Closure(): void)|null $beforeRecording
     * @param list<object> $owners
     * @internal
     */
    public function insertOperation(object $entity, ?Closure $beforeRecording = null, array $owners = []): WriteOperation
    {
        return new WriteOperation(
            $entity,
            fn (): ?string => $this->entityIdentity($this->maps->load($entity::class), $entity),
            fn (): array => [...$this->navigationParents($entity), ...$owners],
            function (FlushJournal $journal) use ($entity, $beforeRecording): void {
                if ($beforeRecording !== null) {
                    $beforeRecording();
                }

                $journal->recordInsert($this->maps->load($entity::class), $entity);
            },
            fn (): ?PreparedWrite => $this->prepareInsert($entity),
        );
    }

    /** @internal */
    public function updateOperation(object $entity): WriteOperation
    {
        return new WriteOperation(
            $entity,
            fn (): ?string => $this->entityIdentity($this->maps->load($entity::class), $entity),
            fn (): array => $this->navigationParents($entity),
            fn (FlushJournal $journal) => $journal->recordUpdate($this->maps->load($entity::class), $entity),
            fn (): ?PreparedWrite => $this->prepareUpdate($entity),
        );
    }

    /**
     * @param list<string> $properties
     * @internal
     */
    public function updateOnlyOperation(object $entity, array $properties): WriteOperation
    {
        return new WriteOperation(
            $entity,
            fn (): ?string => $this->entityIdentity($this->maps->load($entity::class), $entity),
            fn (): array => $this->navigationParents($entity),
            fn (FlushJournal $journal) => $journal->recordUpdate($this->maps->load($entity::class), $entity),
            fn (): ?PreparedWrite => $this->prepareUpdateOnly($entity, $properties),
        );
    }

    /** The key or the entity form, dispatched as {@see delete()} is. @internal */
    public function deleteOperation(string $entityType, mixed $keyOrEntity): WriteOperation
    {
        $entity = is_object($keyOrEntity) && $keyOrEntity instanceof $entityType ? $keyOrEntity : null;

        return new WriteOperation(
            $entity,
            // A delete by key compares its key argument, as given; the entity form its instance's key.
            function () use ($entityType, $entity, $keyOrEntity): ?string {
                $map = $this->maps->load($entityType);

                return $entity !== null ? $this->entityIdentity($map, $entity) : self::keyArgumentIdentity($map, $keyOrEntity);
            },
            fn (): array => $entity === null ? [] : $this->navigationParents($entity),
            function (FlushJournal $journal) use ($entityType, $entity): void {
                if ($entity !== null) {
                    $journal->recordDelete($this->maps->load($entityType), $entity);
                }
            },
            fn (): ?PreparedWrite => $this->prepareDelete($entityType, $keyOrEntity),
        );
    }

    /** A registry command as one operation of a batched call: its args bind when the call prepares it. @internal */
    public function commandOperation(Command $command, object $args): WriteOperation
    {
        return new WriteOperation(
            null,
            static fn (): ?string => null,
            static fn (): array => [],
            static function (FlushJournal $journal): void {
                // A registry command moves nothing on an instance.
            },
            fn (): ?PreparedWrite => $this->prepareCommand($command, $args),
        );
    }

    /** The prepare half of a registry command: its args bound, the SQL kept in `@name` form for a multi-statement text. */
    private function prepareCommand(Command $command, object $args): PreparedWrite
    {
        $bound = ParameterBinder::bindKeepingPlaceholders(
            $command->source->sql,
            $args,
            $command->source->description,
            $this->converter,
            $this->options->dialect->supportsArrayParameters(),
        );

        return new PreparedWrite(
            sql: $bound->sql,
            parameters: $bound->parameters,
            result: ResultKind::CommandCount,
            complete: static fn (mixed $count): int => (int) $count,
            undo: static function (): void {
                // Nothing in memory to put back.
            },
            name: $this->entryName('command', $command->source),
        );
    }

    /**
     * The (entity type, key) an instance stands on, when its key is **set** — plan rule 1a's key clause (spec/session.md
     * "Batching"): two statements of one call on different instances with the same set key must not share a message, or
     * the identity gate would read a map the message has not yet updated. Null for a keyless map or an unset key.
     */
    private function entityIdentity(EntityMap $map, object $entity): ?string
    {
        return $map->keyProperties === [] ? null : self::writeIdentity($map, $map->getKeyValues($entity));
    }

    /** A delete by key's identity from its argument as given (a scalar, or a list in key order); null when it names no set key. */
    private static function keyArgumentIdentity(EntityMap $map, mixed $key): ?string
    {
        $parts = is_array($key) ? array_values($key) : [$key];

        return count($parts) === count($map->keyProperties) ? self::writeIdentity($map, $parts) : null;
    }

    /**
     * The identity of a key tuple: the entity type and the value-wise key token, unless the key is not set — a null part,
     * a database-generated key at its default, an empty client GUID (the "unset key" of spec/unit-of-work.md "Detach and
     * merge"). A natural key's default value is a key.
     *
     * @param list<mixed> $keyValues
     */
    private static function writeIdentity(EntityMap $map, array $keyValues): ?string
    {
        if ($keyValues === [] || (new KeyTuple($keyValues))->hasNullPart() || self::isUnsetGeneratedKey($map, $keyValues)) {
            return null;
        }

        return $map->entityType . '|' . (new KeyTuple($keyValues))->token();
    }

    /**
     * The targets of an instance's non-null many-to-one navigations — what plan rule 1b asks about: a statement on an
     * instance that points at one inserted in the current message (with a key the database generates) must wait for it.
     *
     * @return list<object>
     */
    private function navigationParents(object $entity): array
    {
        $map = $this->maps->load($entity::class);
        $parents = [];
        foreach ($map->relationships as $relationship) {
            if ($relationship->kind !== RelationshipKind::ManyToOne) {
                continue;
            }

            $property = $map->navigationProperty($relationship);
            $target = $property->isInitialized($entity) ? $property->getValue($entity) : null;
            if ($target !== null) {
                $parents[] = $target;
            }
        }

        return $parents;
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

        $bound = ParameterBinder::bindKeepingPlaceholders(
            (string) $map->definingSql,
            $args,
            self::statementName($resultType),
            $this->converter,
            $this->options->dialect->supportsArrayParameters(),
        );
        $statement = $this->connection()->prepare(SqlPlaceholders::toPdo($bound->sql));
        $this->log->write("{$map->entityName()}.statement", $bound->sql, $bound->parameters);
        PdoBinder::bindAndExecute($statement, $bound->parameters);

        return $statement;
    }

    private static function statementName(string $resultType): string
    {
        return self::shortName($resultType) . ' [Statement]';
    }

    // --- shared plumbing ---------------------------------------------------------

    /** @param 'query'|'command' $kind */
    private function createStatement(SqlSource $source, object $args, string $kind): PDOStatement
    {
        $bound = ParameterBinder::bindKeepingPlaceholders(
            $source->sql,
            $args,
            $source->description,
            $this->converter,
            $this->options->dialect->supportsArrayParameters(),
        );
        $statement = $this->connection()->prepare(SqlPlaceholders::toPdo($bound->sql));
        $this->log->write($this->entryName($kind, $source), $bound->sql, $bound->parameters);
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

    /** The plan every read path uses: the mapping plan, passed through the identity map when the session tracks. */
    private function planFor(PDOStatement $statement, string $resultType, string $queryName): callable
    {
        return $this->adoptingPlan($resultType, self::columnNames($statement), $queryName);
    }

    /** The mapping plan alone: always a fresh instance, never the identity map's ({@see refresh()} reads the raw row). */
    private function rawPlanFor(PDOStatement $statement, string $resultType, string $queryName): callable
    {
        return $this->mapper->createPlan($resultType, self::columnNames($statement), $queryName);
    }

    /**
     * The mapping plan for a column list, consulting the identity map for entity results when the
     * session tracks (ADR-0034 add.1): a known key returns the held instance and the row is not
     * applied. The join engine builds its per-segment plans through this too.
     *
     * @param list<string> $columnNames
     * @return callable(array<string, mixed>): mixed
     * @internal used by {@see DbEagerJoin}
     */
    public function adoptingPlan(string $resultType, array $columnNames, string $queryName): callable
    {
        $plan = $this->mapper->createPlan($resultType, $columnNames, $queryName);

        return $this->tracking?->adopting($plan, $resultType) ?? $plan;
    }

    /** @return list<string> */
    private static function columnNames(PDOStatement $statement): array
    {
        $columns = [];
        for ($i = 0; $i < $statement->columnCount(); $i++) {
            $meta = $statement->getColumnMeta($i);
            $columns[] = $meta !== false ? $meta['name'] : (string) $i;
        }

        return $columns;
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

    /**
     * The name a criteria query against `$map` reports in errors (`"<Entity>
     * criteria"`, §7.11) — the one implementation {@see DbEagerJoin} and
     * {@see \SimpleOrm\Query\CriteriaQuery} share (CODING-STANDARD §8) instead
     * of re-deriving the entity's short name themselves.
     */
    public static function criteriaQueryName(EntityMap $map): string
    {
        return $map->entityName() . ' criteria';
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
        $this->tracking?->clear();
        $this->unitOfWork?->clear();
    }

    public function __destruct()
    {
        $this->close();
    }
}
