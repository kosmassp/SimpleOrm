<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Migrations\Support;

use ReflectionProperty;
use SimpleOrm\Metadata\ColumnType;
use SimpleOrm\Metadata\PropertyMap;

/**
 * @internal test helper — builds `PropertyMap`s directly from a plain fixture
 * class's declared properties, bypassing the not-yet-landed `EntityMapLoader`.
 * Migrations tests need real `EntityMap`s (the loader's own tests own loader
 * correctness); this is the least amount of scaffolding that gets there.
 */
final class PropertyMapFactory
{
    private function __construct()
    {
    }

    /** @param class-string $class */
    public static function make(
        string $class,
        string $propertyName,
        string $columnName,
        ColumnType $type,
        bool $nullable = false,
        bool $key = false,
        bool $generated = false,
        bool $version = false,
    ): PropertyMap {
        return new PropertyMap(
            new ReflectionProperty($class, $propertyName),
            $columnName,
            $type,
            null,
            $nullable,
            $key,
            $generated,
            $version,
        );
    }
}
