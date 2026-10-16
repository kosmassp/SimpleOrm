<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Support;

use SimpleOrm\Metadata\EntityMap;
use SimpleOrm\Metadata\PropertyMap;

/**
 * Records what the session asked the dialect to render for a write — the seam through which
 * the observable output of tracking and the unit of work is checked without parsing SQL
 * (spec/session.md "Conformance: tracking-cases/", spec/unit-of-work.md "Conformance: flush-cases/").
 *
 * {@see $statements} is every rendered write in order as `{op, entity, columns?}` — the shape
 * `flush-cases` pin (`columns`: `'all'` for a full-row update, the property names (as
 * `PropertyMap::propertyName()`) for a column-list one); {@see $updates} is the update-only
 * projection tracking-cases read (`'all'` or the names, one entry per update statement). An empty
 * list means nothing was rendered. Callers reset the list they read.
 */
final class RecordingUpdateDialect extends DelegatingDialect
{
    /** @var list<array{op: string, entity: string, columns?: 'all'|list<string>}> */
    public array $statements = [];

    /** @var list<'all'|list<string>> */
    public array $updates = [];

    public function insertSql(EntityMap $map): string
    {
        $this->statements[] = ['op' => 'insert', 'entity' => $map->entityName()];

        return $this->inner->insertSql($map);
    }

    public function updateSql(EntityMap $map): string
    {
        $this->updates[] = 'all';
        $this->statements[] = ['op' => 'update', 'entity' => $map->entityName(), 'columns' => 'all'];

        return $this->inner->updateSql($map);
    }

    public function updateOnlySql(EntityMap $map, array $properties): string
    {
        $columns = array_map(
            static fn (PropertyMap $property): string => $property->propertyName(),
            $properties,
        );
        $this->updates[] = $columns;
        $this->statements[] = ['op' => 'update', 'entity' => $map->entityName(), 'columns' => $columns];

        return $this->inner->updateOnlySql($map, $properties);
    }

    public function deleteSql(EntityMap $map, bool $checkVersion): string
    {
        $this->statements[] = ['op' => 'delete', 'entity' => $map->entityName()];

        return $this->inner->deleteSql($map, $checkVersion);
    }
}
