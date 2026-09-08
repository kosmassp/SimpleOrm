<?php

declare(strict_types=1);

namespace SimpleOrm\Session;

/**
 * The args value for queries and commands that take no parameters (§6): zero
 * public properties, so `ParameterBinder`'s strictness (`PRM-001`/`PRM-002`) is
 * trivially satisfied. Mirrors dotnet's `EmptyArgs.Value`; a fresh instance per
 * call rather than a cached singleton (CODING-STANDARD §3: no static mutable
 * state) — behaviorally identical, since the value carries no state at all.
 */
final class EmptyArgs
{
    public static function value(): self
    {
        return new self();
    }

    private function __construct()
    {
    }
}
