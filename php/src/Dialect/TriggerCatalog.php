<?php

declare(strict_types=1);

namespace SimpleOrm\Dialect;

use PDO;
use SimpleOrm\Parameters\PdoBinder;
use SimpleOrm\Parameters\SqlPlaceholders;
use SimpleOrm\Session\StatementLog;

/**
 * Reads the stored statements of a trigger through a dialect's `triggerDefinitionSql()` (ADR-0042, ADR-0043): the one
 * query-and-fetch the trigger parsers of the shadow hosts share.
 */
final class TriggerCatalog
{
    private function __construct()
    {
    }

    /**
     * The stored create statements of a trigger name — one row per statement the dialect created for it, in the order
     * the catalog returns them.
     *
     * @param StatementLog|null $log the statement log (ADR-0045): the read is written under the name the log carries
     * @return list<string>
     */
    public static function definitions(PDO $connection, Dialect $dialect, string $name, ?StatementLog $log = null): array
    {
        $sql = $dialect->triggerDefinitionSql();
        $statement = $connection->prepare(SqlPlaceholders::toPdo($sql));
        ($log ?? StatementLog::off())->write('shadow', $sql, ['relation' => $name]);
        PdoBinder::bindAndExecute($statement, ['relation' => $name]);

        return array_map(static fn (mixed $definition): string => (string) $definition, $statement->fetchAll(PDO::FETCH_COLUMN));
    }
}
