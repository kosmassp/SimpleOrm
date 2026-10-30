<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Support;

use SimpleOrm\Metadata\EntityMap;
use SimpleOrm\Metadata\PropertyMap;
use SimpleOrm\Session\Db;

/**
 * Records every write rendering the session takes — the seam through which the observable output of tracking and the
 * unit of work is checked without parsing SQL (spec/session.md "The seam", "Conformance: tracking-cases/",
 * spec/unit-of-work.md "Conformance: flush-cases/"). The session caches its generated CRUD text (ADR-0048 item 1), so the
 * dialect is asked once per text and a wrapping dialect no longer sees every rendering; the recorder listens on the
 * session's test hook {@see Db::observeRenderings()} instead, which reports each rendering taken, cached or not, where
 * the dialect used to be asked (CODING-STANDARD §10).
 *
 * {@see $statements} is every rendered write in order as `{op, entity, columns?}` — the shape `flush-cases` pin
 * (`columns`: `'all'` for a full-row update, the property names (as `PropertyMap::propertyName()`) for a column-list
 * one); {@see $updates} is the update-only projection tracking-cases read (`'all'` or the names, one entry per update
 * statement). An empty list means nothing was rendered. Callers reset the list they read.
 */
final class WriteRenderingRecorder
{
    /** @var list<array{op: string, entity: string, columns?: 'all'|list<string>}> */
    public array $statements = [];

    /** @var list<'all'|list<string>> */
    public array $updates = [];

    /** A recorder listening on `$db`'s render hook. */
    public static function attach(Db $db): self
    {
        $recorder = new self();
        $db->observeRenderings(static function (string $op, EntityMap $map, string|array|null $columns) use ($recorder): void {
            $recorder->record($op, $map, $columns);
        });

        return $recorder;
    }

    /** @param 'all'|list<PropertyMap>|null $columns */
    private function record(string $op, EntityMap $map, string|array|null $columns): void
    {
        if ($op !== 'update') {
            $this->statements[] = ['op' => $op, 'entity' => $map->entityName()];

            return;
        }

        $names = $columns === 'all'
            ? 'all'
            : array_map(static fn (PropertyMap $property): string => $property->propertyName(), (array) $columns);
        $this->updates[] = $names;
        $this->statements[] = ['op' => 'update', 'entity' => $map->entityName(), 'columns' => $names];
    }
}
