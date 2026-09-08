<?php

declare(strict_types=1);

namespace SimpleOrm\Parameters;

use ReflectionClass;
use ReflectionProperty;
use SimpleOrm\Errors\SimpleOrmException;
use SimpleOrm\Mapping\TypeConverter;
use Traversable;

/**
 * Binds `@name` placeholders from the public properties of the args object
 * (§7.12/§7.13, mirrors dotnet/src/SimpleOrm/ParameterBinder.cs). Both
 * directions are strict: an unmatched placeholder is `PRM-001`, an unused
 * property is `PRM-002`. A collection-typed property (array or `Traversable`;
 * strings are values, never collections) expands `IN (@ids)` to generated
 * placeholders (`@ids_0..@ids_N`); an empty collection becomes SQL `NULL`. On a
 * dialect with native array parameters (`$arrayParameters`, ADR-0025) the
 * collection binds as one parameter instead and the SQL is left untouched.
 */
final class ParameterBinder
{
    public static function bind(
        string $sql,
        object $args,
        string $queryName,
        TypeConverter $converter,
        bool $arrayParameters = false,
    ): BoundSql {
        $properties = self::publicProperties($args);
        $placeholders = SqlPlaceholders::find($sql);
        $argsType = $args::class;

        foreach ($placeholders as $placeholder) {
            if (!self::hasProperty($properties, $placeholder)) {
                throw new SimpleOrmException(
                    'PRM-001',
                    $queryName,
                    "SQL parameter @{$placeholder} has no matching property on {$argsType}",
                );
            }
        }

        $text = $sql;
        $parameters = [];
        foreach ($properties as $name => $value) {
            $placeholder = self::matchingPlaceholder($placeholders, $name);
            if ($placeholder === null) {
                throw new SimpleOrmException(
                    'PRM-002',
                    $queryName,
                    "property {$argsType}.{$name} is never used by the SQL",
                );
            }

            $context = "{$queryName} @{$placeholder}";
            if (self::isCollection($value)) {
                $items = is_array($value) ? array_values($value) : iterator_to_array($value, preserve_keys: false);
                if ($arrayParameters) {
                    $parameters[$placeholder] = array_map(
                        static fn (mixed $item): mixed => $converter->toDatabase($item, $context),
                        $items,
                    );
                } else {
                    $text = self::expandList($text, $placeholder, $items, $converter, $context, $parameters);
                }
            } else {
                $parameters[$placeholder] = $converter->toDatabase($value, $context);
            }
        }

        return new BoundSql(SqlPlaceholders::toPdo($text), $parameters);
    }

    /**
     * Rewrites every real occurrence of `@placeholder` to its expanded list
     * (`@placeholder_0, @placeholder_1, …`, or SQL `NULL` when empty) and adds
     * one bound parameter per element.
     *
     * @param list<mixed> $values
     * @param array<string, mixed> $parameters appended to by reference
     */
    private static function expandList(
        string $sql,
        string $placeholder,
        array $values,
        TypeConverter $converter,
        string $context,
        array &$parameters,
    ): string {
        $names = [];
        foreach ($values as $index => $value) {
            $name = "{$placeholder}_{$index}";
            $names[] = $name;
            $parameters[$name] = $converter->toDatabase($value, $context);
        }

        // An empty list becomes NULL: "x IN (NULL)" is valid SQL matching no rows.
        $replacement = $names === [] ? 'NULL' : implode(', ', array_map(static fn (string $n): string => '@' . $n, $names));

        $result = $sql;
        foreach (array_reverse(SqlPlaceholders::occurrences($sql, $placeholder)) as [$start, $length]) {
            $result = substr_replace($result, $replacement, $start, $length);
        }

        return $result;
    }

    /** @return array<string, mixed> declared public instance properties, in declaration order */
    private static function publicProperties(object $args): array
    {
        $result = [];
        $reflection = new ReflectionClass($args);
        foreach ($reflection->getProperties(ReflectionProperty::IS_PUBLIC) as $property) {
            if ($property->isStatic()) {
                continue;
            }

            $result[$property->getName()] = $property->isInitialized($args) ? $property->getValue($args) : null;
        }

        return $result;
    }

    /** @param array<string, mixed> $properties */
    private static function hasProperty(array $properties, string $placeholder): bool
    {
        foreach (array_keys($properties) as $name) {
            if (strcasecmp($name, $placeholder) === 0) {
                return true;
            }
        }

        return false;
    }

    /** @param list<string> $placeholders */
    private static function matchingPlaceholder(array $placeholders, string $propertyName): ?string
    {
        foreach ($placeholders as $placeholder) {
            if (strcasecmp($placeholder, $propertyName) === 0) {
                return $placeholder;
            }
        }

        return null;
    }

    /** A collection binds as an IN-list expansion; a string is a value, never its characters. */
    private static function isCollection(mixed $value): bool
    {
        return is_array($value) || $value instanceof Traversable;
    }
}
