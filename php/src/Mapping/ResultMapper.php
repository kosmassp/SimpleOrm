<?php

declare(strict_types=1);

namespace SimpleOrm\Mapping;

use DateTimeImmutable;
use ReflectionClass;
use ReflectionMethod;
use ReflectionNamedType;
use ReflectionParameter;
use ReflectionProperty;
use SimpleOrm\Errors\SimpleOrmException;
use SimpleOrm\Metadata\ColumnType;
use SimpleOrm\Metadata\EntityMapLoader;
use SimpleOrm\Metadata\PropertyMap;
use SimpleOrm\Types\Decimal;

/**
 * The one row-mapping pipeline (§7.11), mirroring
 * dotnet/src/SimpleOrm/ResultMapper.cs: plans are built per (result type,
 * column list) and cached, shared by raw SQL, statement entities, generated
 * CRUD, and criteria queries.
 *
 * Strictness (§7.7/§7.8): an entity result's columns must match its
 * `EntityMap` exactly both ways — an unknown column is `MAP-001`, a mapped
 * property with no column is `MAP-002`. A plain DTO result type (no mapping
 * attributes, CODING-STANDARD §10) constructs via the §7.8 algorithm: a
 * matched constructor wins, leftover columns bind to settable properties, a
 * required member (a constructor parameter with no default, or a non-nullable
 * property with no default) with no column is `MAP-002`; NULL into a
 * non-nullable member is `MAP-031`. PHP allows at most one constructor per
 * class, so the C# reference's "competing constructors" ambiguity (`MAP-003`)
 * is realized here as one constructor parameter matching more than one result
 * column.
 */
final class ResultMapper
{
    /** @var array<string, callable> */
    private array $plans = [];

    private readonly UnloadedNavigations $unloaded;

    public function __construct(
        private readonly EntityMapLoader $maps,
        private readonly TypeConverter $converter,
    ) {
        $this->unloaded = new UnloadedNavigations($maps);
    }

    /**
     * @param list<string> $columnNames
     * @return callable(array<string, mixed>): mixed row → mapped value, cached per (type, column list)
     */
    public function createPlan(string $resultType, array $columnNames, string $queryName): callable
    {
        $cacheKey = $resultType . '|' . implode("\0", $columnNames);

        return $this->plans[$cacheKey] ??= $this->buildPlan($resultType, $columnNames, $queryName);
    }

    /** @param list<string> $columnNames */
    private function buildPlan(string $resultType, array $columnNames, string $queryName): callable
    {
        if ($this->isScalarType($resultType)) {
            return $this->compileScalar($resultType, $queryName);
        }

        return EntityMapLoader::hasMappingAttributes($resultType)
            ? $this->compileEntity($resultType, $columnNames, $queryName)
            : $this->compileDto($resultType, $columnNames, $queryName);
    }

    // --- scalar results -----------------------------------------------------

    private function isScalarType(string $type): bool
    {
        $bare = ltrim($type, '?');

        return in_array($bare, ['int', 'float', 'bool', 'string'], true)
            || $bare === Decimal::class
            || $bare === DateTimeImmutable::class
            || enum_exists($bare)
            || $this->converter->hasHandler($bare);
    }

    private function compileScalar(string $resultType, string $queryName): callable
    {
        $nullable = str_starts_with($resultType, '?');
        $bareType = $nullable ? substr($resultType, 1) : $resultType;
        $columnType = ColumnType::fromPhpType($bareType);
        $context = "{$queryName} → {$resultType}.scalar";

        return function (array $row) use ($bareType, $columnType, $nullable, $context): mixed {
            $raw = $row === [] ? null : reset($row);

            return $this->convert($raw, $columnType, $bareType, $nullable, $context);
        };
    }

    // --- entity results -------------------------------------------------------

