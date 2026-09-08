<?php

declare(strict_types=1);

namespace SimpleOrm\Metadata;

use ReflectionProperty;

/**
 * The two visibility questions the loaders ask of a property (CODING-STANDARD
 * §10, ADR-0004/0005 add.2): whether it is open enough to need a mapping
 * decision at all, and whether a navigation's write access is safely restricted
 * to the library. PHP 8.4 asymmetric visibility replaces C#'s `{ get; private
 * set; }` check; readonly is a distinct mechanism and is rejected for
 * navigations even though `ReflectionProperty::isProtectedSet()` reports true
 * for a plain `readonly` property under the hood.
 */
final class PropertyVisibility
{
    private function __construct()
    {
    }

    /**
     * Read and write both public: the ADR-0004 property that needs `#[Column]`,
     * `#[Ignore]`, or a relationship attribute (MAP-010), and the one the
     * convention loader maps automatically.
     */
    public static function isPubliclySettable(ReflectionProperty $property): bool
    {
        return $property->isPublic()
            && !$property->isStatic()
            && !$property->isReadOnly()
            && !$property->isPrivateSet()
            && !$property->isProtectedSet();
    }

    /** True when a navigation's setter is not safely restricted to the library — MAP-011. */
    public static function isInvalidNavigation(ReflectionProperty $property): bool
    {
        return $property->isReadOnly() || !($property->isPrivateSet() || $property->isProtectedSet());
    }
}
