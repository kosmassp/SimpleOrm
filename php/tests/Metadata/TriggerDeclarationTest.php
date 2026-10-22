<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Metadata;

use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use SimpleOrm\Errors\MappingException;
use SimpleOrm\Metadata\Attributes\Column;
use SimpleOrm\Metadata\Attributes\Generated;
use SimpleOrm\Metadata\Attributes\Key;
use SimpleOrm\Metadata\Attributes\MaterializedView;
use SimpleOrm\Metadata\Attributes\Owned;
use SimpleOrm\Metadata\Attributes\Statement;
use SimpleOrm\Metadata\Attributes\Table;
use SimpleOrm\Metadata\Attributes\Trigger;
use SimpleOrm\Metadata\Attributes\View;
use SimpleOrm\Metadata\EntityMapBuilder;
use SimpleOrm\Metadata\EntityMapJson;
use SimpleOrm\Metadata\EntityMapLoader;
use SimpleOrm\Metadata\TriggerBody;
use SimpleOrm\Metadata\TriggerEvent;
use SimpleOrm\Metadata\TriggerRules;
use SimpleOrm\Metadata\TriggerTiming;
use SimpleOrm\Tests\Sample\Models\Role;
use SimpleOrm\Tests\Sample\Models\RoleAuditTrigger;

/**
 * Triggers in the mapping (Level 4, ADR-0042): every `MAP-029` rule of spec/metadata-model.md "Triggers" has a failing
 * fixture and is exactly `MAP-029`, the errors are collected rather than first-only, the trigger class is refused as an
 * entity, the manual builder links under the same rules, and the scan rule sees what a lone load cannot.
 */
final class TriggerDeclarationTest extends TestCase
{
    // --- the valid declaration ---------------------------------------------------------------------------------

    #[Test]
    public function a_linked_declaration_resolves_to_the_entity_map_in_link_order(): void
    {
        $map = (new EntityMapLoader())->load(Role::class);

        self::assertCount(1, $map->triggers);
        self::assertSame(RoleAuditTrigger::class, $map->triggers[0]->triggerClass);
        $definition = $map->triggers[0]->definition;
        self::assertSame('trg_roles_audit', $definition->name);
        self::assertSame(TriggerTiming::After, $definition->timing);
        self::assertSame([TriggerEvent::Update], $definition->events);
        self::assertSame([], $definition->updateOf);
        self::assertSame($definition, $map->trigger('trg_roles_audit')?->definition);
        self::assertNull($map->trigger('nope'));
    }

    #[Test]
    public function events_are_a_set_normalized_to_the_fixed_order_and_update_of_resolves_to_columns(): void
    {
        $map = (new EntityMapLoader())->load(TrgMultiEventEntity::class);
        $definition = $map->triggers[0]->definition;

        self::assertSame([TriggerEvent::Insert, TriggerEvent::Update, TriggerEvent::Delete], $definition->events);
        self::assertSame(['display_name'], $definition->updateOf, 'a property name resolves to its column name');

        $export = json_decode(EntityMapJson::export($map, new EntityMapLoader()), true, flags: JSON_THROW_ON_ERROR);
        self::assertSame(['insert', 'update', 'delete'], $export['triggers'][0]['events']);
        self::assertSame(['display_name'], $export['triggers'][0]['updateOf']);
        self::assertSame(['name', 'timing', 'events', 'updateOf', 'sql'], array_keys($export['triggers'][0]));
    }

    #[Test]
    public function the_export_carries_the_body_with_its_tokens_unexpanded_and_no_trigger_class_name(): void
    {
        $export = EntityMapJson::export((new EntityMapLoader())->load(Role::class), new EntityMapLoader());

        self::assertStringContainsString('{new: id, role_name}', $export);
        self::assertStringContainsString('{now}', $export);
        self::assertStringNotContainsString('RoleAuditTrigger', $export);
    }

