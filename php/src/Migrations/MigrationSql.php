<?php

declare(strict_types=1);

namespace SimpleOrm\Migrations;

/**
 * Collects plain data statements for the step-level `preDown`/`postDown` hooks
 * (ADR-0016): data work a derived or hand-written rollback core cannot express
 * (stash values before a destructive revert, restore or transform after).
 */
final class MigrationSql
{
    /** @var list<string> */
    private array $statements = [];

    public function sql(string $sql): self
    {
        $this->statements[] = $sql;

        return $this;
    }

    /** @return list<string> */
    public function statements(): array
    {
        return $this->statements;
    }
}
