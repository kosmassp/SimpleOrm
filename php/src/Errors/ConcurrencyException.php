<?php

declare(strict_types=1);

namespace SimpleOrm\Errors;

/**
 * Optimistic-concurrency conflict (§7.16, `CRUD-010`): an update or delete with a
 * version column affected zero rows — the caller's version is stale or the row
 * is gone.
 */
final class ConcurrencyException extends SimpleOrmException
{
    public function __construct(string $target, string $message)
    {
        parent::__construct('CRUD-010', $target, $message);
    }
}
