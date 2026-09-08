<?php

declare(strict_types=1);

namespace SimpleOrm\Mapping;

use DateTimeImmutable;
use InvalidArgumentException;
use ReflectionClass;
use ReflectionNamedType;
use ReflectionProperty;
use SimpleOrm\Naming\SnakeCaseNamingConvention;
use SimpleOrm\Types\Decimal;
use SimpleOrm\Types\Iso8601;
use UnitEnum;

/**
 * The §7.10 JSON-column handler: a TEXT column holding a JSON object (or, with
 * `$list`, a `json_group_array` of objects) deserializes into a plain PHP type
 * via its constructor or public properties, snake_case keys matched
 * case-insensitively — mirrors dotnet's `JsonTypeHandler<T>` (System.Text.Json,
 * snake_case naming policy) as far as reflection-only PHP allows.
 *
 * Deliberately small: it hydrates **flat** DTOs only — scalar properties,
 * {@see Decimal}, `DateTimeImmutable` (the §7.9 UTC convention), and pure enums
 * by case name. No nested objects, no collections inside one item, and no
 * handler delegation for a member's own type. A shape beyond that needs its own
 * `TypeHandler`.
 *
 * `type()` returns the item class for the single-object form; for the list form
 * (`json_group_array` nesting) it returns the synthetic key `"list<Item>"` —
 * PHP has no generic class-string for `list<Item>`, so callers registering or
 * looking up a list handler must use the same convention.
 */
final class JsonTypeHandler implements TypeHandler
{
    /** @param class-string $itemType */
    public function __construct(
        private readonly string $itemType,
        private readonly bool $list = false,
    ) {
    }

    public function type(): string
    {
        return $this->list ? "list<{$this->itemType}>" : $this->itemType;
    }

    public function parse(mixed $databaseValue): mixed
    {
        $decoded = json_decode((string) $databaseValue, associative: true, flags: JSON_THROW_ON_ERROR);
        if ($this->list) {
            /** @var list<array<string, mixed>> $decoded */
            return array_map(fn (array $item): object => $this->hydrate($item), $decoded);
        }

        /** @var array<string, mixed> $decoded */
        return $this->hydrate($decoded);
    }

    public function format(mixed $value): mixed
    {
        if ($this->list) {
            /** @var list<object> $value */
            return json_encode(array_map($this->dehydrate(...), $value), JSON_THROW_ON_ERROR);
        }

        return json_encode($this->dehydrate($value), JSON_THROW_ON_ERROR);
    }

    /** @param array<string, mixed> $data snake_case keys from the JSON document */
    private function hydrate(array $data): object
    {
        $byProperty = [];
        foreach ($data as $key => $value) {
            $byProperty[self::toCamel($key)] = $value;
        }

        $class = new ReflectionClass($this->itemType);
        $constructor = $class->getConstructor();
        if ($constructor !== null && $constructor->getNumberOfParameters() > 0) {
            $arguments = [];
            foreach ($constructor->getParameters() as $parameter) {
                $raw = $byProperty[$parameter->getName()] ?? null;
                $arguments[] = self::coerce($raw, $parameter->getType());
            }

            return $class->newInstanceArgs($arguments);
        }

        $instance = $class->newInstanceWithoutConstructor();
        foreach ($class->getProperties(ReflectionProperty::IS_PUBLIC) as $property) {
            if (array_key_exists($property->getName(), $byProperty)) {
                $property->setValue($instance, self::coerce($byProperty[$property->getName()], $property->getType()));
            }
        }

        return $instance;
    }

    /** @return array<string, mixed> */
    private function dehydrate(object $value): array
    {
        $out = [];
        $class = new ReflectionClass($value);
        foreach ($class->getProperties(ReflectionProperty::IS_PUBLIC) as $property) {
            if (!$property->isInitialized($value)) {
                continue;
            }

            $out[self::toSnake($property->getName())] = self::plain($property->getValue($value));
        }

        return $out;
    }

    private static function coerce(mixed $raw, mixed $type): mixed
    {
        if ($raw === null || !$type instanceof ReflectionNamedType) {
            return $raw;
        }

        $name = $type->getName();

        return match (true) {
            $name === 'int' => (int) $raw,
            $name === 'float' => (float) $raw,
            $name === 'bool' => (bool) $raw,
            $name === 'string' => (string) $raw,
            $name === Decimal::class => is_string($raw) ? Decimal::of($raw) : Decimal::of((string) $raw),
            $name === DateTimeImmutable::class => new DateTimeImmutable((string) $raw),
            enum_exists($name) => self::enumCase($name, $raw),
            default => $raw,
        };
    }

    /** @param class-string $enumClass */
    private static function enumCase(string $enumClass, mixed $raw): UnitEnum
    {
        if (is_string($raw)) {
            foreach ($enumClass::cases() as $case) {
                if (strcasecmp($case->name, $raw) === 0) {
                    return $case;
                }
            }
        }

        throw new InvalidArgumentException("'{$raw}' is not a case of {$enumClass}");
    }

    private static function plain(mixed $value): mixed
    {
        return match (true) {
            $value instanceof Decimal => (string) $value,
            $value instanceof DateTimeImmutable => Iso8601::format($value),
            $value instanceof UnitEnum => $value->name,
            default => $value,
        };
    }

    private static function toCamel(string $snake): string
    {
        $parts = explode('_', $snake);
        $first = array_shift($parts) ?? '';
        foreach ($parts as $part) {
            $first .= ucfirst($part);
        }

        return $first;
    }

    /** Snake_cases via the shared {@see SnakeCaseNamingConvention} — JSON columns always use it, independent of the entity's own convention. */
    private static function toSnake(string $camel): string
    {
        return (new SnakeCaseNamingConvention())->toDatabase($camel);
    }
}
