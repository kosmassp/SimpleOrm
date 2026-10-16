<?php

declare(strict_types=1);

namespace SimpleOrm\Session;

use SimpleOrm\Dialect\Dialect;
use SimpleOrm\Mapping\TypeHandlerRegistry;
use SimpleOrm\Metadata\MappingOptions;

/**
 * Options fixed at {@see Db::open}: the dialect, metadata configuration, the type-handler registry (§7.17),
 * and the tracking mode (ADR-0034 add.1 — `Untracked` by default, the Level 0–2 session exactly).
 */
final readonly class DbOptions
{
    public function __construct(
        public Dialect $dialect,
        public MappingOptions $mapping = new MappingOptions(),
        public TypeHandlerRegistry $typeHandlers = new TypeHandlerRegistry(),
        public TrackingMode $tracking = TrackingMode::Untracked,
    ) {
    }
}
