<?php

declare(strict_types=1);

namespace SimpleOrm\Migrations;

/**
 * A step's rendered rollback: data hooks around the DDL core (ADR-0016).
 * Reversibility is judged on `$core` alone — hooks without a core (hand-written,
 * or derived at runner time from versioned snapshots, ADR-0018) do not make a
 * step reversible.
 */
final readonly class DownPlan
{
    /**
     * @param list<MigrationStatement> $pre
     * @param list<MigrationStatement> $core
     * @param list<MigrationStatement> $post
     */
    public function __construct(
        public array $pre,
        public array $core,
        public array $post,
    ) {
    }
}
