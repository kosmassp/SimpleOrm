<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Query;

use SimpleOrm\Query\AnsiSelectRenderer;
use SimpleOrm\Query\SelectAst;
use SimpleOrm\Tests\Support\DelegatingDialect;

/**
 * Delegates every {@see \SimpleOrm\Dialect\Dialect} member to a real dialect except
 * `supportsRowValueIn`, which it forces to `false` — a test-local stand-in for
 * a dialect like SQL Server (ADR-0024), so {@see AnsiSelectRendererLevel2Test}
 * can exercise the correlated-EXISTS rewrite the pinned SQLite conformance
 * cases never trigger, without duplicating the whole seam (CODING-STANDARD §8:
 * the delegation lives once in {@see DelegatingDialect}).
 */
final class RowValueInDisabledDialect extends DelegatingDialect
{
    public function selectSql(SelectAst $select, callable $bindParameter): string
    {
        // Not delegated: the reference rendering consults *this* decorator for
        // `supportsRowValueIn()`, so it must render through `$this`, not `$inner`.
        return AnsiSelectRenderer::selectSql($this, $select, $bindParameter);
    }

    public function supportsRowValueIn(): bool
    {
        return false;
    }
}
