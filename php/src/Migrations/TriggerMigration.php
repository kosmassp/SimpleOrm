<?php

declare(strict_types=1);

namespace SimpleOrm\Migrations;

use Closure;
use SimpleOrm\Dialect\Dialect;
use SimpleOrm\Errors\SimpleOrmException;
use SimpleOrm\Metadata\EntityMapLoader;
use SimpleOrm\Metadata\RelationKind;
use SimpleOrm\Metadata\TriggerRules;

/**
 * A trigger's change (Level 4, ADR-0042; the C# `TriggerMigration<TEntity, TTrigger>`): the step names the entity the
 * trigger is on and the trigger class, and its `schema_version` object is the trigger's declared name. Actions execute
 * in declaration order ({@see TriggerActions}); the rollback derives from the trigger snapshots like every other kind
 * (ADR-0018), `down()` being the manual override. `entityClass()`/`triggerClass()` replace the generic parameters — PHP
 * has no generics. The object's folder is the trigger class: `Trigger/<Class>/V000N_<Desc>.php`.
 */
abstract class TriggerMigration extends MigrationStep
{
    /** @return class-string the entity (table, or view for INSTEAD OF) the trigger is on */
    abstract public function entityClass(): string;

    /** @return class-string the trigger declaration class */
    abstract public function triggerClass(): string;

    abstract public function action(TriggerActions $actions): void;

    /** The manual rollback override (always wins over the derived one). */
    public function down(TriggerActions $actions): void
    {
    }

    /** Data work before the rollback DDL. */
    public function preDown(MigrationSql $sql): void
    {
    }

    /** Data work after the rollback DDL. */
    public function postDown(MigrationSql $sql): void
    {
    }

    /** The trigger's declared name — readable from the class itself, so a removal step works after the entity unlinked it. */
    public function objectName(EntityMapLoader $maps): string
    {
        return TriggerRules::declaredName($this->triggerClass());
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

    /**
     * The composed up actions — what the shadow reads (`declared()`/`leavesDeclaration()`) to snapshot the trigger from
     * the step rather than from a catalog that may not hand it back as declared (ADR-0043).
     */
    public function upActions(EntityMapLoader $maps, Dialect $dialect): TriggerActions
    {
        return $this->compose($maps, $dialect, $this->action(...));
    }

    private function compose(EntityMapLoader $maps, Dialect $dialect, Closure $compose): TriggerActions
    {
        $map = $maps->load($this->entityClass());
        if ($map->kind !== RelationKind::Table && $map->kind !== RelationKind::View) {
            throw new SimpleOrmException(
                'DDL-001',
                $this->entityClass(),
                "is {$map->kind->name}-backed; TriggerMigration applies to tables (and views, for INSTEAD OF)",
            );
        }

        $linked = null;
        foreach ($map->triggers as $trigger) {
            if (strcasecmp(ltrim($trigger->triggerClass, '\\'), ltrim($this->triggerClass(), '\\')) === 0) {
                $linked = $trigger;

                break;
            }
        }

        $actions = new TriggerActions(
            (string) $map->relationName,
            $map->kind === RelationKind::View,
            TriggerRules::declaredName($this->triggerClass()),
            $linked,
            $dialect,
            $map->entityName(),
        );
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
