<?php

declare(strict_types=1);

namespace SimpleOrm\Migrations;

/**
 * A data-driven version — raw SQL steps constructed programmatically (used by
 * the conformance suite; also the shape a future generator can target).
 */
final class SqlVersion extends MigrationVersion
{
    /** @var list<SqlVersionStep> */
    private readonly array $steps;

    public function __construct(
        private readonly int $rootVersion,
        SqlVersionStep ...$steps,
    ) {
        $this->steps = $steps;
    }

    public function version(): int
    {
        return $this->rootVersion;
    }

    public function compose(VersionBuilder $version): void
    {
        foreach ($this->steps as $step) {
            $version->apply(new SqlVersionRawStep($this->rootVersion, $step));
        }
    }
}
