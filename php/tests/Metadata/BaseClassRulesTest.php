<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Metadata;

use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use SimpleOrm\Discovery\ClassScanner;
use SimpleOrm\Errors\MappingException;
use SimpleOrm\Errors\SchemaValidationException;
use SimpleOrm\Metadata\Attributes\Column;
use SimpleOrm\Metadata\Attributes\ForeignKey;
use SimpleOrm\Metadata\Attributes\Generated;
use SimpleOrm\Metadata\Attributes\Index;
use SimpleOrm\Metadata\Attributes\Key;
use SimpleOrm\Metadata\Attributes\ManyToOne;
use SimpleOrm\Metadata\Attributes\Statement;
use SimpleOrm\Metadata\Attributes\Table;
use SimpleOrm\Metadata\Attributes\View;
use SimpleOrm\Metadata\BaseClassRules;
use SimpleOrm\Metadata\EntityMapBuilder;
use SimpleOrm\Metadata\EntityMapLoader;
use SimpleOrm\Metadata\MappingOptions;
use SimpleOrm\Query\FetchMode;
use SimpleOrm\Session\EmptyArgs;
use SimpleOrm\Session\Query;
use SimpleOrm\Session\TrackingMode;
use SimpleOrm\Tests\Sample\Models\BaseModel;
use SimpleOrm\Tests\Sample\Models\User;
use SimpleOrm\Tests\Support\SampleDatabase;
use SimpleOrm\Tests\Support\TempDatabase;
use SimpleOrm\Validation\SchemaGuard;
use SimpleOrm\Dialect\SqliteDialect;
use SimpleOrm\Session\DbOptions;
use SimpleOrm\Errors\SimpleOrmException;

/**
 * `MAP-028` - a base class is never an entity (ADR-0041, spec/metadata-model.md): the abstract class refused on
 * every path that loads a map, the parent-chain declaration rule, and the assembly-scan rule. PHP's
 * `isAbstract()` and `getParentClass()` cover all of it - no divergence (CODING-STANDARD §10).
 */
final class BaseClassRulesTest extends TestCase
{
    private TempDatabase $database;

    protected function setUp(): void
    {
        $this->database = TempDatabase::create();
    }

    protected function tearDown(): void
    {
        $this->database->delete();
    }

    // --- case 1: an abstract class loaded as an entity ---------------------------------------------------------

    #[Test]
    public function the_loader_refuses_an_abstract_class(): void
    {
        $this->assertMap028(static fn () => (new EntityMapLoader())->load(AbstractThing::class), 'AbstractThing');
        // The fixture's own base class, annotated with column hooks, is no different.
        $this->assertMap028(static fn () => (new EntityMapLoader())->load(BaseModel::class), 'BaseModel');
    }

    #[Test]
    public function the_loader_refuses_an_abstract_class_even_when_a_map_is_registered_for_it(): void
    {
        // A registration of the abstract class is refused too, before any map is consulted.
        $this->assertMap028(static fn () => new MappingOptions(explicitMaps: [AbstractThing::class => (new EntityMapLoader())->load(ConcreteThing::class)]), 'AbstractThing');
    }

    #[Test]
    public function the_manual_builder_refuses_to_register_an_abstract_class(): void
    {
        $this->assertMap028(static fn () => EntityMapBuilder::for(AbstractThing::class), 'AbstractThing');
    }

    #[Test]
    public function a_registry_query_with_an_abstract_result_type_refuses(): void
    {
        $db = SampleDatabase::open($this->database);

        $this->assertMap028(
            static fn () => $db->query(Query::inline(EmptyArgs::class, BaseModel::class, 'select created_at, updated_at from users'), EmptyArgs::value()),
            'BaseModel',
        );
    }

    #[Test]
    public function a_tracked_session_refuses_an_abstract_result_type_too(): void
    {
        $db = SampleDatabase::open($this->database, new DbOptions(new SqliteDialect(), tracking: TrackingMode::Tracked));

        $this->assertMap028(
            static fn () => $db->query(Query::inline(EmptyArgs::class, BaseModel::class, 'select created_at, updated_at from users'), EmptyArgs::value()),
            'BaseModel',
        );
    }

