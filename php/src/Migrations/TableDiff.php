<?php

declare(strict_types=1);

namespace SimpleOrm\Migrations;

/**
 * The result of `MigrationGenerator::diff()` (ADR-0017): what changed between
 * the current model and the latest committed snapshot. The generator's
 * emitters turn this into migration source; `Diff\DiffCommand` gates `$removed`
 * behind `--allow-remove` (`DDL-003`) and presents `$unsupported` as `DDL-004`
 * — `diff()` itself only reports.
 */
final readonly class TableDiff
{
    /**
     * @param list<ColumnSpec> $added
     * @param list<ColumnSpec> $removed
     * @param list<ColumnRename> $renamed
     * @param list<string> $addedIndexSql
     * @param list<string> $removedIndexNames
     * @param list<string> $unsupported changes that cannot be expressed safely (type/nullability changes, non-nullable additions) — write these migrations by hand
     */
    public function __construct(
        public bool $isNew,
        public array $added,
        public array $removed,
        public array $renamed,
        public array $addedIndexSql,
        public array $removedIndexNames,
        public array $unsupported,
    ) {
    }

    public function hasChanges(): bool
    {
        return $this->isNew
            || $this->added !== []
            || $this->removed !== []
            || $this->renamed !== []
            || $this->addedIndexSql !== []
            || $this->removedIndexNames !== [];
    }
}
