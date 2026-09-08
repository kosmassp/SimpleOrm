<?php

declare(strict_types=1);

namespace SimpleOrm\Validation;

use DateTimeImmutable;
use PDO;
use PDOException;
use PDOStatement;
use ReflectionClass;
use ReflectionMethod;
use ReflectionNamedType;
use ReflectionProperty;
use SimpleOrm\Errors\MappingException;
use SimpleOrm\Errors\SchemaValidationException;
use SimpleOrm\Errors\SimpleOrmException;
use SimpleOrm\Errors\ValidationError;
use SimpleOrm\Metadata\ColumnType;
use SimpleOrm\Metadata\EntityMap;
use SimpleOrm\Metadata\EntityMapLoader;
use SimpleOrm\Metadata\RelationKind;
use SimpleOrm\Migrations\MigrationRunner;
use SimpleOrm\Migrations\MigrationSet;
use SimpleOrm\Migrations\MigrationState;
use SimpleOrm\Migrations\SnapshotSet;
use SimpleOrm\Parameters\SqlPlaceholders;
use SimpleOrm\Session\Command;
use SimpleOrm\Session\Db;
use SimpleOrm\Session\Query;
use SimpleOrm\Types\Decimal;

/**
 * The rules (§7.18-21), mirroring `dotnet/src/SimpleOrm/SchemaGuard.cs`:
 * validates every registered query/command and every mapped entity against
 * the real database the session is connected to, without executing anything
 * that writes. Every violation is collected; `validate()` throws one
 * {@see SchemaValidationException} carrying the complete report. There is no
 * warn-only mode.
 *
 * **PHP adaptation (CODING-STANDARD §10):** PDO exposes column metadata only
 * after `execute()`, never from a bare `prepare()`. A **query** (a result
 * type is expected) is therefore prepared, bound to `NULL` for every
 * placeholder, and executed inside a transaction this class always rolls
 * back, then described via `columnCount()`/`getColumnMeta()` — a
 * `PDOException` there is `VAL-001`. A **command** (no result type) is only
 * prepared, never executed, exactly like the C# reference (SQLite's real,
 * non-emulated `prepare()` already resolves referenced tables/columns, so
 * syntax and schema errors surface there without ever running the statement).
 * `getColumnMeta()` on this build reports `table`/`name` for a column
 * resolved from a base table, but for an **aliased** column `name` is the
 * alias, not the source column — there is no way to recover the original
 * column name. Such a column is therefore treated as an expression column
 * (unknowable nullability, the stricter direction) unless its alias happens
 * to name a real column of the reported table.
 */
final class SchemaGuard
{
    private function __construct()
    {
    }

    /**
     * Every violation across every registry entry, entity, and (when given) the
     * migration history — never first-error-only.
     *
     * @param list<class-string> $classes registries and mapped/statement entities to inspect
     * @return list<ValidationError>
     */
    public static function report(
        Db $db,
        array $classes,
        ?MigrationSet $migrations = null,
        ?SnapshotSet $snapshots = null,
    ): array {
        $errors = [];

        if ($migrations !== null) {
            self::checkMigrations($db, $migrations, $snapshots, $errors);
        }

        $connection = $db->connection();
        $connection->beginTransaction();
        $columnsCache = [];
        try {
            foreach (self::discoverRegistry($classes) as [$source, $sql, $argsType, $resultType]) {
                self::validateStatement($db, $connection, $columnsCache, $source, $sql, $argsType, $resultType, $errors);
            }

            foreach ($classes as $class) {
                $reflection = new ReflectionClass($class);
                if ($reflection->isAbstract() || $reflection->isInterface()) {
                    continue;
                }

                if (EntityMapLoader::hasMappingAttributes($class) && !EntityMapLoader::isOwnedType($class)) {
                    self::validateEntity($db, $connection, $columnsCache, $class, $errors);
                }
            }
        } finally {
            $connection->rollBack();
        }

        return $errors;
    }

    /**
     * Throws {@see SchemaValidationException} when {@see self::report()} is
     * non-empty.
     *
     * @param list<class-string> $classes
     */
    public static function validate(
        Db $db,
        array $classes,
        ?MigrationSet $migrations = null,
        ?SnapshotSet $snapshots = null,
    ): void {
        $errors = self::report($db, $classes, $migrations, $snapshots);
        if ($errors !== []) {
            throw new SchemaValidationException($errors);
        }
    }