    #[Test]
    public function a_criteria_query_a_key_read_and_a_write_refuse_an_abstract_class(): void
    {
        $db = SampleDatabase::open($this->database);

        $this->assertMap028(static fn () => $db->from(AbstractThing::class), 'AbstractThing');
        $this->assertMap028(static fn () => $db->get(AbstractThing::class, 1), 'AbstractThing');
        $this->assertMap028(static fn () => $db->getOrDefault(AbstractThing::class, 1), 'AbstractThing');
        $this->assertMap028(static fn () => $db->queryAll(AbstractThing::class), 'AbstractThing');
        $this->assertMap028(static fn () => $db->delete(AbstractThing::class, 1), 'AbstractThing');
        $this->assertMap028(static fn () => $db->createTable(AbstractThing::class), 'AbstractThing');
    }

    #[Test]
    public function loading_a_navigation_typed_to_an_abstract_class_refuses(): void
    {
        $db = SampleDatabase::open($this->database);
        $db->createTable(AbstractTargetOwner::class);
        $owner = new AbstractTargetOwner();
        $owner->targetId = 1;
        $owner->createdAtUtc = SampleDatabase::seedTime();
        $db->insert($owner);

        $this->assertMap028(static fn () => $db->load($owner, 'target'), 'AbstractThing');
        $this->assertMap028(static fn () => $db->loadEach([$owner], 'target'), 'AbstractThing');
    }

    #[Test]
    public function eager_loading_a_navigation_typed_to_an_abstract_class_refuses(): void
    {
        $db = SampleDatabase::open($this->database);
        $db->createTable(AbstractTargetOwner::class);
        $owner = new AbstractTargetOwner();
        $owner->targetId = 1;
        $owner->createdAtUtc = SampleDatabase::seedTime();
        $db->insert($owner);

        foreach ([FetchMode::MultiQuery, FetchMode::SubSelect, FetchMode::Join] as $mode) {
            $this->assertMap028(static fn () => $db->from(AbstractTargetOwner::class)->include('target')->fetch($mode)->toList(), 'AbstractThing');
        }
    }

    #[Test]
    public function a_concrete_subclass_of_an_abstract_class_loads_and_inherits_its_members(): void
    {
        $map = (new EntityMapLoader())->load(ConcreteThing::class);

        self::assertSame('things', $map->relationName);
        self::assertSame(['name', 'id'], array_map(static fn ($p) => $p->columnName, $map->properties));
    }

    // --- case 2: a parent carrying a relation source or an index -----------------------------------------------

    #[Test]
    public function a_subclass_of_a_table_parent_is_map028_naming_both_classes(): void
    {
        $exception = $this->loadFailing(TabledChild::class);

        self::assertSame('MAP-028', $exception->errorCode);
        self::assertStringContainsString('TabledChild', (string) $exception->getMessage());
        self::assertStringContainsString('TabledBase', $exception->getMessage());
        self::assertStringContainsString('#[Table]', $exception->getMessage());
    }

    #[Test]
    public function a_subclass_of_a_view_parent_is_map028(): void
    {
        $exception = $this->loadFailing(ViewChild::class);

        self::assertSame('MAP-028', $exception->errorCode);
        self::assertStringContainsString('ViewBase', $exception->getMessage());
        self::assertStringContainsString('#[View]', $exception->getMessage());
    }

    #[Test]
    public function a_subclass_of_a_statement_parent_is_map028(): void
    {
        $exception = $this->loadFailing(StatementChild::class);

        self::assertSame('MAP-028', $exception->errorCode);
        self::assertStringContainsString('StatementBase', $exception->getMessage());
        self::assertStringContainsString('#[Statement]', $exception->getMessage());
    }

    #[Test]
    public function a_subclass_of_an_indexed_parent_is_map028_even_without_a_relation_source(): void
    {
        $exception = $this->loadFailing(IndexedChild::class);

        self::assertSame('MAP-028', $exception->errorCode);
        self::assertStringContainsString('IndexedBase', $exception->getMessage());
        self::assertStringContainsString('#[Index]', $exception->getMessage());
    }

    #[Test]
    public function the_parent_chain_is_walked_two_levels_up(): void
    {
        $exception = $this->loadFailing(Grandchild::class);

        self::assertSame('MAP-028', $exception->errorCode);
        self::assertStringContainsString('Grandchild', $exception->getMessage());
        self::assertStringContainsString('TabledBase', $exception->getMessage());
    }

    #[Test]
    public function an_abstract_parent_carrying_a_table_is_map028_on_its_subclass(): void
    {
        $exception = $this->loadFailing(FromAbstractTabled::class);

        self::assertSame('MAP-028', $exception->errorCode);
        self::assertStringContainsString('AbstractTabled', $exception->getMessage());
        // Loading the abstract parent itself is the abstract rule.
        $this->assertMap028(static fn () => (new EntityMapLoader())->load(AbstractTabled::class), 'AbstractTabled');
    }