    /** @param list<string> $columnNames */
    private function compileEntity(string $resultType, array $columnNames, string $queryName): callable
    {
        $map = $this->maps->load($resultType);
        $entityName = $map->entityName();

        /** @var list<PropertyMap> $byColumn */
        $byColumn = [];
        foreach ($columnNames as $columnName) {
            $property = null;
            foreach ($map->properties as $candidate) {
                if (strcasecmp($candidate->columnName, $columnName) === 0) {
                    $property = $candidate;
                    break;
                }
            }

            if ($property === null) {
                throw new SimpleOrmException(
                    'MAP-001',
                    $queryName,
                    "result column '{$columnName}' has no mapped property on {$entityName}",
                );
            }

            $byColumn[] = $property;
        }

        $missing = array_values(array_filter(
            $map->properties,
            static fn (PropertyMap $p): bool => !in_array($p, $byColumn, true),
        ));
        if ($missing !== []) {
            throw new SimpleOrmException(
                'MAP-002',
                $queryName,
                "{$entityName} expects column(s) " . implode(', ', array_map(
                    static fn (PropertyMap $p): string => $p->columnName,
                    $missing,
                )) . ' which the result does not contain',
            );
        }

        // The sample entities are plain classes with an implicit parameterless
        // constructor (§7.1); every mapped property assigns through
        // PropertyMap::setValue, which writes via reflection so
        // `private(set)`/readonly targets work the same as for navigations.
        // Owned members (ADR-0030) regroup by navigation: construct the owned
        // instance, assign its members, attach it. A nullable navigation whose
        // member columns are all NULL stays null — the row said "no value".
        // A database-read entity's collection navigations are unset (ADR-0032):
        // unloaded is not empty.
        $markUnloaded = $this->unloaded->markerFor($resultType);

        return function (array $row) use ($resultType, $columnNames, $byColumn, $queryName, $markUnloaded): object {
            $instance = new $resultType();
            if ($markUnloaded !== null) {
                $markUnloaded($instance);
            }
            /** @var array<int, array{owner: \SimpleOrm\Metadata\OwnedMap, bindings: list<array{0: string, 1: PropertyMap}>}> $ownedGroups */
            $ownedGroups = [];
            foreach ($columnNames as $i => $columnName) {
                $property = $byColumn[$i];
                if ($property->owner !== null) {
                    $ownedGroups[spl_object_id($property->owner)] ??= ['owner' => $property->owner, 'bindings' => []];
                    $ownedGroups[spl_object_id($property->owner)]['bindings'][] = [$columnName, $property];
                    continue;
                }

                $context = "{$queryName} → {$property->target()}";
                $value = $this->convert($row[$columnName] ?? null, $property->type, $property->phpType, $property->nullable, $context);
                $property->setValue($instance, $value);
            }

            foreach ($ownedGroups as $group) {
                $owner = $group['owner'];
                if ($owner->nullable) {
                    $allNull = true;
                    foreach ($group['bindings'] as [$columnName]) {
                        if (($row[$columnName] ?? null) !== null) {
                            $allNull = false;
                            break;
                        }
                    }

                    if ($allNull) {
                        continue;
                    }
                }

                $ownedType = $owner->ownedType;
                $owned = new $ownedType();
                foreach ($group['bindings'] as [$columnName, $property]) {
                    $context = "{$queryName} → {$property->target()}";
                    $value = $this->convert($row[$columnName] ?? null, $property->type, $property->phpType, $property->nullable, $context);
                    $property->property->setValue($owned, $value);
                }

                $owner->property->setValue($instance, $owned);
            }

            return $instance;
        };
    }

    // --- DTO results (records/plain classes with no mapping attributes) -------

