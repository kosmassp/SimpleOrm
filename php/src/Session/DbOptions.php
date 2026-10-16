<?php

declare(strict_types=1);

namespace SimpleOrm\Session;

use Closure;
use DateTimeImmutable;
use DateTimeZone;
use SimpleOrm\Dialect\Dialect;
use SimpleOrm\Errors\SimpleOrmException;
use SimpleOrm\Mapping\TypeHandlerRegistry;
use SimpleOrm\Metadata\MappingOptions;

/**
 * Options fixed at {@see Db::open}: the dialect, metadata configuration, the type-handler registry (§7.17),
 * the tracking mode (ADR-0034 add.1 — `Untracked` by default, the Level 0–2 session exactly), and the session
 * clock the declared hooks write (Level 3 M3, spec/hooks.md "The session clock").
 */
final readonly class DbOptions
{
    /**
     * @param (Closure(): DateTimeImmutable)|null $clock the instant `#[CreatedAt]`/`#[UpdatedAt]` write — UTC, the wall
     *        clock when null; tests and the conformance runners fix it
     */
    public function __construct(
        public Dialect $dialect,
        public MappingOptions $mapping = new MappingOptions(),
        public TypeHandlerRegistry $typeHandlers = new TypeHandlerRegistry(),
        public TrackingMode $tracking = TrackingMode::Untracked,
        public ?Closure $clock = null,
    ) {
    }

    /**
     * The clock's current instant — the one place the clock is read and held to the §7.9 UTC rule: an instant
     * in any other zone is `VAL-020` (CODING-STANDARD §10: PHP names the zone, so UTC is the zone named
     * `UTC`, `Z`, or `+00:00`).
     */
    public function now(): DateTimeImmutable
    {
        // "UTC" is the language's notion of it (spec/hooks.md Clarifications): a zone *named* UTC, Z, or +00:00 (an offset of
        // 0 is not enough: Europe/London is +00:00 only in winter). Checked when a hook reads the clock, never at open.
        $instant = $this->clock !== null ? ($this->clock)() : new DateTimeImmutable('now', new DateTimeZone('UTC'));
        if (!in_array($instant->getTimezone()->getName(), ['UTC', 'Z', '+00:00'], true)) {
            throw new SimpleOrmException(
                'VAL-020',
                'DbOptions.clock',
                "the clock returned an instant in '{$instant->getTimezone()->getName()}'; the session clock must return UTC (§7.9)",
            );
        }

        return $instant;
    }
}
