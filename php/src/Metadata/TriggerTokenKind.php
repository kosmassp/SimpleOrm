<?php

declare(strict_types=1);

namespace SimpleOrm\Metadata;

/** The three trigger body tokens (spec/metadata-model.md "Triggers"): the row relations and the clock. */
enum TriggerTokenKind: string
{
    case New = 'new';
    case Old = 'old';
    case Now = 'now';
}
