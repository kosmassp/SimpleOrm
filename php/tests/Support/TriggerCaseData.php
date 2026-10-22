<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Support;

use SimpleOrm\Metadata\TriggerDefinition;
use SimpleOrm\Metadata\TriggerEvent;
use SimpleOrm\Metadata\TriggerTiming;

/**
 * Reads a trigger declaration out of conformance JSON (`trigger-sql/`, the `trigger`/`expectTrigger` migration steps):
 * `{ "timing", "events", "updateOf"?, "sql" }` plus the name its container carries. One reader for every runner
 * (CODING-STANDARD §8).
 */
final class TriggerCaseData
{
    private function __construct()
    {
    }

    /** @param array<string, mixed> $declaration */
    public static function definition(string $name, array $declaration): TriggerDefinition
    {
        /** @var list<string> $events */
        $events = $declaration['events'];
        /** @var list<string> $updateOf */
        $updateOf = $declaration['updateOf'] ?? [];

        return new TriggerDefinition(
            $name,
            TriggerTiming::from((string) $declaration['timing']),
            TriggerEvent::fromTokens($events),
            (string) $declaration['sql'],
            $updateOf,
        );
    }
}
