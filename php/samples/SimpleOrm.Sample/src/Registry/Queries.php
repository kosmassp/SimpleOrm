<?php

declare(strict_types=1);

namespace SimpleOrm\Sample\Registry;

use SimpleOrm\Session\EmptyArgs;
use SimpleOrm\Session\Query;

/**
 * The query registry (§6, ADR-0009): SQL declared once, inline, next to its args
 * and result types. SchemaGuard enumerates every public static method returning
 * a `Query` and validates it without running it (CODING-STANDARD §10: the PHP
 * shape of a registry field).
 */
final class Queries
{
    private function __construct()
    {
    }

    /**
     * The §7.10 nesting pattern: children arrive as one JSON column produced by
     * `json_group_array(json_object(...))`, deserialized by the registered
     * `JsonTypeHandler(DetailLine::class, list: true)`. One round trip, no
     * in-memory reshaping.
     */
    public static function transactionsWithDetails(): Query
    {
        return Query::inline(TransactionsByUserArgs::class, TransactionWithDetails::class, <<<'SQL'
            -- notnull: details
            select t.id,
                   t.amount,
                   (select json_group_array(json_object(
                            'description', d.description,
                            'quantity', d.quantity,
                            'unit_price', d.unit_price))
                    from transaction_details d
                    where d.transaction_id = t.id) as details
            from transactions t
            where t.user_id = @userId
            order by t.id
            SQL);
    }

    /** An aggregate over the many-to-many link; `-- notnull:` lifts the expression column (§7.19). */
    public static function roleUsage(): Query
    {
        return Query::inline(EmptyArgs::class, RoleUsage::class, <<<'SQL'
            -- notnull: user_count
            select r.role_name,
                   count(ur.user_id) as user_count
            from roles r
            left join user_roles ur on ur.role_id = r.id
            group by r.id, r.role_name
            order by r.role_name
            SQL);
    }
}