    #[Test]
    public function a_view_takes_an_instead_of_trigger(): void
    {
        $map = (new EntityMapLoader())->load(TrgViewEntity::class);

        self::assertSame(TriggerTiming::InsteadOf, $map->triggers[0]->definition->timing);
    }

    // --- MAP-029 through the manual builder (one trigger class per rule) ---------------------------------------

    /** @return array<string, array{class-string}> */
    public static function badTriggerClasses(): array
    {
        return [
            'a blank name' => [TrgBlankName::class],
            'no event' => [TrgNoEvent::class],
            'a blank body' => [TrgBlankBody::class],
            'updateOf without the update event' => [TrgUpdateOfWithoutUpdate::class],
            'updateOf naming an unmapped property' => [TrgUpdateOfUnmapped::class],
            'an unknown token' => [TrgUnknownToken::class],
            'a malformed token' => [TrgMalformedToken::class],
            'a -- line comment' => [TrgLineComment::class],
            'a row token without columns' => [TrgRowTokenWithoutColumns::class],
            'a row token naming an unmapped column' => [TrgUnmappedColumn::class],
            '{new} on a delete-only trigger' => [TrgNewOnDeleteOnly::class],
            '{old} on an insert-only trigger' => [TrgOldOnInsertOnly::class],
            '{new} on an insert and delete trigger' => [TrgNewOnInsertAndDelete::class],
            '{old} on an update and insert trigger' => [TrgOldOnUpdateAndInsert::class],
            'a class without a declaration' => [TrgNoDeclaration::class],
            'a class with two declarations' => [TrgTwoDeclarations::class],
            'a link to an entity' => [TrgMultiEventEntity::class],
            'a link to an owned type' => [TrgOwnedValue::class],
            'a link to something that is no class' => ['No\\Such\\TriggerClass'],
        ];
    }

    /** @param class-string $triggerClass */
    #[Test]
    #[DataProvider('badTriggerClasses')]
    public function a_bad_link_or_declaration_is_exactly_map029(string $triggerClass): void
    {
        try {
            self::plainBuilder()->trigger($triggerClass)->build();
            self::fail("linking {$triggerClass} was expected to fail");
        } catch (MappingException $e) {
            self::assertNotSame([], $e->errors);
            foreach ($e->errors as $error) {
                self::assertSame('MAP-029', $error->code, (string) $error);
            }
        }
    }

    #[Test]
    public function a_class_linked_twice_by_one_entity_is_map029(): void
    {
        $this->expectMap029(static fn () => self::plainBuilder()->trigger(TrgOk::class)->trigger(TrgOk::class)->build());
    }

    #[Test]
    public function the_builder_links_a_valid_trigger_class(): void
    {
        $map = self::plainBuilder()->trigger(TrgOk::class)->build();

        self::assertSame('trg_ok', $map->triggers[0]->definition->name);
        self::assertSame(TrgOk::class, $map->triggers[0]->triggerClass);
    }

    #[Test]
    public function errors_are_collected_never_first_only(): void
    {
        try {
            self::plainBuilder()->trigger(TrgBlankName::class)->trigger(TrgNoEvent::class)->trigger(TrgUnknownToken::class)->build();
            self::fail('expected MappingException');
        } catch (MappingException $e) {
            self::assertGreaterThanOrEqual(3, count($e->errors));
        }
    }

    #[Test]
    public function a_blank_declaration_reports_every_problem_it_has(): void
    {
        try {
            self::plainBuilder()->trigger(TrgEmptyDeclaration::class)->build();
            self::fail('expected MappingException');
        } catch (MappingException $e) {
            $text = implode("\n", array_map('strval', $e->errors));
            self::assertStringContainsString('name is blank', $text);
            self::assertStringContainsString('no event', $text);
            self::assertStringContainsString('body is blank', $text);
        }
    }

    // --- MAP-029 through the attribute loader (the source-dependent rules) ---------------------------------------

