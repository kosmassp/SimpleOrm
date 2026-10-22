<?php

declare(strict_types=1);

namespace SimpleOrm\Metadata;

/**
 * When a declared trigger fires relative to the write (Level 4, ADR-0042, spec/metadata-model.md "Triggers"). The
 * backing value is the export token (`"timing": "instead_of"`); {@see self::keyword()} is the SQL spelling the
 * dialects share.
 */
enum TriggerTiming: string
{
    case Before = 'before';
    case After = 'after';
    case InsteadOf = 'instead_of';

    /** The SQL keyword(s): `before`, `after`, `instead of`. */
    public function keyword(): string
    {
        return $this === self::InsteadOf ? 'instead of' : $this->value;
    }
}
