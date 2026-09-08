<?php

declare(strict_types=1);

namespace SimpleOrm\Mapping;

/**
 * The extension point beyond the fixed conversion table (§7.9): one handler per
 * PHP type, registered on `DbOptions`. Handlers win over the fixed table in both
 * directions. No reflection-based guessing anywhere else.
 */
interface TypeHandler
{
    /** @return class-string the PHP type this handler owns */
    public function type(): string;

    /** PHP value → database value (a scalar PDO can bind). */
    public function format(mixed $value): mixed;

    /** Database value → PHP value. */
    public function parse(mixed $value): mixed;
}
