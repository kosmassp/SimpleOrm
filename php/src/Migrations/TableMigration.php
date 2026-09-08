<?php

declare(strict_types=1);

namespace SimpleOrm\Migrations;

use Closure;
use SimpleOrm\Dialect\Dialect;
use SimpleOrm\Errors\SimpleOrmException;
use SimpleOrm\Metadata\EntityMapLoader;
use SimpleOrm\Metadata\RelationKind;

/**
 * A table's change: actions execute rename → add → remove → raw SQL, regardless
 * of call order (§7.22, ADR-0013). `entityClass()` replaces C#'s
 * `TableMigration<TEntity>` generic parameter — PHP has no generics.
 */
abstract class TableMigration extends MigrationStep
{
    /** @return class-string the mapped entity this step changes */
    abstract public function entityClass(): string;

    abstract public function action(TableActions $actions): void;

    /**
     * The rollback DDL — normally NOT hand-written: it derives from the
     * versioned schema snapshots at `migrate down` time (ADR-0018); overriding
     * is the manual escape hatch. A step whose down core renders nothing
     * refuses `migrate down` (`MIG-020`) unless the snapshots can derive it.
     */
    public function down(TableActions $actions): void
    {
    }

    /** Data work before the rollback DDL (e.g. stash values a destructive revert would lose). */
    public function preDown(MigrationSql $sql): void
    {
    }

    /** Data work after the rollback DDL (e.g. restore or transform). */
    public function postDown(MigrationSql $sql): void
    {
    }

    public function objectName(EntityMapLoader $maps): string
    {
        /** @var string $relationName table-kind maps always carry a relation name */
        $relationName = $maps->load($this->entityClass())->relationName;

        return $relationName;
    }

    public function renderUp(EntityMapLoader $maps, Dialect $dialect): array
    {
        return $this->compose($maps, $dialect, $this->action(...))->build();
    }

    public function renderDown(EntityMapLoader $maps, Dialect $dialect): DownPlan
    {
        $pre = new MigrationSql();
        $this->preDown($pre);
        $post = new MigrationSql();
        $this->postDown($post);

        return new DownPlan(
            self::wrap($pre->statements(), 'pre-down'),
            $this->compose($maps, $dialect, $this->down(...))->build(),
            self::wrap($post->statements(), 'post-down'),
        );
    }

    public function upRenames(EntityMapLoader $maps, Dialect $dialect): array
    {
        return $this->compose($maps, $dialect, $this->action(...))->columnRenames();
    }

    private function compose(EntityMapLoader $maps, Dialect $dialect, Closure $compose): TableActions
    {
        $map = $maps->load($this->entityClass());
        if ($map->kind !== RelationKind::Table) {
            throw new SimpleOrmException(
                'DDL-001',
                $this->entityClass(),
                "is {$map->kind->name}-backed; TableMigration applies to tables",
            );
        }

        $actions = new TableActions($map, $dialect);
        $compose($actions);

        return $actions;
    }

    /**
     * @param list<string> $statements
     * @return list<MigrationStatement>
     */
    private static function wrap(array $statements, string $origin): array
    {
        return array_map(static fn (string $sql): MigrationStatement => new MigrationStatement($sql, $origin), $statements);
    }
}
