<?php

declare(strict_types=1);

namespace SimpleOrm\Parameters;

use ReflectionClass;
use ReflectionProperty;
use SimpleOrm\Errors\SimpleOrmException;
use SimpleOrm\Mapping\TypeConverter;
use SimpleOrm\Metadata\Attributes\Column;
use SimpleOrm\Metadata\ColumnType;
use Traversable;

/**
 * Binds `@name` placeholders from the public properties of the args object
 * (§7.12/§7.13, mirrors dotnet/src/SimpleOrm/ParameterBinder.cs). Both
 * directions are strict: an unmatched placeholder is `PRM-001`, an unused
 * property is `PRM-002`. A collection-typed property (array or `Traversable`;
 * strings are values, never collections) expands `IN (@ids)` to generated
 * placeholders (`@ids_0..@ids_N`); an empty collection becomes SQL `NULL`. On a
 * dialect with native array parameters (`$arrayParameters`, ADR-0025) the
 * collection binds as one parameter instead and the SQL is left untouched. An
 * args property may declare its neutral token with `#[Column(type: …)]`; a
 * `bytes` token binds the value as a binary parameter (ADR-0040 add.1).
 */
final class ParameterBinder
{
    /** How many distinct SQL texts keep a binding plan; the table is emptied when full. */
    public const int MAX_CACHED_TEXTS = 1024;

    /** @var array<string, array<class-string, array{properties: list<array{0: string, 1: ReflectionProperty, 2: ?string, 3: ?ColumnType}>, unmatched: ?string}>> */
    private static array $plans = [];

    private static int $planned = 0;

    /** How many binding plans have been built in this process — the cache's test hook. @internal */
    public static function planCount(): int
    {
        return self::$planned;
    }

    public static function bind(
        string $sql,
        object $args,
        string $queryName,
        TypeConverter $converter,
        bool $arrayParameters = false,
    ): BoundSql {
        $bound = self::bindKeepingPlaceholders($sql, $args, $queryName, $converter, $arrayParameters);

        return new BoundSql(SqlPlaceholders::toPdo($bound->sql), $bound->parameters);
    }

    /**
     * The same binding with the SQL left in its `@name` form (any IN-list already expanded), for a caller that renames the
     * placeholders itself before PDO sees them — a planned message of several statements (ADR-0044). The returned
     * {@see BoundSql::$sql} is **not** PDO-ready.
     */
    public static function bindKeepingPlaceholders(
        string $sql,
        object $args,
        string $queryName,
        TypeConverter $converter,
        bool $arrayParameters = false,
    ): BoundSql {
        $argsType = $args::class;
        $plan = self::plan($sql, $argsType);
        // Every value is read before anything is judged, as the uncached binder did (a property hook runs first either way).
        $values = [];
        foreach ($plan['properties'] as $i => [, $property]) {
            $values[$i] = $property->isInitialized($args) ? $property->getValue($args) : null;
        }

        if ($plan['unmatched'] !== null) {
            throw new SimpleOrmException(
                'PRM-001',
                $queryName,
                "SQL parameter @{$plan['unmatched']} has no matching property on {$argsType}",
            );
        }

        $text = $sql;
        $parameters = [];
        foreach ($plan['properties'] as $i => [$name, , $placeholder, $token]) {
            $value = $values[$i];
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
                    // One array literal: its elements are text inside it, never separate binary parameters.
                    $parameters[$placeholder] = array_map(
                        static fn (mixed $item): mixed => $converter->toDatabase($item, $context, type: $token),
                        $items,
                    );
                } else {
                    $text = self::expandList($text, $placeholder, $items, $converter, $context, $token, $parameters);
                }
            } else {
                $parameters[$placeholder] = $converter->toParameter($value, $context, type: $token);
            }
        }

        return new BoundSql($text, $parameters);
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
        ?ColumnType $token,
        array &$parameters,
    ): string {
        $names = [];
        foreach ($values as $index => $value) {
            $name = "{$placeholder}_{$index}";
            $names[] = $name;
            $parameters[$name] = $converter->toParameter($value, $context, type: $token);
        }

        // An empty list becomes NULL: "x IN (NULL)" is valid SQL matching no rows.
        $replacement = $names === [] ? 'NULL' : implode(', ', array_map(static fn (string $n): string => '@' . $n, $names));

        $result = $sql;
        foreach (array_reverse(SqlPlaceholders::occurrences($sql, $placeholder)) as [$start, $length]) {
            $result = substr_replace($result, $replacement, $start, $length);
        }

        return $result;
    }

    /**
     * The binding plan of one SQL text for one args class, computed once (ADR-0048 item 2): the declared public instance
     * properties in declaration order, each with its declared token and its placeholder (null = `PRM-002`, still thrown
     * in property order while binding, so an earlier property's conversion error still wins), and the first placeholder
     * no property matches (`PRM-001`). Keyed by the text and the class name — the text is the whole input of the scan,
     * and a class name is one class in a PHP process (CODING-STANDARD §10) — in a bounded table.
     *
     * @param class-string $argsType
     * @return array{properties: list<array{0: string, 1: ReflectionProperty, 2: ?string, 3: ?ColumnType}>, unmatched: ?string}
     */
    private static function plan(string $sql, string $argsType): array
    {
        if (isset(self::$plans[$sql][$argsType])) {
            return self::$plans[$sql][$argsType];
        }

        self::$planned++;
        $reflection = new ReflectionClass($argsType);
        $tokens = self::declaredTokens($reflection);
        $placeholders = SqlPlaceholders::find($sql);

        $properties = [];
        foreach ($reflection->getProperties(ReflectionProperty::IS_PUBLIC) as $property) {
            if ($property->isStatic()) {
                continue;
            }

            $name = $property->getName();
            $properties[] = [$name, $property, self::matchingPlaceholder($placeholders, $name), $tokens[$name] ?? null];
        }

        $unmatched = null;
        foreach ($placeholders as $placeholder) {
            if (self::matchingPlaceholder(array_column($properties, 0), $placeholder) === null) {
                $unmatched = $placeholder;
                break;
            }
        }

        if (count(self::$plans) >= self::MAX_CACHED_TEXTS) {
            self::$plans = [];
        }

        return self::$plans[$sql][$argsType] = ['properties' => $properties, 'unmatched' => $unmatched];
    }

    /**
     * The neutral type token an args property declares with `#[Column(type: …)]`, by property
     * name — the same override an entity property uses (CODING-STANDARD §10). PHP types alone
     * cannot say `bytes` (one string type), `date`, `time`, or `datetimeoffset`; an args property
     * without the attribute binds by its value, as before. Only the token is read: an args type is
     * not an entity, so the attribute's column name means nothing here.
     *
     * @return array<string, ColumnType>
     */
    private static function declaredTokens(ReflectionClass $reflection): array
    {
        $result = [];
        foreach ($reflection->getProperties(ReflectionProperty::IS_PUBLIC) as $property) {
            foreach ($property->getAttributes(Column::class) as $attribute) {
                $type = $attribute->newInstance()->type;
                if ($type !== null) {
                    $result[$property->getName()] = $type;
                }
            }
        }

        return $result;
    }

    /**
     * The first of `$candidates` equal to `$name` ignoring case — a property's placeholder, or a placeholder's property.
     *
     * @param list<string> $candidates
     */
    private static function matchingPlaceholder(array $candidates, string $name): ?string
    {
        foreach ($candidates as $candidate) {
            if (strcasecmp($candidate, $name) === 0) {
                return $candidate;
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
