<?php

declare(strict_types=1);

namespace SimpleOrm\Migrations;

/** One version's steps, fully rendered (§7.22) — what `MigrationSet::render()` produces for the runner phase. */
final readonly class RenderedVersion
{
    /**
     * @param list<RenderedStep> $steps
     * @param bool $draft the root carries the draft marker (Level 3 M5): pending, it is never applied or baselined (`MIG-013`)
     */
    public function __construct(
        public int $version,
        public array $steps,
        public bool $draft = false,
    ) {
    }
}