    /** @return array<string, array{class-string}> */
    public static function misplacedLinks(): array
    {
        return [
            'a link on a statement' => [TrgOnStatement::class],
            'a link on a materialized view' => [TrgOnMaterializedView::class],
            'before on a view' => [TrgBeforeOnView::class],
            'after on a view' => [TrgAfterOnView::class],
        ];
    }

    /** @param class-string $entityType */
    #[Test]
    #[DataProvider('misplacedLinks')]
    public function a_link_a_source_cannot_take_is_exactly_map029(string $entityType): void
    {
        try {
            (new EntityMapLoader())->load($entityType);
            self::fail("{$entityType} was expected to fail loading");
        } catch (MappingException $e) {
            self::assertNotSame([], $e->errors);
            foreach ($e->errors as $error) {
                self::assertSame('MAP-029', $error->code, (string) $error);
            }
        }
    }

    // --- the trigger class is never an entity --------------------------------------------------------------------

    #[Test]
    public function a_trigger_class_loaded_as_an_entity_is_map029(): void
    {
        $this->expectMap029(static fn () => (new EntityMapLoader())->load(RoleAuditTrigger::class));
    }

    #[Test]
    public function the_builder_refuses_a_trigger_class_as_the_entity(): void
    {
        $this->expectMap029(static fn () => EntityMapBuilder::for(RoleAuditTrigger::class));
    }

    #[Test]
    public function a_trigger_class_is_recognized_by_its_declaration_not_its_link(): void
    {
        self::assertTrue(TriggerRules::isTriggerClass(new \ReflectionClass(RoleAuditTrigger::class)));
        self::assertFalse(TriggerRules::isTriggerClass(new \ReflectionClass(Role::class)));
        self::assertSame('trg_roles_audit', TriggerRules::declaredName(RoleAuditTrigger::class));
    }

    // --- the scan rule ---------------------------------------------------------------------------------------------

    #[Test]
    public function a_trigger_class_linked_by_two_entities_is_one_map029(): void
    {
        $errors = TriggerRules::scanErrors([TrgSharedA::class, TrgSharedB::class, TrgShared::class]);

        self::assertCount(1, $errors);
        self::assertSame('MAP-029', $errors[0]->code);
        self::assertStringContainsString('TrgShared', $errors[0]->target);
        self::assertStringContainsString('TrgSharedA', $errors[0]->message);
        self::assertStringContainsString('TrgSharedB', $errors[0]->message);
    }

    #[Test]
    public function two_classes_declaring_one_name_are_one_map029(): void
    {
        $errors = TriggerRules::scanErrors([TrgNameTwinOne::class, TrgNameTwinTwo::class]);

        self::assertCount(1, $errors);
        self::assertSame('MAP-029', $errors[0]->code);
        self::assertSame('trg_twin', $errors[0]->target, 'a name conflict names the trigger name');
        self::assertStringContainsString('TrgNameTwinOne', $errors[0]->message);
        self::assertStringContainsString('TrgNameTwinTwo', $errors[0]->message);
    }

    #[Test]
    public function a_clean_scan_has_no_conflict_and_scans_skip_trigger_classes_as_entities(): void
    {
        // The trigger class is in the list: it is a declaration object, never mapped as an entity by a scan.
        self::assertSame([], TriggerRules::scanErrors([Role::class, RoleAuditTrigger::class]));
        self::assertFalse(EntityMapLoader::hasMappingAttributes(RoleAuditTrigger::class));
        self::assertTrue(EntityMapLoader::hasMappingAttributes(Role::class));
    }

    // --- the body grammar --------------------------------------------------------------------------------------------

    #[Test]
    public function normalization_collapses_layout_and_spells_tokens_canonically(): void
    {
        self::assertSame(
            'insert into t (a) select n.id from {new: id, name} n where x = {now};',
            TriggerBody::normalize("insert into t (a)\n    select n.id\n    from { NEW : id ,name }  n where x = {  now };\n"),
        );
    }

