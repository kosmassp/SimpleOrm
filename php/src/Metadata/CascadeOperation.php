<?php

declare(strict_types=1);

namespace SimpleOrm\Metadata;

/**
 * An operation a navigation may cascade (Level 3 M2, ADR-0034 §7c: "declared, never inferred").
 * Declaration order is the export order — `persist` before `remove` — and the backing value is
 * the export token (`"cascade": ["persist", "remove"]`, spec/metadata-model.md). A pure set of
 * names, not flags: PHP enums are not bit sets, so a navigation carries a list of them
 * (CODING-STANDARD §10).
 */
enum CascadeOperation: string
{
    case Persist = 'persist';
    case Remove = 'remove';
}
