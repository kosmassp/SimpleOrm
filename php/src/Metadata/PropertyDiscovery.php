<?php

declare(strict_types=1);

namespace SimpleOrm\Metadata;

use ReflectionClass;
use ReflectionProperty;

/**
 * Property enumeration order (spec/metadata-model.md "Loader precedence"): the
 * most-derived class's declared properties first, in declaration order, then
 * each parent class upward — so a base model's audit columns land last in the
 * export, matching the pinned conformance files. Walks `getParentClass()`
 * explicitly rather than relying on `ReflectionClass::getProperties()`'s
 * unspecified inherited-property ordering.
 */
final class PropertyDiscovery
{
    private function __construct()
    {
    }

    /**
     * Public, non-static instance properties, most-derived class first,
     * declaration order within each class.
     *
     * @param ReflectionClass<object> $class
     * @return list<ReflectionProperty>
     */
    public static function propertiesInDeclarationOrder(ReflectionClass $class): array
    {
        $properties = [];
        for ($current = $class; $current !== false; $current = $current->getParentClass()) {
            foreach ($current->getProperties(ReflectionProperty::IS_PUBLIC) as $property) {
                if ($property->isStatic() || $property->getDeclaringClass()->getName() !== $current->getName()) {
                    // Inherited properties are visited when we reach their declaring class.
                    continue;
                }

                $properties[] = $property;
            }
        }

        return $properties;
    }
}