    #[Test]
    public function braces_inside_a_string_literal_are_text_not_tokens(): void
    {
        $problems = [];
        $segments = TriggerBody::parse("insert into t (j) values ('{\"a\": {now}}');", $problems);

        self::assertSame([], $problems);
        self::assertCount(1, $segments);
        self::assertIsString($segments[0]);
    }

    #[Test]
    public function a_quote_doubled_inside_a_literal_does_not_end_it(): void
    {
        $problems = [];
        $segments = TriggerBody::parse("select 'it''s {x} here', {now};", $problems);

        self::assertSame([], $problems);
        self::assertCount(3, $segments);
    }

    // --- helpers ------------------------------------------------------------------------------------------------------

    private static function plainBuilder(): EntityMapBuilder
    {
        return EntityMapBuilder::for(TrgPlainEntity::class)
            ->table('trg_plain')
            ->column('id', key: true, generated: true)
            ->column('name');
    }

    /** @param callable(): mixed $action */
    private function expectMap029(callable $action): void
    {
        try {
            $action();
            self::fail('expected MAP-029');
        } catch (MappingException $e) {
            self::assertSame('MAP-029', $e->errorCode);
        }
    }
}

// --- fixtures: trigger declaration classes ------------------------------------------------------------------------

#[Trigger('trg_ok', TriggerTiming::After, [TriggerEvent::Update], 'insert into trg_log (x) select n.id from {new: id} n;')]
final class TrgOk
{
}

#[Trigger('  ', TriggerTiming::After, [TriggerEvent::Update], 'select 1;')]
final class TrgBlankName
{
}

#[Trigger('trg_no_event', TriggerTiming::After, [], 'select 1;')]
final class TrgNoEvent
{
}

#[Trigger('trg_blank_body', TriggerTiming::After, [TriggerEvent::Update], "  \n ")]
final class TrgBlankBody
{
}

#[Trigger('', TriggerTiming::After, [], '')]
final class TrgEmptyDeclaration
{
}

#[Trigger('trg_uo_noupdate', TriggerTiming::After, [TriggerEvent::Insert], 'select 1;', updateOf: ['name'])]
final class TrgUpdateOfWithoutUpdate
{
}

#[Trigger('trg_uo_ghost', TriggerTiming::After, [TriggerEvent::Update], 'select 1;', updateOf: ['ghost'])]
final class TrgUpdateOfUnmapped
{
}

#[Trigger('trg_unknown', TriggerTiming::After, [TriggerEvent::Update], 'select {nope} from t;')]
final class TrgUnknownToken
{
}

#[Trigger('trg_malformed', TriggerTiming::After, [TriggerEvent::Update], 'select 1 from {new: id name} n;')]
final class TrgMalformedToken
{
}

#[Trigger('trg_line_comment', TriggerTiming::After, [TriggerEvent::Update], "select 1 from {new: id} n; -- folds the rest in a snapshot")]
final class TrgLineComment
{
}

#[Trigger('trg_new_ins_del', TriggerTiming::After, [TriggerEvent::Insert, TriggerEvent::Delete], 'select 1 from {new: id} n;')]
final class TrgNewOnInsertAndDelete
{
}

#[Trigger('trg_old_upd_ins', TriggerTiming::After, [TriggerEvent::Update, TriggerEvent::Insert], 'select 1 from {old: id} o;')]
final class TrgOldOnUpdateAndInsert
{
}

#[Trigger('trg_nocols', TriggerTiming::After, [TriggerEvent::Update], 'select 1 from {new} n;')]
final class TrgRowTokenWithoutColumns
{
}

#[Trigger('trg_ghostcol', TriggerTiming::After, [TriggerEvent::Update], 'select 1 from {new: ghost} n;')]
final class TrgUnmappedColumn
{
}

#[Trigger('trg_new_on_delete', TriggerTiming::After, [TriggerEvent::Delete], 'select 1 from {new: id} n;')]
final class TrgNewOnDeleteOnly
{
}

