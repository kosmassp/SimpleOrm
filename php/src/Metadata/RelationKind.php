<?php

declare(strict_types=1);

namespace SimpleOrm\Metadata;

/** What backs an entity (ADR-0008): exactly one per class. The backing value is the export token. */
enum RelationKind: string
{
    case Table = 'table';
    case View = 'view';
    case MaterializedView = 'materialized_view';
    case Statement = 'statement';
    case Procedure = 'procedure';

    /** Whether the source has a name a query can select from (statements do not, ADR-0011). */
    public function isNamed(): bool
    {
        return $this !== self::Statement;
    }

    /** Whether generated CRUD may write to it (ADR-0008: only tables). */
    public function isWritable(): bool
    {
        return $this === self::Table;
    }
}
