<?php

declare(strict_types=1);

namespace SimpleOrm\Migrations;

/** One `(version, object)` row of `MigrationRunner::status()` (§7.23); mirrors `MigrationRunner.cs`'s `MigrationEntry`. */
final readonly class MigrationEntry
{
    public function __construct(
        public int $version,
        public string $objectName,
        public string $description,
        public MigrationState $state,
    ) {
    }

    public function __toString(): string
    {
        return sprintf('V%04d %-28s %-30s %s', $this->version, $this->objectName, $this->description, $this->state->value);
    }
}
