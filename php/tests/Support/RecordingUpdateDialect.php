<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Support;

use SimpleOrm\Metadata\EntityMap;
use SimpleOrm\Metadata\PropertyMap;

/**
 * Records what the session asked the dialect to render for an update — the seam through which
 * tracking's observable output is checked without parsing SQL (spec/session.md "Conformance:
 * tracking-cases/"): `'all'` for a full-row {@see updateSql()}, the property names (as
 * `PropertyMap::propertyName()`) for a column-list {@see updateOnlySql()}. One entry per
 * rendered update statement, in order; an empty list means no update statement was rendered.
 */
final class RecordingUpdateDialect extends DelegatingDialect
{
    /** @var list<'all'|list<string>> */
    public array $updates = [];

    public function updateSql(EntityMap $map): string
    {
        $this->updates[] = 'all';

        return $this->inner->updateSql($map);
    }

    public function updateOnlySql(EntityMap $map, array $properties): string
    {
        $this->updates[] = array_map(
            static fn (PropertyMap $property): string => $property->propertyName(),
            $properties,
        );

        return $this->inner->updateOnlySql($map, $properties);
    }
}
