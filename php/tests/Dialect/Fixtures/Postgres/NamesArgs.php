<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Dialect\Fixtures\Postgres;

/** A string collection: elements with quotes, commas, braces, and backslashes must survive the array literal. */
final readonly class NamesArgs
{
    /** @param list<string> $names */
    public function __construct(public array $names)
    {
    }
}