    // --- registry ------------------------------------------------------------------

    /**
     * Every public static no-arg method returning `Query`/`Command`, reflected
     * and called (§6, CODING-STANDARD §10: the PHP shape of a registry field).
     *
     * @param list<class-string> $classes
     * @return iterable<array{0: string, 1: string, 2: ?string, 3: ?string}> source, sql, argsType, resultType
     */
    private static function discoverRegistry(array $classes): iterable
    {
        foreach ($classes as $class) {
            $reflection = new ReflectionClass($class);
            foreach ($reflection->getMethods(ReflectionMethod::IS_PUBLIC | ReflectionMethod::IS_STATIC) as $method) {
                if ($method->getNumberOfParameters() > 0 || $method->isAbstract()) {
                    continue;
                }

                $returnType = $method->getReturnType();
                if (!$returnType instanceof ReflectionNamedType || $returnType->isBuiltin()) {
                    continue;
                }

                $returnTypeName = $returnType->getName();
                if ($returnTypeName !== Query::class && $returnTypeName !== Command::class) {
                    continue;
                }

                $value = $method->invoke(null);
                $source = $reflection->getShortName() . '.' . $method->getName();
                if ($value instanceof Query) {
                    yield [$source, $value->source->sql, $value->argsType, $value->resultType];
                } elseif ($value instanceof Command) {
                    yield [$source, $value->source->sql, $value->argsType, null];
                }
            }
        }
    }

    // --- statements (registry queries/commands and statement entities) -------------

    /** @param array<string, array<string, array{0: string, 1: bool}>> $columnsCache */
    private static function validateStatement(
        Db $db,
        PDO $connection,
        array &$columnsCache,
        string $source,
        string $sql,
        ?string $argsType,
        ?string $resultType,
        array &$errors,
    ): void {
        $placeholders = SqlPlaceholders::find($sql);

        // PRM-001/PRM-002 statically, both directions (§7.13).
        if ($argsType !== null) {
            $properties = self::publicPropertyNames($argsType);
            $argsShort = self::shortName($argsType);
            foreach ($placeholders as $placeholder) {
                if (!self::containsCaseInsensitive($properties, $placeholder)) {
                    $errors[] = new ValidationError(
                        'PRM-001',
                        $source,
                        "SQL parameter @{$placeholder} has no property on {$argsShort}",
                    );
                }
            }

            foreach ($properties as $property) {
                if (!self::containsCaseInsensitive($placeholders, $property)) {
                    $errors[] = new ValidationError(
                        'PRM-002',
                        $source,
                        "property {$argsShort}.{$property} is never used by the SQL",
                    );
                }
            }
        }

        // Lints on the raw text.
        $stripped = self::stripLiteralsAndComments($sql);
        if (self::isSelectStar($stripped)) {
            $errors[] = new ValidationError('VAL-021', $source, 'SELECT * is not allowed; list columns explicitly');
        }

        if (preg_match('/(?i)current_timestamp|datetime\s*\(\s*\'now\'/', $stripped) === 1) {
            $errors[] = new ValidationError(
                'VAL-020',
                $source,
                "current_timestamp/datetime('now') store datetimes without a UTC marker; bind an ISO-8601 Z value instead",
            );
        }

        // Prepare / describe without executing anything that persists: a
        // command is only prepared (never run); a query additionally executes
        // with every placeholder bound NULL, inside the caller's shield
        // transaction, to obtain its column metadata (see the class docblock).
        try {
            $statement = $connection->prepare(SqlPlaceholders::toPdo($sql));
        } catch (PDOException $exception) {
            $errors[] = new ValidationError('VAL-001', $source, $exception->getMessage());

            return;
        }

        foreach ($placeholders as $placeholder) {
            $statement->bindValue(':' . $placeholder, null, PDO::PARAM_NULL);
        }

        if ($resultType === null) {
            return;
        }

        try {
            $statement->execute();
        } catch (PDOException $exception) {
            $errors[] = new ValidationError('VAL-001', $source, $exception->getMessage());

            return;
        }

        // Result shape (MAP-001/002/003) through the one mapping pipeline.
        $columns = self::describedColumns($statement);
        try {
            $db->mapper()->createPlan($resultType, $columns, $source);
        } catch (SimpleOrmException $exception) {
            $errors[] = new ValidationError($exception->errorCode, $source, self::rawMessage($exception));
        }

        self::validateResultColumns($db, $connection, $columnsCache, $source, $sql, $statement, $resultType, $errors);
        $statement->closeCursor();
    }