    /** @param list<string> $columnNames */
    private function compileDto(string $resultType, array $columnNames, string $queryName): callable
    {
        $class = new ReflectionClass($resultType);
        $shortName = $class->getShortName();

        /** @var array<string, ReflectionProperty> $settable */
        $settable = [];
        foreach ($class->getProperties(ReflectionProperty::IS_PUBLIC) as $property) {
            if (!$property->isStatic()) {
                $settable[$property->getName()] = $property;
            }
        }

        $constructor = $class->getConstructor();
        $ctorPlan = null;
        $consumedOrdinals = [];

        if ($constructor !== null && $constructor->getNumberOfParameters() > 0) {
            [$ctorPlan, $consumedOrdinals] = $this->resolveConstructorPlan(
                $constructor,
                $columnNames,
                $shortName,
                $queryName,
            );
        }

        // Leftover columns bind to settable properties not already consumed
        // by the constructor; an unmatched column is MAP-001 (§7.8 step 4).
        /** @var array<int, ReflectionProperty> $memberBindings */
        $memberBindings = [];
        foreach ($columnNames as $i => $columnName) {
            if (isset($consumedOrdinals[$i])) {
                continue;
            }

            $property = self::findByName($settable, $columnName);
            if ($property === null) {
                throw new SimpleOrmException(
                    'MAP-001',
                    $queryName,
                    "result column '{$columnName}' matches no constructor parameter or settable property on {$shortName}",
                );
            }

            $memberBindings[$i] = $property;
        }

        // A plain (non-promoted) settable property that is non-nullable, has
        // no default, and never got bound is MAP-002 (§7.8 step 5); promoted
        // properties were already checked as constructor parameters above.
        $boundNames = array_map(static fn (ReflectionProperty $p): string => $p->getName(), $memberBindings);
        if ($ctorPlan !== null) {
            foreach ($ctorPlan as $entry) {
                $boundNames[] = $entry['parameter']->getName();
            }
        }

        $missingRequired = [];
        foreach ($settable as $name => $property) {
            if ($property->isPromoted() || in_array($name, $boundNames, true)) {
                continue;
            }

            if (!self::propertyNullable($property) && !$property->hasDefaultValue()) {
                $missingRequired[] = $name;
            }
        }

        if ($missingRequired !== []) {
            throw new SimpleOrmException(
                'MAP-002',
                $queryName,
                'required member(s) ' . implode(', ', $missingRequired) . " of {$shortName} have no matching result column",
            );
        }

        return function (array $row) use ($resultType, $columnNames, $ctorPlan, $memberBindings, $queryName): object {
            $arguments = [];
            if ($ctorPlan !== null) {
                foreach ($ctorPlan as $entry) {
                    $parameter = $entry['parameter'];
                    if ($entry['ordinal'] === null) {
                        $arguments[] = $parameter->getDefaultValue();
                        continue;
                    }

                    $columnName = $columnNames[$entry['ordinal']];
                    $phpType = self::parameterType($parameter);
                    $context = "{$queryName} → {$resultType}.{$parameter->getName()}";
                    $arguments[] = $this->convert(
                        $row[$columnName] ?? null,
                        ColumnType::fromPhpType($phpType ?? 'string'),
                        $phpType,
                        $parameter->allowsNull(),
                        $context,
                    );
                }
            }

            $instance = $ctorPlan !== null
                ? (new ReflectionClass($resultType))->newInstanceArgs($arguments)
                : new $resultType();

            foreach ($memberBindings as $i => $property) {
                $columnName = $columnNames[$i];
                $phpType = self::propertyType($property);
                $context = "{$queryName} → {$resultType}.{$property->getName()}";
                $value = $this->convert(
                    $row[$columnName] ?? null,
                    ColumnType::fromPhpType($phpType ?? 'string'),
                    $phpType,
                    self::propertyNullable($property),
                    $context,
                );
                $property->setValue($instance, $value);
            }

            return $instance;
        };
    }

