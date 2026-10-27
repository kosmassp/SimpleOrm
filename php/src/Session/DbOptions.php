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
 * the tracking mode (ADR-0034 add.1 — `Untracked` by default, the Level 0–2 session exactly), the session
 * clock the declared hooks write (Level 3 M3, spec/hooks.md "The session clock"), and the statement-batching option
 * (Level 4, ADR-0044 — `Off` by default, the Level 0–3 behaviour exactly; spec/session.md "Batching"), and the statement log
 * (Level 4 item 4, ADR-0045 — unset by default: nothing is written and nothing is formatted; spec/session.md "Statement log"),
 * and the resilience options (Level 4, ADR-0047 — the command timeout and the open retry, both off by default;
 * spec/session.md "Resilience").
 */
final readonly class DbOptions
{
    /**
     * @param (Closure(): DateTimeImmutable)|null $clock the instant `#[CreatedAt]`/`#[UpdatedAt]` write — UTC, the wall
     *        clock when null; tests and the conformance runners fix it
     * @param BatchingMode $batching `Off` (one message per statement) or `Auto` (the statements of one flush or one
     *        explicit batch go out in as few messages as the plan allows)
     * @param int $maxStatementsPerMessage the most statements one message carries, 100 by default; below 1 is an
     *        `InvalidArgumentException` when the session opens
     * @param (Closure(string): void)|null $log called once per statement sent with its line, synchronously; null (the
     *        default) writes nothing and formats nothing. {@see DbLog::stdout()} is the built-in sink
     * @param bool $logParameterValues whether a line carries the parameter values beside their names — off by default:
     *        values may be personal data
     * @param int|float|null $commandTimeout seconds every statement the session sends may run (ADR-0047, spec/session.md
     *        "Resilience") — applied in whole seconds rounded up, at least 1; null (the default) leaves the driver's own
     *        default untouched; negative refuses at open. See {@see CommandTimeout} for what each PDO driver takes
     * @param int $openRetries how many more attempts the open may make after a failure the dialect calls transient; 0
     *        (the default) is one attempt; negative refuses at open
     * @param int|float $openRetryDelay the fixed wait between two attempts, in seconds — 1 by default, 0 for none; negative
     *        refuses at open
     */
    public function __construct(
        public Dialect $dialect,
        public MappingOptions $mapping = new MappingOptions(),
        public TypeHandlerRegistry $typeHandlers = new TypeHandlerRegistry(),
        public TrackingMode $tracking = TrackingMode::Untracked,
        public ?Closure $clock = null,
        public BatchingMode $batching = BatchingMode::Off,
        public int $maxStatementsPerMessage = 100,
        public ?Closure $log = null,
        public bool $logParameterValues = false,
        public int|float|null $commandTimeout = null,
        public int $openRetries = 0,
        public int|float $openRetryDelay = 1,
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
