<?php

declare(strict_types=1);

namespace SimpleOrm\Dialect;

/** The outcome of {@see SqliteShadow::rebuildSnapshots()}: files written and progress notes. */
final class ShadowResult
{
    /** @var list<string> */
    public array $writtenFiles = [];

    /** @var list<string> */
    public array $notes = [];
}
