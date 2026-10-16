<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Support;

use Closure;
use DateTimeImmutable;
use DateTimeZone;

/**
 * The session clock every session-opening conformance runner fixes (Level 3 M3, spec/hooks.md "The session
 * clock"): `2026-08-29T00:00:00Z`, so the `#[CreatedAt]`/`#[UpdatedAt]` values the case files expect are
 * deterministic. One definition for crud, tracking, flush, hook, and merge runners (CODING-STANDARD §8).
 */
final class ConformanceClock
{
    public const string INSTANT = '2026-08-29T00:00:00.0000000Z';

    /** @return Closure(): DateTimeImmutable */
    public static function fixed(): Closure
    {
        return static fn (): DateTimeImmutable => new DateTimeImmutable(self::INSTANT, new DateTimeZone('UTC'));
    }
}
