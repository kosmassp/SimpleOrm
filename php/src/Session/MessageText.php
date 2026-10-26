<?php

declare(strict_types=1);

namespace SimpleOrm\Session;

use SimpleOrm\Parameters\SqlPlaceholders;

use function implode;

/**
 * The multi-statement realization of a planned message (ADR-0044, spec/dialects.md "Statement batching"; SQL Server in
 * PHP): the statements joined into **one command text**, each statement's placeholders renamed apart by the session
 * first — `@name` in the *i*-th statement (from 0) becomes `@p<i>_name`, literals and comments untouched — and the
 * values bound under the new names, so the text is sent as one `sp_executesql` and its results read in statement
 * order with `nextRowset()`. Pure text work: it touches no connection.
 *
 * @internal
 */
final class MessageText
{
    private function __construct()
    {
    }

    /**
     * @param list<PreparedWrite> $writes
     * @return array{0: string, 1: array<string, mixed>} the PDO-ready text (`:name` placeholders) and the renamed values
     */
    public static function render(array $writes): array
    {
        $texts = [];
        $parameters = [];
        foreach ($writes as $position => $write) {
            $prefix = "p{$position}_";
            $texts[] = SqlPlaceholders::prefixed($write->sql, $prefix);
            foreach ($write->parameters as $name => $value) {
                $parameters[$prefix . $name] = $value;
            }
        }

        // A newline after each `;`: a statement ending in a line comment must not swallow its neighbour.
        return [SqlPlaceholders::toPdo(implode(";\n", $texts)), $parameters];
    }
}