    /** @return list<string> */
    private static function describedColumns(PDOStatement $statement): array
    {
        $columns = [];
        for ($i = 0; $i < $statement->columnCount(); $i++) {
            $meta = $statement->getColumnMeta($i);
            $columns[] = $meta !== false && is_string($meta['name'] ?? null) ? $meta['name'] : (string) $i;
        }

        return $columns;
    }

    /**
     * @param array<string, array<string, array{0: string, 1: bool}>> $columnsCache
     */
    private static function validateResultColumns(
        Db $db,
        PDO $connection,
        array &$columnsCache,
        string $source,
        string $sql,
        PDOStatement $statement,
        string $resultType,
        array &$errors,
    ): void {
        $notNullOverrides = [];
        if (preg_match_all('/(?i)--\s*notnull:\s*([^\r\n]+)/', $sql, $matches) > 0) {
            foreach ($matches[1] as $list) {
                foreach (explode(',', $list) as $name) {
                    $notNullOverrides[strtolower(trim($name))] = true;
                }
            }
        }

        $map = !self::isScalarType($resultType) && EntityMapLoader::hasMappingAttributes($resultType)
            ? $db->maps()->load($resultType)
            : null;

        for ($i = 0; $i < $statement->columnCount(); $i++) {
            $meta = $statement->getColumnMeta($i);
            $columnName = $meta !== false && is_string($meta['name'] ?? null) ? $meta['name'] : (string) $i;

            $member = self::resolveMember($resultType, $map, $columnName);
            if ($member === null) {
                continue;   // MAP-001 already reported by the pipeline
            }

            [$columnType, $phpType, $nullable] = $member;

            $table = $meta !== false && is_string($meta['table'] ?? null) && $meta['table'] !== '' ? $meta['table'] : null;
            if ($table !== null) {
                $info = self::getColumnInfo($db, $connection, $columnsCache, $table, $columnName);
                if ($info !== null) {
                    [$declaredType, $notNull] = $info;
                    $hasHandler = $phpType !== null && $db->converter()->hasHandler($phpType);
                    if (!$db->options()->dialect->isDeclaredTypeCompatible($declaredType, $columnType) && !$hasHandler) {
                        $errors[] = new ValidationError(
                            'VAL-011',
                            $source,
                            "column '{$columnName}' is declared {$declaredType} in {$table}, incompatible with "
                                . self::shortPhpType($phpType) . ' (no handler)',
                        );
                    }

                    if (!$notNull && !$nullable) {
                        $errors[] = new ValidationError(
                            'VAL-010',
                            $source,
                            "nullable column '{$columnName}' maps to non-nullable " . self::shortPhpType($phpType),
                        );
                    }

                    continue;
                }
            }

            // Expression column (or an alias PDO cannot resolve to a real base
            // column): nullability unknowable — require nullable or the comment.
            if (!$nullable && !isset($notNullOverrides[strtolower($columnName)])) {
                $errors[] = new ValidationError(
                    'VAL-010',
                    $source,
                    "expression column '{$columnName}' has unknown nullability; make the member nullable or add "
                        . "'-- notnull: {$columnName}'",
                );
            }
        }
    }

