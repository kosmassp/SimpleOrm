<?php

declare(strict_types=1);

namespace SimpleOrm\Metadata;

use ReflectionNamedType;
use ReflectionProperty;

/**
 * Resolves a mapped property's neutral type from its declared PHP type (§7.9,
 * CODING-STANDARD §10): `#[Column(type:)]` wins when given, else
 * {@see ColumnType::fromPhpType()}; `#[EnumAsInt]` switches a genuine enum to
 * `EnumInt`. An untyped property resolves to a nullable `Custom` — PHP has no
 * compiler-enforced type to read, so nothing more specific can be claimed.
 * Also answers the property-shape questions the relationship loader needs
 * (array-typed collection navigations, nullable-class to-one navigations).
 */
final class PropertyTypes
{
    private function __construct()
    {
    }

    public static function resolve(ReflectionProperty $property, ?ColumnType $explicitType, bool $enumAsInt): ResolvedPropertyType
    {
        $type = $property->getType();

        if ($type === null) {
            return new ResolvedPropertyType($explicitType ?? ColumnType::Custom, null, true);
        }

        if (!$type instanceof ReflectionNamedType) {
            // Union/intersection types are outside the fixed vocabulary; treat as an
            // opaque handler type, nullable if the union admits null.
            return new ResolvedPropertyType($explicitType ?? ColumnType::Custom, null, $type->allowsNull());
        }

        $phpType = $type->getName();

        return new ResolvedPropertyType(
            $explicitType ?? self::defaultType($phpType, $enumAsInt),
            $phpType,
            $type->allowsNull(),
        );
    }

    /** True when the property's declared type is a genuine enum (`#[EnumAsInt]` requires this — MAP-019 otherwise). */
    public static function isEnumType(ReflectionProperty $property): bool
    {
        $type = $property->getType();

        return $type instanceof ReflectionNamedType && enum_exists($type->getName());
    }

    /** True when the property is declared `array` (CODING-STANDARD §10: collection navigations must be). */
    public static function isArrayType(ReflectionProperty $property): bool
    {
        $type = $property->getType();

        return $type instanceof ReflectionNamedType && $type->getName() === 'array';
    }

    /**
     * The nullable entity class a to-one navigation (`#[ManyToOne]`/`#[OneToOne]`)
     * must declare, or null when the shape is wrong (MAP-020): not a class, or
     * not nullable.
     *
     * @return class-string|null
     */
    public static function toOneTargetClass(ReflectionProperty $property): ?string
    {
        $type = $property->getType();
        if (!$type instanceof ReflectionNamedType || $type->isBuiltin() || !$type->allowsNull()) {
            return null;
        }

        /** @var class-string */
        return $type->getName();
    }

    private static function defaultType(string $phpType, bool $enumAsInt): ColumnType
    {
        if ($enumAsInt && enum_exists($phpType)) {
            return ColumnType::EnumInt;
        }

        return ColumnType::fromPhpType($phpType);
    }
}
