<?php

declare(strict_types=1);

namespace SimpleOrm\Metadata;

use ReflectionClass;
use SimpleOrm\Errors\MappingError;
use SimpleOrm\Errors\MappingException;
use SimpleOrm\Metadata\Attributes\Index;
use SimpleOrm\Metadata\Attributes\MaterializedView;
use SimpleOrm\Metadata\Attributes\Procedure;
use SimpleOrm\Metadata\Attributes\Statement;
use SimpleOrm\Metadata\Attributes\Table;
use SimpleOrm\Metadata\Attributes\View;

/**
 * `MAP-028` - a base class is never an entity (ADR-0041, spec/metadata-model.md). A base class shares
 * members only; identity comes from the concrete class alone. Three cases, one home for each:
 *
 *  1. {@see self::assertConcrete()} - an abstract class loaded as an entity by any path (the one map-loading
 *     entry point, the builder, and the explicit registrations call it);
 *  2. {@see self::parentDeclarationErrors()} - a mapped class whose parent chain carries a relation source or
 *     an `#[Index]` of its own (collected with the class's other mapping errors). PHP class attributes are
 *     never inherited by reflection (`getAttributes()` reads the class itself), so a subclass can never
 *     adopt its parent's table - there is nothing extra to switch off;
 *  3. {@see self::scan()} / {@see self::scanErrors()} - in an assembly scan, a concrete class without a
 *     relation source that another scanned mapped class extends. The one case a lone load cannot see (such a
 *     class legitimately maps to a convention-named table on its own); SchemaGuard and the CLI both use it.
 */
final class BaseClassRules
{
    /** @var list<class-string> the relation-source attributes plus `#[Index]`, the declarations a base class may not carry */
    private const FORBIDDEN_ON_BASE = [Table::class, View::class, MaterializedView::class, Statement::class, Procedure::class, Index::class];

    /** @var list<class-string> the relation sources alone */
    private const RELATION_SOURCES = [Table::class, View::class, MaterializedView::class, Statement::class, Procedure::class];

    private function __construct()
    {
    }

    /**
     * Case 1: refuses an abstract class (or interface) used as an entity.
     *
     * @param class-string $entityType
     * @throws MappingException `MAP-028`
     */
    public static function assertConcrete(string $entityType): void
    {
        $class = new ReflectionClass($entityType);
        if ($class->isAbstract() || $class->isInterface()) {
            throw new MappingException($entityType, [new MappingError(
                'MAP-028',
                $class->getShortName(),
                'is abstract, so it is a base class, not an entity; a base class shares members only and is never read, '
                    . 'written, queried, exported, or targeted by a navigation - use the concrete subclass',
            )]);
        }
    }

    /**
     * Case 2: one error per ancestor that carries a relation source or an `#[Index]` of its own, naming both
     * classes and the declarations found.
     *
     * @param ReflectionClass<object> $class
     * @return list<MappingError>
     */
    public static function parentDeclarationErrors(ReflectionClass $class): array
    {
        $errors = [];
        for ($ancestor = $class->getParentClass(); $ancestor !== false; $ancestor = $ancestor->getParentClass()) {
            $found = [];
            foreach (self::FORBIDDEN_ON_BASE as $attribute) {
                if ($ancestor->getAttributes($attribute) !== []) {
                    $found[] = '#[' . substr($attribute, (int) strrpos($attribute, '\\') + 1) . ']';
                }
            }

            if ($found !== []) {
                $errors[] = new MappingError(
                    'MAP-028',
                    $class->getShortName(),
                    "extends {$ancestor->getShortName()}, whose own declaration " . implode(', ', $found)
                        . ' makes it an entity; a base class shares members only and may not carry a relation source or an '
                        . "#[Index] - drop the declaration from {$ancestor->getShortName()} or stop extending it",
                );
            }
        }

        return $errors;
    }

    /**
     * Case 3: the base classes in a scan, each with the scanned mapped classes extending it (directly or through
     * other classes), both sorted by name. A base is a concrete, mapped, non-owned class carrying no relation
     * source of its own; an extender is any scanned mapped, non-owned class (abstract ones included) below it.
     *
     * @param list<class-string> $classes
     * @return array<class-string, list<class-string>>
     */
    public static function scan(array $classes): array
    {
        /** @var array<class-string, ReflectionClass<object>> $mapped */
        $mapped = [];
        foreach ($classes as $class) {
            $reflection = new ReflectionClass($class);
            if (!$reflection->isInterface() && !$reflection->isTrait() && !$reflection->isEnum()
                && EntityMapLoader::hasMappingAttributes($class) && !EntityMapLoader::isOwnedType($class)) {
                $mapped[$reflection->getName()] = $reflection;
            }
        }

        $bases = [];
        foreach ($mapped as $name => $reflection) {
            for ($ancestor = $reflection->getParentClass(); $ancestor !== false; $ancestor = $ancestor->getParentClass()) {
                $candidate = $mapped[$ancestor->getName()] ?? null;
                if ($candidate !== null && !$candidate->isAbstract() && !self::hasRelationSource($candidate)) {
                    $bases[$candidate->getName()][] = $name;
                }
            }
        }

        ksort($bases, SORT_STRING);
        foreach ($bases as &$extenders) {
            sort($extenders, SORT_STRING);
        }
        unset($extenders);

        return $bases;
    }

    /**
     * Case 3 as reportable errors: one per base class, naming the extending classes.
     *
     * @param list<class-string> $classes
     * @return list<MappingError>
     */
    public static function scanErrors(array $classes): array
    {
        $errors = [];
        foreach (self::scan($classes) as $base => $extenders) {
            $shortExtenders = array_map(static fn (string $c): string => (new ReflectionClass($c))->getShortName(), $extenders);
            $errors[] = new MappingError(
                'MAP-028',
                (new ReflectionClass($base))->getShortName(),
                'is a concrete class without a relation source that ' . implode(', ', $shortExtenders)
                    . ' extend, so it is a base class, not an entity; declare it abstract (a base class shares members only)',
            );
        }

        return $errors;
    }

    /** @param ReflectionClass<object> $class */
    private static function hasRelationSource(ReflectionClass $class): bool
    {
        foreach (self::RELATION_SOURCES as $attribute) {
            if ($class->getAttributes($attribute) !== []) {
                return true;
            }
        }

        return false;
    }
}