    /**
     * Matches the constructor's parameters against the result columns
     * (§7.8): a parameter matching more than one column is ambiguous
     * (`MAP-003` — PHP's realization of "competing constructors", since a
     * class has at most one constructor); an unmatched parameter with no
     * default is collected for the caller's `MAP-002`.
     *
     * @param list<string> $columnNames
     * @return array{0: list<array{parameter: ReflectionParameter, ordinal: ?int}>, 1: array<int, true>}
     */
    private function resolveConstructorPlan(
        ReflectionMethod $constructor,
        array $columnNames,
        string $shortName,
        string $queryName,
    ): array {
        $plan = [];
        $consumedOrdinals = [];
        $missingRequired = [];

        foreach ($constructor->getParameters() as $parameter) {
            $ordinal = null;
            $matches = 0;
            foreach ($columnNames as $i => $columnName) {
                if (self::namesMatch($columnName, $parameter->getName())) {
                    $ordinal = $i;
                    $matches++;
                }
            }

            if ($matches > 1) {
                throw new SimpleOrmException(
                    'MAP-003',
                    $queryName,
                    "{$shortName} constructor parameter '{$parameter->getName()}' matches more than one result column; construction is ambiguous",
                );
            }

            if ($ordinal === null) {
                if (!$parameter->isDefaultValueAvailable()) {
                    $missingRequired[] = $parameter->getName();
                }

                $plan[] = ['parameter' => $parameter, 'ordinal' => null];
                continue;
            }

            $plan[] = ['parameter' => $parameter, 'ordinal' => $ordinal];
            $consumedOrdinals[$ordinal] = true;
        }

        if ($missingRequired !== []) {
            throw new SimpleOrmException(
                'MAP-002',
                $queryName,
                'required member(s) ' . implode(', ', $missingRequired) . " of {$shortName} have no matching result column",
            );
        }

        return [$plan, $consumedOrdinals];
    }

    // --- conversion -------------------------------------------------------------

    private function convert(mixed $raw, ColumnType $type, ?string $phpType, bool $nullable, string $context): mixed
    {
        if ($raw === null) {
            if ($nullable) {
                return null;
            }

            throw new SimpleOrmException('MAP-031', $context, "NULL cannot convert to non-nullable {$phpType}");
        }

        return $this->converter->fromDatabase($raw, $type, $phpType, $context);
    }

    // --- name matching and reflection helpers -----------------------------------

    /** Case- and underscore-insensitive: `created_at` matches `CreatedAt` (§7.8). */
    private static function namesMatch(string $left, string $right): bool
    {
        return strcasecmp(str_replace('_', '', $left), str_replace('_', '', $right)) === 0;
    }

    /** @param array<string, ReflectionProperty> $settable */
    private static function findByName(array $settable, string $columnName): ?ReflectionProperty
    {
        foreach ($settable as $name => $property) {
            if (self::namesMatch($columnName, $name)) {
                return $property;
            }
        }

        return null;
    }

    private static function propertyNullable(ReflectionProperty $property): bool
    {
        $type = $property->getType();

        return $type === null || $type->allowsNull();
    }

    /**
     * The member's PHP type for conversion: a builtin name or a class-string,
     * `list<Item>` for an `array`-typed member documented `@var list<Item>` /
     * `@param list<Item> $name` (the §7.10 JSON list handler's lookup key,
     * CODING-STANDARD §10 — PHP arrays carry no element type of their own).
     */
    private static function propertyType(ReflectionProperty $property): ?string
    {
        $type = $property->getType();
        if ($type instanceof ReflectionNamedType && $type->getName() === 'array') {
            $item = self::listItemType($property->getDocComment() ?: '');
            if ($item !== null) {
                return "list<{$item}>";
            }
        }

        return $type instanceof ReflectionNamedType ? $type->getName() : null;
    }

    private static function parameterType(ReflectionParameter $parameter): ?string
    {
        $type = $parameter->getType();
        if ($type instanceof ReflectionNamedType && $type->getName() === 'array') {
            $declaring = $parameter->getDeclaringFunction();
            $doc = $declaring->getDocComment();
            $item = self::listItemType($doc !== false ? $doc : '', $parameter->getName());
            if ($item !== null) {
                return "list<{$item}>";
            }
        }

        return $type instanceof ReflectionNamedType ? $type->getName() : null;
    }

    private static function listItemType(string $docComment, ?string $paramName = null): ?string
    {
        if ($docComment === '') {
            return null;
        }

        $pattern = $paramName !== null
            ? '/@param\s+list<([^>]+)>\s+\$' . preg_quote($paramName, '/') . '\b/'
            : '/@var\s+list<([^>]+)>/';

        return preg_match($pattern, $docComment, $m) === 1 ? ltrim(trim($m[1]), '\\') : null;
    }
}
