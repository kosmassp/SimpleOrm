<?php

declare(strict_types=1);

namespace SimpleOrm\Migrations;

use Closure;
use SimpleOrm\Dialect\Dialect;
use SimpleOrm\Errors\SimpleOrmException;
use SimpleOrm\Metadata\EntityMapLoader;
use SimpleOrm\Metadata\RelationKind;

/**
 * A view's (or materialized view's) change: actions execute in declaration
 * order (§7.22). `entityClass()` replaces C#'s `ViewMigration<TEntity>` generic
 * parameter — PHP has no generics.
 */
abstract class ViewMigration extends MigrationStep
{
    /** @return class-string the mapped entity this step changes */
    abstract public function entityClass(): string;

    abstract public function action(ViewActions $actions): void;

    public function down(ViewActions $actions): void
    {
    }

    public function preDown(MigrationSql $sql): void
    {
    }

    public function postDown(MigrationSql $sql): void
    {
    }

    public function objectName(EntityMapLoader $maps): string
    {
        /** @var string $relationName view-kind maps always carry a relation name */
        $relationName = $maps->load($this->entityClass())->relationName;

        return $relationName;
    }

    public function renderUp(EntityMapLoader $maps, Dialect $dialect): array
    {
        return $this->render($maps, $dialect, $this->action(...));
    }

    public function renderDown(EntityMapLoader $maps, Dialect $dialect): DownPlan
    {
        $pre = new MigrationSql();
        $this->preDown($pre);
        $post = new MigrationSql();
        $this->postDown($post);

        return new DownPlan(
            self::wrap($pre->statements(), 'pre-down'),
            $this->render($maps, $dialect, $this->down(...)),
            self::wrap($post->statements(), 'post-down'),
        );
    }

    /** @return list<MigrationStatement> */
    private function render(EntityMapLoader $maps, Dialect $dialect, Closure $compose): array
    {
        $map = $maps->load($this->entityClass());
        if ($map->kind !== RelationKind::View && $map->kind !== RelationKind::MaterializedView) {
            throw new SimpleOrmException(
                'DDL-001',
                $this->entityClass(),
                "is {$map->kind->name}-backed; ViewMigration applies to views",
            );
        }

        if ($map->kind === RelationKind::MaterializedView && !$dialect->supportsMaterializedViews()) {
            throw new SimpleOrmException('DDL-002', $this->entityClass(), 'the dialect has no materialized views');
        }

        $actions = new ViewActions($map, $dialect);
        $compose($actions);

        return $actions->build();
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
