<?php

declare(strict_types=1);

namespace SimpleOrm\Migrations;

use SimpleOrm\Dialect\Dialect;
use SimpleOrm\Metadata\EntityMap;

/**
 * Table actions (§7.22, ADR-0013). Fixed execution order — renames, then adds,
 * then removes, then raw SQL — with declaration order inside each group,
 * regardless of the order the author called them in; each action carries
 * optional pre/post data hooks. Column specs are literal (frozen);
 * metadata-rendered DDL is legal only where the version checksum freezes it
 * (the object's create).
 */
final class TableActions
{
    /** @var list<MigrationAction> */
    private array $renames = [];

    /** @var list<MigrationAction> */
    private array $adds = [];

    /** @var list<MigrationAction> */
    private array $removes = [];

    /** @var list<MigrationAction> */
    private array $custom = [];

    /** @var list<ColumnRename> */
    private array $columnRenames = [];

    public function __construct(
        private readonly EntityMap $map,
        private readonly Dialect $dialect,
    ) {
    }

    private function table(): string
    {
        /** @var string $relationName table-kind maps always carry a relation name */
        $relationName = $this->map->relationName;

        return $relationName;
    }

    /** The object's initial creation, rendered from metadata (table + declared indexes). */
    public function createTable(): MigrationAction
    {
        $statements = [$this->dialect->createTableSql($this->map), ...$this->dialect->createIndexSql($this->map)];

        return $this->track($this->adds, new MigrationAction('create ' . $this->table(), $statements));
    }

    public function dropTable(): MigrationAction
    {
        return $this->track(
            $this->removes,
            new MigrationAction('drop ' . $this->table(), [$this->dialect->dropTableSql($this->table())]),
        );
    }

    public function renameTable(string $fromName): MigrationAction
    {
        return $this->track($this->renames, new MigrationAction(
            "rename table {$fromName}",
            [$this->dialect->renameTableSql($fromName, $this->table())],
        ));
    }

    /** Column renames, kept structurally too — the derived rollback inverts them (ADR-0018). */
    public function renameColumn(string $fromName, string $toName): MigrationAction
    {
        $this->columnRenames[] = new ColumnRename($fromName, $toName);

        return $this->track($this->renames, new MigrationAction(
            "rename {$this->table()}.{$fromName}",
            [$this->dialect->renameColumnSql($this->table(), $fromName, $toName)],
        ));
    }

    /** Literal column spec; a NOT NULL addition to a populated table needs `$defaultSql`. */
    public function addColumn(string $name, string $type, bool $nullable = true, ?string $defaultSql = null): MigrationAction
    {
        return $this->track($this->adds, new MigrationAction(
            "add {$this->table()}.{$name}",
            [$this->dialect->addColumnSql($this->table(), $name, $type, $nullable, $defaultSql)],
        ));
    }

    public function removeColumn(string $name): MigrationAction
    {
        return $this->track($this->removes, new MigrationAction(
            "remove {$this->table()}.{$name}",
            [$this->dialect->dropColumnSql($this->table(), $name)],
        ));
    }

    public function createIndexes(): MigrationAction
    {
        return $this->track(
            $this->adds,
            new MigrationAction('indexes ' . $this->table(), $this->dialect->createIndexSql($this->map)),
        );
    }

    public function dropIndex(string $name): MigrationAction
    {
        return $this->track($this->removes, new MigrationAction('drop index ' . $name, ['drop index ' . $name]));
    }

    /** Raw SQL escape hatch; runs after the ordered groups. */
    public function sql(string $sql): MigrationAction
    {
        return $this->track($this->custom, new MigrationAction('sql ' . $this->table(), [$sql]));
    }

    /** @return list<ColumnRename> */
    public function columnRenames(): array
    {
        return $this->columnRenames;
    }

    /** @return list<MigrationStatement> */
    public function build(): array
    {
        $statements = [];
        foreach ([...$this->renames, ...$this->adds, ...$this->removes, ...$this->custom] as $action) {
            foreach ($action->render() as $statement) {
                $statements[] = $statement;
            }
        }

        return $statements;
    }

    /** @param list<MigrationAction> $group */
    private function track(array &$group, MigrationAction $action): MigrationAction
    {
        $group[] = $action;

        return $action;
    }
}
