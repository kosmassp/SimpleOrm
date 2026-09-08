<?php

declare(strict_types=1);

namespace SimpleOrm\Migrations;

/** Collects a version's composing steps in the explicit order `compose()` applies them (§7.22) — each step's own action ordering (rename → add → remove → raw SQL) is `TableActions`'/`ViewActions`' concern, not this class's. */
final class VersionBuilder
{
    /** @var list<MigrationStep> */
    private array $steps = [];

    /** @param class-string<MigrationStep>|MigrationStep $step a step class (instantiated here) or an already-built instance */
    public function apply(string|MigrationStep $step): self
    {
        $this->steps[] = is_string($step) ? new $step() : $step;

        return $this;
    }

    /** @return list<MigrationStep> */
    public function steps(): array
    {
        return $this->steps;
    }
}
