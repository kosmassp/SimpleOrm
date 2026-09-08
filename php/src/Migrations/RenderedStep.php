<?php

declare(strict_types=1);

namespace SimpleOrm\Migrations;

/**
 * A step, fully rendered against one dialect and metadata snapshot — what the
 * runner phase records and applies. `$checksum` is SHA-256 hex of `$up`'s SQL
 * joined with `"\n;\n"` (mirrors `MigrationRunner.cs`'s `ComputeChecksum` byte
 * for byte, including any leading view-guard statement): drift on an applied
 * step is `MIG-010`.
 *
 * Immutable by design (CODING-STANDARD §3): unlike the C# reference, this does
 * not carry a settable `DerivedDownCore` slot — the runner phase resolves a
 * step's derived rollback (`DownDeriver::derive()`) and holds the result
 * alongside this value rather than mutating it.
 */
final readonly class RenderedStep
{
    public string $checksum;

    /**
     * @param list<MigrationStatement> $up
     * @param list<ColumnRename> $upRenames
     */
    public function __construct(
        public int $version,
        public string $objectName,
        public string $description,
        public array $up,
        public DownPlan $down,
        public array $upRenames,
    ) {
        $this->checksum = self::computeChecksum($up);
    }

    /** @param list<MigrationStatement> $statements */
    private static function computeChecksum(array $statements): string
    {
        $sql = implode("\n;\n", array_map(static fn (MigrationStatement $s): string => $s->sql, $statements));

        return hash('sha256', $sql);
    }
}
