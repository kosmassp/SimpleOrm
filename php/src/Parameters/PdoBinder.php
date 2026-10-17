<?php

declare(strict_types=1);

namespace SimpleOrm\Parameters;

use DateTimeImmutable;
use PDO;
use PDOStatement;
use SimpleOrm\Types\Iso8601;

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
 * columns or cast in SQL. An `array` value (only a dialect with
 * `supportsArrayParameters()` leaves one, ADR-0025) binds as a PostgreSQL array
 * literal string ({@see PostgresArrayLiteral}). A {@see BinaryParameter} — a value
 * whose property's metadata token is `bytes` — binds as `PARAM_LOB` (ADR-0040
 * add.1): PHP strings carry no text/bytes distinction of their own. Parameter names are given without their leading `:`
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
            if ($value instanceof BinaryParameter) {
                // A `bytes`-typed property's value (ADR-0040 add.1): a binary parameter on every dialect.
                $statement->bindValue(':' . $name, $value->bytes, PDO::PARAM_LOB);
                continue;
            }

            if ($value instanceof DateTimeImmutable) {
                // A dialect with BindsTemporalsNatively (ADR-0025) leaves the UTC instant a DateTimeImmutable, and
                // PDO cannot bind one: the ISO-8601 string under an untyped parameter is its equivalent, the server
                // infers timestamptz from the comparison (CODING-STANDARD §10).
                $value = Iso8601::format($value);
            }

            if (is_array($value)) {
                // A collection kept whole by a dialect with array parameters (ADR-0025): one PostgreSQL
                // array literal under an untyped string parameter; the server infers the array type.
                $value = PostgresArrayLiteral::encode($value);
            }

            $statement->bindValue(':' . $name, $value, match (true) {
                $value === null => PDO::PARAM_NULL,
                is_int($value), is_bool($value) => PDO::PARAM_INT,
                default => PDO::PARAM_STR,
            });
        }

        $statement->execute();
    }
}
