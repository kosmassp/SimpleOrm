<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Support;

use PDO;
use SimpleOrm\Dialect\Dialect;
use SimpleOrm\Metadata\EntityMap;
use SimpleOrm\Metadata\RelationKind;
use SimpleOrm\Migrations\SchemaSnapshot;
use SimpleOrm\Parameters\PdoBinder;
use SimpleOrm\Parameters\SqlPlaceholders;

/**
 * The symmetric contract of the trigger live legs (ADR-0042, spec/dialects.md "Introspection result shapes"): the rows of
 * `Dialect::triggerDefinitionSql()` for a created trigger, normalized, are exactly the dialect's rendering of its
 * declaration, normalized. One reader of both sides for every live leg (CODING-STANDARD §8).
 */
final class TriggerDefinitions
{
    private function __construct()
    {
    }

    /** @return list<string> the live definitions of the trigger, normalized and sorted */
    public static function live(PDO $connection, Dialect $dialect, string $name): array
    {
        $statement = $connection->prepare(SqlPlaceholders::toPdo($dialect->triggerDefinitionSql()));
        PdoBinder::bindAndExecute($statement, ['relation' => $name]);

        return self::normalized(array_map(static fn (mixed $definition): string => (string) $definition, $statement->fetchAll(PDO::FETCH_COLUMN)));
    }

    /** @return list<string> the dialect's create statements for the entity's `$index`th linked trigger, normalized and sorted */
    public static function rendered(Dialect $dialect, EntityMap $map, int $index = 0): array
    {
        return self::normalized($dialect->createTriggerSql(
            (string) $map->relationName,
            $map->kind === RelationKind::View,
            $map->triggers[$index]->definition,
        ));
    }

    /**
     * @param list<string> $statements
     * @return list<string>
     */
    private static function normalized(array $statements): array
    {
        $normalized = array_map(SchemaSnapshot::normalizeTriggerDdl(...), $statements);
        sort($normalized, SORT_STRING);

        return $normalized;
    }
}
