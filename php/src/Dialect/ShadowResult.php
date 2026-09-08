<?php

declare(strict_types=1);

namespace SimpleOrm\Dialect;

/** The outcome of {@see SqliteShadow::rebuildSnapshots()} (ADR-0017): files written and progress notes, as mutable lists the replay builds up incrementally. */
final class ShadowResult
{
    /** @var list<string> */
    public array $writtenFiles = [];

    /** @var list<string> */
    public array $notes = [];
}