#[Trigger('trg_old_on_insert', TriggerTiming::After, [TriggerEvent::Insert], 'select 1 from {old: id} o;')]
final class TrgOldOnInsertOnly
{
}

final class TrgNoDeclaration
{
}

#[Trigger('trg_two_a', TriggerTiming::After, [TriggerEvent::Update], 'select 1;')]
#[Trigger('trg_two_b', TriggerTiming::After, [TriggerEvent::Update], 'select 2;')]
final class TrgTwoDeclarations
{
}

#[Owned]
final class TrgOwnedValue
{
    #[Column]
    public string $street = '';
}

#[Trigger('trg_before_on_view', TriggerTiming::Before, [TriggerEvent::Insert], 'select 1;')]
final class TrgBeforeOnViewTrigger
{
}

#[Trigger('trg_after_on_view', TriggerTiming::After, [TriggerEvent::Insert], 'select 1;')]
final class TrgAfterOnViewTrigger
{
}

#[Trigger('trg_instead_view', TriggerTiming::InsteadOf, [TriggerEvent::Insert], 'insert into trg_plain (name) select n.name from {new: name} n;')]
final class TrgInsteadOnViewTrigger
{
}

#[Trigger('trg_multi', TriggerTiming::After, [TriggerEvent::Delete, TriggerEvent::Update, TriggerEvent::Insert, TriggerEvent::Update], 'select {now};', updateOf: ['displayName'])]
final class TrgMultiEventTrigger
{
}

#[Trigger('trg_shared', TriggerTiming::After, [TriggerEvent::Update], 'select 1;')]
final class TrgShared
{
}

#[Trigger('trg_twin', TriggerTiming::After, [TriggerEvent::Update], 'select 1;')]
final class TrgNameTwinOne
{
}

#[Trigger('TRG_TWIN', TriggerTiming::After, [TriggerEvent::Update], 'select 2;')]
final class TrgNameTwinTwo
{
}

// --- fixtures: entities ---------------------------------------------------------------------------------------------

/** Mapped by the builder only (no attributes), the host of every bad-link case. */
final class TrgPlainEntity
{
    public int $id;

    public string $name = '';
}

#[Table('trg_multi')]
#[Trigger(TrgMultiEventTrigger::class)]
final class TrgMultiEventEntity
{
    #[Key]
    #[Generated]
    #[Column]
    public int $id;

    #[Column('display_name')]
    public string $displayName = '';
}

#[View('trg_view', 'select id, name from trg_plain')]
#[Trigger(TrgInsteadOnViewTrigger::class)]
final class TrgViewEntity
{
    #[Key]
    #[Column]
    public int $id;

    #[Column]
    public string $name = '';
}

#[View('trg_view_before', 'select id from trg_plain')]
#[Trigger(TrgBeforeOnViewTrigger::class)]
final class TrgBeforeOnView
{
    #[Key]
    #[Column]
    public int $id;
}

#[View('trg_view_after', 'select id from trg_plain')]
#[Trigger(TrgAfterOnViewTrigger::class)]
final class TrgAfterOnView
{
    #[Key]
    #[Column]
    public int $id;
}

#[Statement('select 1 as id')]
#[Trigger(TrgOk::class)]
final class TrgOnStatement
{
    #[Column]
    public int $id;
}

#[MaterializedView('trg_mv', 'select 1 as id')]
#[Trigger(TrgOk::class)]
final class TrgOnMaterializedView
{
    #[Key]
    #[Column]
    public int $id;
}

#[Table('trg_shared_a')]
#[Trigger(TrgShared::class)]
final class TrgSharedA
{
    #[Key]
    #[Column]
    public int $id;
}

#[Table('trg_shared_b')]
#[Trigger(TrgShared::class)]
final class TrgSharedB
{
    #[Key]
    #[Column]
    public int $id;
}
