<?php

declare(strict_types=1);

namespace SimpleOrm\Parameters;

use PDO;
use PDOStatement;

/**
 * Executes a prepared PDO statement with every parameter bound under an
 * explicit PDO type — the one way the library runs a parameterized statement
 * (§7.12 strictness; CODING-STANDARD §10). `PDOStatement::execute($parameters)`
 * binds every value as `PDO::PARAM_STR`, even with emulated prepares off. That
 * is invisible against a column with declared affinity (SQLite coerces the
 * comparison back) but wrong against anything SQLite gives no affinity — a
 * view's aggregate expression, a `pragma_*` table-valued function's columns:
 * a TEXT "0" sorts after every INTEGER, so `count(...) > @zero` never matches.
 * Where ADO.NET infers `DbType` from the CLR value, PHP has to say it:
 * `int` → `PARAM_INT`, `bool` → `PARAM_INT` (SQLite has no boolean), `null` →
 * `PARAM_NULL`, everything else → `PARAM_STR`. PDO has no double type, so a
 * `float` still binds as text; compare floats against declared-affinity
 * columns or cast in SQL. Parameter names are given without their leading `:`
 * (as `BoundSql::$parameters` does).
 */
final class PdoBinder
{
    private function __construct()
    {
    }

    /** @param array<string, mixed> $parameters keys without the leading '@'/':' prefix */
    public static function bindAndExecute(PDOStatement $statement, array $parameters): void
    {
        foreach ($parameters as $name => $value) {
            $statement->bindValue(':' . $name, $value, match (true) {
                $value === null => PDO::PARAM_NULL,
                is_int($value), is_bool($value) => PDO::PARAM_INT,
                default => PDO::PARAM_STR,
            });
        }

        $statement->execute();
    }
}
