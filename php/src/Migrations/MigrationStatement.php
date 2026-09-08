<?php

declare(strict_types=1);

namespace SimpleOrm\Migrations;

/**
 * A rendered statement paired with the action it came from, for error reporting
 * (§7.22; mirrors `dotnet/src/SimpleOrm/Migration.cs`'s `MigrationStatement`). A
 * statement carrying `$guardView` is a **precondition**, not SQL to execute: the
 * runner phase compares the view's live definition against `$sql`
 * (whitespace-normalized) before continuing (`MIG-012` on mismatch — views get
 * patched outside code in urgencies, and a migration must not silently overwrite
 * that). Guards still count into the step checksum.
 */
final readonly class MigrationStatement
{
    public function __construct(
        public string $sql,
        public string $origin,
        public ?string $guardView = null,
    ) {
    }
}
