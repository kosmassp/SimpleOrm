<?php

declare(strict_types=1);

namespace SimpleOrm\Migrations;

/**
 * The result of `MigrationGenerator::diffTrigger()` (ADR-0042): how a trigger's current declaration differs from its
 * latest snapshot. Triggers diff **by declaration** — neutral on both sides, nothing rendered: no snapshot is new; a
 * different relation, timing, event set, update-of list, or whitespace-normalized body is a change; a snapshot history
 * no entity declares any more is a removal (gated by `--allow-remove`, `DDL-003`, in `Diff\DiffCommand`).
 */
final readonly class TriggerDiff
{
    public function __construct(
        public bool $isNew,
        public bool $isChanged,
        public bool $isRemoved,
    ) {
    }

    public function hasChanges(): bool
    {
        return $this->isNew || $this->isChanged || $this->isRemoved;
    }
}
