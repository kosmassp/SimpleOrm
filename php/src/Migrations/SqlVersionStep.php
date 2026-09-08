<?php

declare(strict_types=1);

namespace SimpleOrm\Migrations;

/**
 * One object step of a data-driven `SqlVersion` (§7.22/§7.23; used by the
 * conformance suite; also the shape a future generator can target): literal
 * up/down SQL, declared renames, and the optional view apply guard, all as data.
 */
final readonly class SqlVersionStep
{
    /**
     * @param list<string> $up
     * @param list<string> $down
     * @param list<ColumnRename> $renames declared column renames — the derived rollback inverts them (ADR-0018)
     * @param string|null $expectDefinition the expected live definition of this step's view, as data — the MIG-012 apply guard
     */
    public function __construct(
        public string $objectName,
        public string $description,
        public array $up,
        public array $down = [],
        public array $renames = [],
        public ?string $expectDefinition = null,
    ) {
    }
}
