<?php

declare(strict_types=1);

namespace SimpleOrm\Migrations;

use SimpleOrm\Dialect\Dialect;
use SimpleOrm\Metadata\EntityMap;

/** View actions (§7.22); executed in declaration order. */
final class ViewActions
{
    /** @var list<MigrationAction> */
    private array $actions = [];

    public function __construct(
        private readonly EntityMap $map,
        private readonly Dialect $dialect,
    ) {
    }

    private function view(): string
    {
        /** @var string $relationName view-kind maps always carry a relation name */
        $relationName = $this->map->relationName;

        return $relationName;
    }

    public function createView(): MigrationAction
    {
        $statements = [$this->dialect->createViewSql($this->map), ...$this->dialect->createIndexSql($this->map)];

        return $this->track(new MigrationAction('create ' . $this->view(), $statements));
    }

    public function dropView(): MigrationAction
    {
        return $this->track(new MigrationAction('drop ' . $this->view(), ['drop view ' . $this->view()]));
    }

    /** Drops (if present) and re-creates from the current defining SQL. */
    public function recreateView(): MigrationAction
    {
        $statements = [
            'drop view if exists ' . $this->view(),
            $this->dialect->createViewSql($this->map),
            ...$this->dialect->createIndexSql($this->map),
        ];

        return $this->track(new MigrationAction('recreate ' . $this->view(), $statements));
    }

    public function sql(string $sql): MigrationAction
    {
        return $this->track(new MigrationAction('sql ' . $this->view(), [$sql]));
    }

    /**
     * Precondition: the view's live definition must match `$ddl`
     * (whitespace-normalized) when this step applies. On mismatch — the view
     * was adjusted outside the code — the run refuses with `MIG-012` unless
     * forced. Generated change steps carry this automatically from the
     * previous snapshot; hand-written steps may declare it too.
     */
    public function expectDefinition(string $ddl): MigrationAction
    {
        return $this->track(new MigrationAction(
            'expect ' . $this->view(),
            [],
            new MigrationStatement(SchemaSnapshot::normalizeDdl($ddl), 'expect ' . $this->view(), $this->view()),
        ));
    }

    /** @return list<MigrationStatement> */
    public function build(): array
    {
        $statements = [];
        foreach ($this->actions as $action) {
            foreach ($action->render() as $statement) {
                $statements[] = $statement;
            }
        }

        return $statements;
    }

    private function track(MigrationAction $action): MigrationAction
    {
        $this->actions[] = $action;

        return $action;
    }
}
