<?php

declare(strict_types=1);

namespace SimpleOrm\Migrations;

/**
 * One declared column rename, in the order the author wrote it (§7.22, ADR-0018).
 * Snapshots alone cannot tell a rename from a drop+add, so the typed
 * `TableActions::renameColumn()` call records the pair here; the derived
 * rollback (`DownDeriver`) inverts them data-preservingly before diffing shapes.
 */
final readonly class ColumnRename
{
    public function __construct(
        public string $from,
        public string $to,
    ) {
    }
}
