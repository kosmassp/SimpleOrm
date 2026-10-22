<?php

declare(strict_types=1);

namespace SimpleOrm\Migrations;

use SimpleOrm\Dialect\Dialect;
use SimpleOrm\Errors\SimpleOrmException;
use SimpleOrm\Metadata\EntityTrigger;
use SimpleOrm\Metadata\TriggerDefinition;
use SimpleOrm\Metadata\TriggerEvent;
use SimpleOrm\Metadata\TriggerTiming;

/**
 * Trigger actions (Level 4, ADR-0042, spec/migrations.md "Steps and actions"); executed in declaration order, each one
 * rendered through the dialect **from a declaration** — the entity's (`createTrigger()`, the initial create, frozen by
 * the checksum) or the step's own literal one (`recreate()`, `expectDefinition()`, the forms `diff` emits). There is
 * deliberately no raw-SQL action: trigger DDL is dialect-specific, so a step holding one dialect's text would tie the
 * migration tree to it. Every create is drop-if-present, then create (replace semantics).
 */
final class TriggerActions
{
    /** @var list<MigrationAction> */
    private array $actions = [];

    private bool $leavesDeclaration = false;

    private ?TriggerSchema $declared = null;

    /**
     * @param string $relation the table or view the trigger is on
     * @param ?EntityTrigger $linked the entity's link to this step's trigger class, null when the entity no longer links it
     */
    public function __construct(
        private readonly string $relation,
        private readonly bool $onView,
        private readonly string $triggerName,
        private readonly ?EntityTrigger $linked,
        private readonly Dialect $dialect,
        private readonly string $entityName = '',
    ) {
    }

    /**
     * The trigger's initial creation, rendered from the entity's linked declaration (drop if present, then create) —
     * `DDL-001` (schema creation that does not apply) when the entity does not link this trigger class.
     */
    public function createTrigger(): MigrationAction
    {
        $linked = $this->linked ?? throw new SimpleOrmException(
            'DDL-001',
            $this->entityName !== '' ? $this->entityName : $this->triggerName,
            "does not link a trigger declaring '{$this->triggerName}'; createTrigger() renders from the metadata — "
                . 'use recreate() with a literal declaration',
        );

        $this->declare($linked->definition);

        return $this->track(new MigrationAction('create trigger ' . $this->triggerName, $this->replace($linked->definition)));
    }

    /**
     * Drops and creates the trigger from a **literal** declaration — frozen, so the applied step's rendering never
     * changes when the model evolves. `$updateOf` are column names.
     *
     * @param list<TriggerEvent> $events
     * @param list<string> $updateOf
     */
    public function recreate(TriggerTiming $timing, array $events, string $sql, array $updateOf = []): MigrationAction
    {
        $definition = $this->literal($timing, $events, $sql, $updateOf);
        $this->declare($definition);

        return $this->track(new MigrationAction('recreate trigger ' . $this->triggerName, $this->replace($definition)));
    }

    /**
     * Precondition (`MIG-012`): the live trigger must be exactly the dialect's rendering of this declaration when the
     * step applies; drift or absence refuses the run unless forced. Generated change steps carry the previous
     * snapshot's declaration here.
     *
     * @param list<TriggerEvent> $events
     * @param list<string> $updateOf
     */
    public function expectDefinition(TriggerTiming $timing, array $events, string $sql, array $updateOf = []): MigrationAction
    {
        $definition = $this->literal($timing, $events, $sql, $updateOf);

        return $this->track(new MigrationAction(
            'expect trigger ' . $this->triggerName,
            [],
            MigrationStatement::expectTrigger(
                'expect trigger ' . $this->triggerName,
                $this->triggerName,
                $this->dialect->createTriggerSql($this->relation, $this->onView, $definition),
            ),
        ));
    }

    /** Drops the trigger with whatever the dialect created beside it. */
    public function dropTrigger(): MigrationAction
    {
        $this->leavesDeclaration = true;
        $this->declared = null;

        return $this->track(new MigrationAction(
            'drop trigger ' . $this->triggerName,
            $this->dialect->dropTriggerSql($this->relation, $this->triggerName),
        ));
    }

    /**
     * Whether any action creates, recreates, or drops the trigger — what the shadow reads to take the snapshot from the
     * step instead of the catalog (ADR-0043); a step with only `expectDefinition()` leaves nothing declared.
     */
    public function leavesDeclaration(): bool
    {
        return $this->leavesDeclaration;
    }

    /** The declaration the actions leave behind, in order: the last create/recreate wins, a later drop leaves none (null). */
    public function declared(): ?TriggerSchema
    {
        return $this->declared;
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

    private function declare(TriggerDefinition $definition): void
    {
        $this->leavesDeclaration = true;
        $this->declared = new TriggerSchema($this->relation, $this->onView, $definition);
    }

    /**
     * Drop-if-present, then create — the one replace rendering every create shares.
     *
     * @return list<string>
     */
    private function replace(TriggerDefinition $definition): array
    {
        return [
            ...$this->dialect->dropTriggerSql($this->relation, $definition->name),
            ...$this->dialect->createTriggerSql($this->relation, $this->onView, $definition),
        ];
    }

    /**
     * @param list<TriggerEvent> $events
     * @param list<string> $updateOf
     */
    private function literal(TriggerTiming $timing, array $events, string $sql, array $updateOf): TriggerDefinition
    {
        return new TriggerDefinition($this->triggerName, $timing, $events, $sql, $updateOf);
    }

    private function track(MigrationAction $action): MigrationAction
    {
        $this->actions[] = $action;

        return $action;
    }
}