    /** @return array{0: ColumnType, 1: ?string, 2: bool}|null [type, phpType, nullable] */
    private static function resolveMember(string $resultType, ?EntityMap $map, string $column): ?array
    {
        if ($map !== null) {
            foreach ($map->properties as $property) {
                if (strcasecmp($property->columnName, $column) === 0) {
                    return [$property->type, $property->phpType, $property->nullable];
                }
            }

            return null;
        }

        if (self::isScalarType($resultType)) {
            $nullable = str_starts_with($resultType, '?');
            $bare = $nullable ? substr($resultType, 1) : $resultType;

            return [ColumnType::fromPhpType($bare), $bare, $nullable];
        }

        $class = new ReflectionClass($resultType);
        $constructor = $class->getConstructor();
        if ($constructor !== null) {
            foreach ($constructor->getParameters() as $parameter) {
                if (self::namesMatch($column, $parameter->getName())) {
                    $type = $parameter->getType();
                    $phpType = $type instanceof ReflectionNamedType ? $type->getName() : null;

                    return [ColumnType::fromPhpType($phpType ?? 'string'), $phpType, $parameter->allowsNull()];
                }
            }
        }

        foreach ($class->getProperties(ReflectionProperty::IS_PUBLIC) as $property) {
            if (!$property->isStatic() && self::namesMatch($column, $property->getName())) {
                $type = $property->getType();
                $phpType = $type instanceof ReflectionNamedType ? $type->getName() : null;
                $nullable = $type === null || $type->allowsNull();

                return [ColumnType::fromPhpType($phpType ?? 'string'), $phpType, $nullable];
            }
        }

        return null;
    }

    private static function isScalarType(string $type): bool
    {
        $bare = ltrim($type, '?');

        return in_array($bare, ['int', 'float', 'bool', 'string'], true)
            || $bare === Decimal::class
            || $bare === DateTimeImmutable::class
            || enum_exists($bare);
    }

    // --- entities --------------------------------------------------------------------

    /** @param array<string, array<string, array{0: string, 1: bool}>> $columnsCache */
    private static function validateEntity(
        Db $db,
        PDO $connection,
        array &$columnsCache,
        string $entityType,
        array &$errors,
    ): void {
        try {
            $map = $db->maps()->load($entityType);
        } catch (MappingException $exception) {
            foreach ($exception->errors as $error) {
                $errors[] = new ValidationError($error->code, self::shortName($entityType), $error->message);
            }

            return;
        }

        if (
            ($map->kind === RelationKind::Procedure && !$db->options()->dialect->supportsProcedures())
            || ($map->kind === RelationKind::MaterializedView && !$db->options()->dialect->supportsMaterializedViews())
        ) {
            return;   // dormant on this dialect (capability-gated)
        }

        $entityName = self::shortName($entityType);
        if ($map->kind === RelationKind::Statement) {
            self::validateStatement(
                $db,
                $connection,
                $columnsCache,
                $entityName . ' [Statement]',
                (string) $map->definingSql,
                argsType: null,
                resultType: $entityType,
                errors: $errors,
            );

            return;
        }

        $columns = self::getRelationColumns($db, $connection, (string) $map->relationName);
        if ($columns === []) {
            $errors[] = new ValidationError('VAL-012', $entityName, "relation '{$map->relationName}' does not exist in the database");

            return;
        }

        foreach ($map->properties as $property) {
            $info = $columns[strtolower($property->columnName)] ?? null;
            if ($info === null) {
                $errors[] = new ValidationError(
                    'VAL-013',
                    $entityName,
                    "mapped column '{$property->columnName}' does not exist in '{$map->relationName}'",
                );

                continue;
            }

            if ($map->kind !== RelationKind::Table) {
                continue;   // views report neither declared types nor nullability reliably (§7.19)
            }

            [$declaredType, $notNull] = $info;
            $hasHandler = $property->phpType !== null && $db->converter()->hasHandler($property->phpType);
            if (!$db->options()->dialect->isDeclaredTypeCompatible($declaredType, $property->type) && !$hasHandler) {
                $errors[] = new ValidationError(
                    'VAL-011',
                    $entityName,
                    "column '{$property->columnName}' is declared {$declaredType}, incompatible with "
                        . self::shortPhpType($property->phpType) . ' (no handler)',
                );
            }

            if (!$notNull && !$property->nullable) {
                $errors[] = new ValidationError(
                    'VAL-010',
                    $entityName,
                    "nullable column '{$property->columnName}' maps to non-nullable {$property->propertyName()}",
                );
            }
        }
    }

    // --- migrations (MIG-030 and history health) --------------------------------------