    #[Test]
    public function the_parent_error_is_collected_beside_the_classes_other_errors(): void
    {
        $exception = $this->loadFailing(TabledChildWithStrayProperty::class);

        $codes = array_map(static fn ($error) => $error->code, $exception->errors);
        self::assertContains('MAP-028', $codes);
        self::assertContains('MAP-010', $codes);
        self::assertGreaterThanOrEqual(2, count($exception->errors));
    }

    #[Test]
    public function a_subclass_with_only_inherited_members_still_sees_the_parents_table(): void
    {
        // No attribute of its own anywhere on the class: class attributes are not inherited, and the rule still sees the parent.
        $exception = $this->loadFailing(BareChildOfTabled::class);

        self::assertSame('MAP-028', $exception->errorCode);
    }

    #[Test]
    public function the_builder_collects_the_parent_rule_too(): void
    {
        try {
            EntityMapBuilder::for(TabledChild::class)->table('manual')->column('id', key: true)->column('name')->build();
            self::fail('expected MAP-028');
        } catch (MappingException $exception) {
            self::assertSame('MAP-028', $exception->errorCode);
            self::assertStringContainsString('TabledBase', $exception->getMessage());
        }
    }

    #[Test]
    public function a_write_through_a_subclass_of_a_table_parent_refuses(): void
    {
        $db = SampleDatabase::open($this->database);
        $child = new TabledChild();
        $child->id = 1;
        $child->name = 'x';

        $this->assertMap028(static fn () => $db->insert($child), 'TabledChild');
    }

    // --- case 3: the assembly scan --------------------------------------------------------------------------------

    #[Test]
    public function the_scan_reports_a_concrete_sourceless_class_that_another_scanned_class_extends(): void
    {
        $bases = BaseClassRules::scan([ScanBase::class, ScanChildA::class, ScanChildB::class, User::class]);

        self::assertSame([ScanBase::class => [ScanChildA::class, ScanChildB::class]], $bases);

        $errors = BaseClassRules::scanErrors([ScanChildB::class, ScanBase::class, ScanChildA::class]);
        self::assertCount(1, $errors);
        self::assertSame('MAP-028', $errors[0]->code);
        self::assertSame('ScanBase', $errors[0]->target);
        self::assertStringContainsString('ScanChildA, ScanChildB', $errors[0]->message);
    }

    #[Test]
    public function the_scan_follows_the_chain_through_intermediate_classes(): void
    {
        $bases = BaseClassRules::scan([ScanBase::class, ScanMiddle::class, ScanLeaf::class]);

        self::assertSame(
            [ScanBase::class => [ScanLeaf::class, ScanMiddle::class], ScanMiddle::class => [ScanLeaf::class]],
            $bases,
        );
    }

    #[Test]
    public function a_lone_load_of_a_scanned_base_still_succeeds(): void
    {
        $map = (new EntityMapLoader())->load(ScanBase::class);

        self::assertSame('scan_base', $map->relationName);
        // And a scan that does not contain an extender cannot see it.
        self::assertSame([], BaseClassRules::scan([ScanBase::class]));
    }

    #[Test]
    public function abstract_classes_and_the_sample_fixture_are_not_bases(): void
    {
        self::assertSame([], BaseClassRules::scan([AbstractThing::class, ConcreteThing::class]));
        self::assertSame([], BaseClassRules::scan(ClassScanner::classes(__DIR__ . '/../Sample/Models', 'SimpleOrm\\Tests\\Sample\\Models')));
    }

    #[Test]
    public function schema_guard_reports_the_base_once_and_does_not_validate_it_as_a_relation(): void
    {
        $db = SampleDatabase::open($this->database);

        $errors = SchemaGuard::report($db, [ScanBase::class, ScanChildA::class, ScanChildB::class]);

        $map028 = array_values(array_filter($errors, static fn ($e) => $e->code === 'MAP-028'));
        self::assertCount(1, $map028);
        self::assertSame('ScanBase', $map028[0]->target);
        self::assertStringContainsString('ScanChildA', $map028[0]->message);
        self::assertStringContainsString('ScanChildB', $map028[0]->message);
        // The base's table does not exist - had it been validated as a relation it would add VAL-012 under its own name.
        foreach ($errors as $error) {
            self::assertFalse($error->code === 'VAL-012' && $error->target === 'ScanBase');
        }

        try {
            SchemaGuard::validate($db, [ScanBase::class, ScanChildA::class, ScanChildB::class]);
            self::fail('expected SchemaValidationException');
        } catch (SchemaValidationException $exception) {
            self::assertStringContainsString('MAP-028', $exception->getMessage());
        }
    }

