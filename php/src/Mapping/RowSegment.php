<?php

declare(strict_types=1);

namespace SimpleOrm\Mapping;

/**
 * A view over one alias's slice of a joined row (§7.11, ADR-0022 add.1): the
 * join renderer re-aliases every root and projected-join column
 * `<alias>_<column>` (spec/query-ast.md "Joins") so one fetched associative
 * row carries several entities' worth of columns side by side. This strips
 * one alias's prefix back to plain column names, so the existing entity plan
 * ({@see ResultMapper::createPlan}) maps the segment exactly as it would map
 * a plain, unjoined row — one mapping pipeline, never a second mapper for the
 * joined case.
 */
final class RowSegment
{
    /**
     * @param array<string, mixed> $row the full fetched row, keyed by `<prefix><column>`
     * @param list<string> $columnNames the alias's mapped column names, unprefixed, in metadata order
     * @return array<string, mixed> keyed by plain column name — {@see ResultMapper::createPlan}'s row shape
     */
    public static function extract(array $row, string $prefix, array $columnNames): array
    {
        $segment = [];
        foreach ($columnNames as $columnName) {
            $segment[$columnName] = $row[$prefix . $columnName] ?? null;
        }

        return $segment;
    }

    /**
     * A LEFT JOIN miss reads as an all-NULL segment. Per spec/loading.md's
     * "Join mode aliases" clarification, only the **key** columns decide this —
     * a nullable non-key column of a real matched row must not be mistaken for
     * a miss.
     *
     * @param array<string, mixed> $segment
     * @param list<string> $keyColumnNames
     */
    public static function isAbsent(array $segment, array $keyColumnNames): bool
    {
        foreach ($keyColumnNames as $columnName) {
            if (($segment[$columnName] ?? null) !== null) {
                return false;
            }
        }

        return true;
    }
}