    private static function checkMigrations(
        Db $db,
        MigrationSet $migrations,
        ?SnapshotSet $snapshots,
        array &$errors,
    ): void {
        $runner = new MigrationRunner($db->connection(), $db->options()->dialect, $db->maps(), $migrations, $snapshots);
        foreach ($runner->status() as $entry) {
            [$code, $message] = match ($entry->state) {
                MigrationState::Pending => ['MIG-030', 'pending — apply migrations before starting the application'],
                MigrationState::Drifted => ['MIG-010', 'applied with a different checksum than the code renders'],
                MigrationState::Unknown => ['MIG-011', 'applied in the database but unknown to the code'],
                MigrationState::Applied => [null, ''],
            };
            if ($code !== null) {
                $errors[] = new ValidationError($code, sprintf('V%04d %s', $entry->version, $entry->objectName), $message);
            }
        }
    }

    // --- introspection helpers ---------------------------------------------------------

    /**
     * @param array<string, array<string, array{0: string, 1: bool}>> $cache
     * @return array{0: string, 1: bool}|null [declaredType, notNull]
     */
    private static function getColumnInfo(Db $db, PDO $connection, array &$cache, string $table, string $column): ?array
    {
        if (!isset($cache[$table])) {
            $cache[$table] = self::getRelationColumns($db, $connection, $table);
        }

        return $cache[$table][strtolower($column)] ?? null;
    }

    /** @return array<string, array{0: string, 1: bool}> lowercased column name => [declaredType, notNull] */
    private static function getRelationColumns(Db $db, PDO $connection, string $relation): array
    {
        $columns = [];
        $statement = $connection->prepare(SqlPlaceholders::toPdo($db->options()->dialect->columnsInfoSql()));
        $statement->bindValue(':relation', $relation, PDO::PARAM_STR);
        $statement->execute();
        while (($row = $statement->fetch(PDO::FETCH_ASSOC)) !== false) {
            // Primary-key columns are implicitly NOT NULL (SQLite reports notnull=0 for INTEGER PRIMARY KEY).
            $notNull = ((int) $row['notnull']) !== 0 || ((int) $row['pk']) !== 0;
            $columns[strtolower((string) $row['name'])] = [(string) $row['type'], $notNull];
        }

        return $columns;
    }

    // --- small helpers -------------------------------------------------------------------

    private static function rawMessage(SimpleOrmException $exception): string
    {
        $prefix = "{$exception->errorCode} {$exception->target}: ";

        return str_starts_with($exception->getMessage(), $prefix)
            ? substr($exception->getMessage(), strlen($prefix))
            : $exception->getMessage();
    }

    /** @return list<string> */
    private static function publicPropertyNames(string $class): array
    {
        $names = [];
        foreach ((new ReflectionClass($class))->getProperties(ReflectionProperty::IS_PUBLIC) as $property) {
            if (!$property->isStatic()) {
                $names[] = $property->getName();
            }
        }

        return $names;
    }

    /** @param list<string> $haystack */
    private static function containsCaseInsensitive(array $haystack, string $needle): bool
    {
        foreach ($haystack as $candidate) {
            if (strcasecmp($candidate, $needle) === 0) {
                return true;
            }
        }

        return false;
    }

    /** Case- and underscore-insensitive: `created_at` matches `createdAt` (§7.8). */
    private static function namesMatch(string $left, string $right): bool
    {
        return strcasecmp(str_replace('_', '', $left), str_replace('_', '', $right)) === 0;
    }

    private static function isSelectStar(string $sql): bool
    {
        return preg_match('/(?i)(?<=select|,)\s*([A-Za-z_]\w*\s*\.\s*)?\*/', $sql) === 1;
    }

    private static function stripLiteralsAndComments(string $sql): string
    {
        $withoutLiterals = preg_replace("/'([^']|'')*'/", "''", $sql) ?? $sql;

        return preg_replace('/--[^\r\n]*/', '', $withoutLiterals) ?? $withoutLiterals;
    }

    private static function shortName(string $fqcn): string
    {
        $slash = strrpos($fqcn, '\\');

        return $slash === false ? $fqcn : substr($fqcn, $slash + 1);
    }

    private static function shortPhpType(?string $phpType): string
    {
        return $phpType === null ? 'mixed' : self::shortName($phpType);
    }
}
