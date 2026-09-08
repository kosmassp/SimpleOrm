<?php

declare(strict_types=1);

namespace SimpleOrm\Metadata;

use ReflectionClass;
use SimpleOrm\Errors\MappingException;
use SimpleOrm\Naming\NamingConvention;

/**
 * The convention loader (§7.6): for a type with no mapping attributes at all,
 * every publicly settable property maps by convention; a property named `id`
 * is the key (database-generated for `int`, natural otherwise). A conventional
 * entity is always a table, named by the convention from the class short name.
 */
final class ConventionMapLoader
{
    private function __construct()
    {
    }

    /** @param class-string $entityType */
    public static function load(string $entityType, NamingConvention $convention): EntityMap
    {
        $class = new ReflectionClass($entityType);
        $errors = [];
        $specs = [];

        foreach (PropertyDiscovery::propertiesInDeclarationOrder($class) as $property) {
            if (!PropertyVisibility::isPubliclySettable($property)) {
                continue;
            }

            $isId = $property->getName() === 'id';
            $resolved = PropertyTypes::resolve($property, null, false);
            $specs[] = new MappedPropertySpec(
                $property,
                $resolved->type,
                $resolved->phpType,
                $resolved->nullable,
                isKey: $isId,
                isGenerated: $isId && $resolved->phpType === 'int',
            );
        }

        $map = MapAssembler::assemble(
            $entityType,
            RelationKind::Table,
            $convention->toDatabase($class->getShortName()),
            schema: null,
            statementSql: null,
            statementParameters: [],
            specs: $specs,
            indexSpecs: [],
            relationshipSpecs: [],
            convention: $convention,
            errors: $errors,
        );

        if ($map === null) {
            throw new MappingException($entityType, $errors);
        }

        return $map;
    }
}
