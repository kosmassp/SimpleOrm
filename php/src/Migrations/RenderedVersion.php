<?php

declare(strict_types=1);

namespace SimpleOrm\Migrations;

/** One version's steps, fully rendered (§7.22) — what `MigrationSet::render()` produces for the runner phase. */
final readonly class RenderedVersion
{
    /** @param list<RenderedStep> $steps */
    public function __construct(
        public int $version,
        public array $steps,
    ) {
    }
}
