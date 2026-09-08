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
        public ?OwnedMap $owner = null,
    ) {
    }

    /** The property path: a direct member's name, or `navigation.member` for an owned member (ADR-0030) — the name criteria and column lists use. */
    public function propertyName(): string
    {
        return $this->owner === null
            ? $this->property->getName()
            : $this->owner->propertyName() . '.' . $this->property->getName();
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

    /** Reads the member through its path; an owned member of a null navigation reads as null. */
    public function getValue(object $entity): mixed
    {
        $holder = $this->owner === null ? $entity : self::read($this->owner->property, $entity);
        if ($holder === null) {
            return null;
        }

        return self::read($this->property, $holder);
    }

    /**
     * Writes through `private(set)` and `readonly` alike: the library is the only
     * writer of navigations (MAP-011). An owned member (ADR-0030) writes through
     * its path, creating the owned instance on first write.
     */
    public function setValue(object $entity, mixed $value): void
    {
        if ($this->owner === null) {
            $this->property->setValue($entity, $value);

            return;
        }

        $owned = self::read($this->owner->property, $entity);
        if ($owned === null) {
            $ownedType = $this->owner->ownedType;
            $owned = new $ownedType();
            $this->owner->property->setValue($entity, $owned);
        }

        $this->property->setValue($owned, $value);
    }

    private static function read(ReflectionProperty $property, object $holder): mixed
    {
        return $property->isInitialized($holder) ? $property->getValue($holder) : null;
    }
}
