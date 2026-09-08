<?php

declare(strict_types=1);

namespace SimpleOrm\Migrations;

/**
 * One action with its optional per-action data hooks (§7.22): `pre()` runs
 * immediately before it, `post()` immediately after. `$guard` is set only by
 * `ViewActions::expectDefinition()` — a precondition rendered ahead of the
 * statements rather than SQL to execute (`MIG-012`).
 */
final class MigrationAction
{
    /** @var list<string> */
    private array $preSql = [];

    /** @var list<string> */
    private array $postSql = [];

    /** @param list<string> $statements */
    public function __construct(
        private readonly string $origin,
        private readonly array $statements,
        private readonly ?MigrationStatement $guard = null,
    ) {
    }

    /** Data step executed immediately before this action (e.g. preserve values). Optional; chainable. */
    public function pre(string $sql): self
    {
        $this->preSql[] = $sql;

        return $this;
    }

    /** Data step executed immediately after this action (e.g. backfill). Optional; chainable. */
    public function post(string $sql): self
    {
        $this->postSql[] = $sql;

        return $this;
    }

    /** @return list<MigrationStatement> */
    public function render(): array
    {
        $statements = [];
        if ($this->guard !== null) {
            $statements[] = $this->guard;
        }

        foreach ($this->preSql as $sql) {
            $statements[] = new MigrationStatement($sql, $this->origin . ' pre');
        }

        foreach ($this->statements as $sql) {
            $statements[] = new MigrationStatement($sql, $this->origin);
        }

        foreach ($this->postSql as $sql) {
            $statements[] = new MigrationStatement($sql, $this->origin . ' post');
        }

        return $statements;
    }
}
