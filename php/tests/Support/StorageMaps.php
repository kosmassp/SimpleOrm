<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Support;

use SimpleOrm\Metadata\EntityIndex;
use SimpleOrm\Metadata\EntityMap;
use SimpleOrm\Metadata\KeyStrategy;
use SimpleOrm\Metadata\PropertyMap;
use SimpleOrm\Metadata\RelationKind;
use stdClass;

/**
 * Wraps hand-built {@see PropertyMap}s in the table {@see EntityMap} that `Dialect::storageType($map, $property)` now
 * asks for (ADR-0043: the map says whether a column is a key or belongs to a declared index). Unit tests of storage
 * types need nothing more than a relation name, properties, and optionally indexes.
 */
final class StorageMaps
{
    private function __construct()
    {
    }

    /** @param list<EntityIndex> $indexes */
    public static function of(PropertyMap $property, array $indexes = []): EntityMap
    {
        return new EntityMap(
            entityType: stdClass::class,
            kind: RelationKind::Table,
            relationName: 't',
            schema: null,
            definingSql: null,
            statementParameters: [],
            properties: [$property],
            keyStrategy: $property->key ? KeyStrategy::Natural : KeyStrategy::None,
            indexes: $indexes,
            relationships: [],
        );
    }
}
