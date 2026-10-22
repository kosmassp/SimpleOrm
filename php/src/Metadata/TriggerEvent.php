<?php

declare(strict_types=1);

namespace SimpleOrm\Metadata;

/**
 * A write a declared trigger fires on (Level 4, ADR-0042). A declaration carries a **set** of events: PHP enums are
 * not bit sets, so the set is a list of cases normalized by {@see self::ordered()} to the fixed order `insert`,
 * `update`, `delete` without repeats — the order the export, the snapshot, and every rendering use
 * (CODING-STANDARD §10, the same shape as `CascadeOperation`).
 */
enum TriggerEvent: string
{
    case Insert = 'insert';
    case Update = 'update';
    case Delete = 'delete';

    /**
     * The given events in the fixed order, repeats dropped.
     *
     * @param list<TriggerEvent> $events
     * @return list<TriggerEvent>
     */
    public static function ordered(array $events): array
    {
        return array_values(array_filter(
            self::cases(),
            static fn (self $case): bool => in_array($case, $events, true),
        ));
    }

    /**
     * Parses export tokens (`"update"`), in the fixed order; an unknown token is dropped by the caller's validation.
     *
     * @param list<string> $tokens
     * @return list<TriggerEvent>
     */
    public static function fromTokens(array $tokens): array
    {
        $events = [];
        foreach ($tokens as $token) {
            $case = self::tryFrom($token);
            if ($case !== null) {
                $events[] = $case;
            }
        }

        return self::ordered($events);
    }
}
