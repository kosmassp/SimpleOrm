<?php

declare(strict_types=1);

namespace SimpleOrm\Migrations;

/**
 * A data-driven version (§7.22/§7.23) — raw SQL steps constructed
 * programmatically (used by the conformance suite; also the shape a future
 * generator can target). {@see self::draft()} builds the draft-marked form
 * (Level 3 M5; CODING-STANDARD §10: the step list is variadic, so the flag cannot follow it).
 */
final class SqlVersion extends MigrationVersion
{
    /** @var list<SqlVersionStep> */
    private readonly array $steps;

    private bool $draft = false;

    public function __construct(
        private readonly int $rootVersion,
        SqlVersionStep ...$steps,
    ) {
        $this->steps = $steps;
    }

    /** The data-driven counterpart of a root carrying `#[Draft]`: `migrate` and `baseline` refuse it while pending (`MIG-013`). */
    public static function draft(int $rootVersion, SqlVersionStep ...$steps): self
    {
        $version = new self($rootVersion, ...$steps);
        $version->draft = true;

        return $version;
    }

    public function version(): int
    {
        return $this->rootVersion;
    }

    public function isDraft(): bool
    {
        return $this->draft;
    }

    public function compose(VersionBuilder $version): void
    {
        foreach ($this->steps as $step) {
            $version->apply(new SqlVersionRawStep($this->rootVersion, $step));
        }
    }
}
