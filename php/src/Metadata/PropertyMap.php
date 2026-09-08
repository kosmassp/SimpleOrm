<?php

declare(strict_types=1);

namespace SimpleOrm\Metadata;

use ReflectionProperty;

/**
 * One mapped property ↔ column pair (§7.1). The loader resolves the neutral
 * `ColumnType` once; conversion, storage types, and the export read `$type` and
 * never re-derive it from the PHP type. `$phpType` is kept for construction and
 * handler lookup (`class-string` for enums, `Decimal`, `DateTimeImmutable`;
 * a builtin name otherwise; null when the property is untyped).
 */
final readonly class PropertyMap
{
    /** @param class-string|null $foreignKeyReferences entity this FK column references (`#[ForeignKey]`), if declared */
    public function __construct(
        public ReflectionProperty $property,
        public string $columnName,
        public ColumnType $type,
        public ?string $phpType,
        public bool $nullable,
        public bool $key = false,
        public bool $generated = false,
        public bool $version = false,
        public ?string $foreignKeyReferences = null,
    ) {
    }

    public function propertyName(): string
    {
        return $this->property->getName();
    }

    /** `Type.property`, the target form error messages use. */
    public function target(): string
    {
        return $this->property->getDeclaringClass()->getShortName() . '.' . $this->property->getName();
    }

    /** The §7.9 enum storage flag, carried by the token. */
    public function enumAsInt(): bool
    {
        return $this->type === ColumnType::EnumInt;
    }

    /** Whether the property must be present in a row for construction to succeed (`MAP-002`). */
    public function isRequired(): bool
    {
        return !$this->nullable && !$this->property->hasDefaultValue() && !$this->property->isPromoted();
    }

    public function getValue(object $entity): mixed
    {
        return $this->property->isInitialized($entity) ? $this->property->getValue($entity) : null;
    }

    /** Writes through `private(set)` and `readonly` alike: the library is the only writer of navigations (MAP-011). */
    public function setValue(object $entity, mixed $value): void
    {
        $this->property->setValue($entity, $value);
    }
}