    #[Test]
    public function schema_guard_turns_a_registry_entry_with_a_base_result_type_into_report_rows(): void
    {
        $db = SampleDatabase::open($this->database);

        $errors = SchemaGuard::report($db, [BaseResultRegistry::class]);

        $targets = array_map(static fn ($e) => $e->code . ' ' . $e->target, $errors);
        self::assertContains('MAP-028 BaseResultRegistry.abstractResult', $targets);
        self::assertContains('MAP-028 BaseResultRegistry.tabledChildResult', $targets);
    }

    // --- helpers ----------------------------------------------------------------------------------------------------

    /** @param callable(): mixed $action */
    private function assertMap028(callable $action, string $mentioned): void
    {
        try {
            $action();
            self::fail('expected MAP-028');
        } catch (SimpleOrmException $exception) {
            self::assertSame('MAP-028', $exception->errorCode);
            self::assertStringContainsString($mentioned, $exception->getMessage());
        }
    }

    /** @param class-string $type */
    private function loadFailing(string $type): MappingException
    {
        try {
            (new EntityMapLoader())->load($type);
        } catch (MappingException $exception) {
            return $exception;
        }

        self::fail("{$type} was expected to fail loading");
    }
}

abstract class AbstractThing
{
    #[Key]
    #[Generated]
    #[Column]
    public int $id;
}

#[Table('things')]
final class ConcreteThing extends AbstractThing
{
    #[Column]
    public string $name;
}

#[Table('abstract_target_owners')]
final class AbstractTargetOwner extends BaseModel
{
    #[Key]
    #[Generated]
    #[Column]
    public int $id;

    #[Column]
    #[ForeignKey(AbstractThing::class)]
    public int $targetId;

    #[ManyToOne('targetId')]
    public private(set) ?AbstractThing $target = null;
}

#[Table('tabled_bases')]
class TabledBase
{
    #[Key]
    #[Column]
    public int $id;
}

final class TabledChild extends TabledBase
{
    #[Column]
    public string $name;
}

class BareChildOfTabled extends TabledBase
{
}

class MiddleOfTabled extends TabledBase
{
    #[Column]
    public ?string $note = null;
}

final class Grandchild extends MiddleOfTabled
{
    #[Column]
    public ?string $other = null;
}

final class TabledChildWithStrayProperty extends TabledBase
{
    public ?string $forgotten = null;
}

#[View('view_bases', 'select 1 as id')]
class ViewBase
{
    #[Column]
    public int $id;
}

final class ViewChild extends ViewBase
{
    #[Column]
    public ?string $extra = null;
}

#[Statement('select 1 as id')]
class StatementBase
{
    #[Column]
    public int $id;
}

final class StatementChild extends StatementBase
{
    #[Column]
    public ?string $extra = null;
}

#[Index(['id'])]
class IndexedBase
{
    #[Key]
    #[Column]
    public int $id;
}

#[Table('indexed_children')]
final class IndexedChild extends IndexedBase
{
    #[Column]
    public ?string $extra = null;
}

#[Table('abstract_tabled')]
abstract class AbstractTabled
{
    #[Key]
    #[Column]
    public int $id;
}

final class FromAbstractTabled extends AbstractTabled
{
    #[Column]
    public ?string $extra = null;
}

class ScanBase
{
    #[Key]
    #[Generated]
    #[Column]
    public int $id;
}

#[Table('scan_child_a')]
final class ScanChildA extends ScanBase
{
    #[Column]
    public ?string $a = null;
}

#[Table('scan_child_b')]
final class ScanChildB extends ScanBase
{
    #[Column]
    public ?string $b = null;
}

class ScanMiddle extends ScanBase
{
    #[Column]
    public ?string $middle = null;
}

#[Table('scan_leaf')]
final class ScanLeaf extends ScanMiddle
{
    #[Column]
    public ?string $leaf = null;
}

/** Registry entries whose result type is a base class: SchemaGuard reports rows instead of letting the loader failure escape. */
final class BaseResultRegistry
{
    public static function abstractResult(): Query
    {
        return Query::inline(EmptyArgs::class, AbstractThing::class, 'select 1 as id');
    }

    public static function tabledChildResult(): Query
    {
        return Query::inline(EmptyArgs::class, TabledChild::class, 'select 1 as id, \'x\' as name');
    }
}
