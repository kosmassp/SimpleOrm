<?php

declare(strict_types=1);

namespace SimpleOrm\Parameters;

/**
 * The result of binding an args object against SQL (§7.12/§7.13): PDO-ready SQL
 * (`@name` rewritten to `:name`) and the ordered value map keyed by name
 * **without** its prefix — ready for `PdoBinder::bindAndExecute()` (never a bare
 * `PDOStatement::execute($parameters)`, which binds every value as text).
 */
final readonly class BoundSql
{
    /** @param array<string, mixed> $parameters keys without the leading '@'/':' prefix */
    public function __construct(
        public string $sql,
        public array $parameters,
    ) {
    }
}
