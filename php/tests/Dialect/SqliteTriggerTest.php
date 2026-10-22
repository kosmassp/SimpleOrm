<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Dialect;

use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use SimpleOrm\Dialect\SqliteDialect;
use SimpleOrm\Dialect\SqliteTriggerParser;
use SimpleOrm\Errors\SimpleOrmException;
use SimpleOrm\Metadata\TriggerDefinition;
use SimpleOrm\Metadata\TriggerEvent;
use SimpleOrm\Metadata\TriggerTiming;
use SimpleOrm\Migrations\SchemaSnapshot;
use SimpleOrm\Session\Db;
use SimpleOrm\Tests\Sample\Models\Role;
use SimpleOrm\Tests\Sample\Models\RoleChange;
use SimpleOrm\Tests\Sample\Models\User;
use SimpleOrm\Tests\Sample\Models\UserTransactionTotal;
use SimpleOrm\Tests\Support\SampleDatabase;
use SimpleOrm\Tests\Support\TempDatabase;

/**
 * SQLite triggers (Level 4, ADR-0042): the per-event rendering of a multi-event declaration, the token expansions, the
 * parser that folds a stored trigger back to its declaration (the inverse the shadow relies on), and
 * `Db::createTriggers()` against a real database — the trigger fires once per updated row and the live definition
 * reads back as exactly the rendering.
 */
final class SqliteTriggerTest extends TestCase
{
    private TempDatabase $database;

    private SqliteDialect $dialect;

    protected function setUp(): void
    {
        $this->database = TempDatabase::create();
        $this->dialect = new SqliteDialect();
    }

    protected function tearDown(): void
    {
        $this->database->delete();
    }

    // --- rendering ----------------------------------------------------------------------------------------------

    #[Test]
    public function three_events_render_three_triggers_named_per_event_with_update_of_only_on_the_update_one(): void
    {
        $trigger = new TriggerDefinition(
            'trg_all',
            TriggerTiming::After,
            [TriggerEvent::Delete, TriggerEvent::Insert, TriggerEvent::Update],
            'insert into log (id, at) select o.id, {now} from {old: id} o;',
            ['name'],
        );

        $statements = $this->dialect->createTriggerSql('widgets', false, $trigger);

        self::assertCount(3, $statements);
        self::assertStringStartsWith('create trigger trg_all_insert after insert on widgets for each row', $statements[0]);
        self::assertStringStartsWith('create trigger trg_all_update after update of name on widgets for each row', $statements[1]);
        self::assertStringStartsWith('create trigger trg_all_delete after delete on widgets for each row', $statements[2]);
        foreach ($statements as $statement) {
            self::assertStringContainsString("from (select old.id as id) o;\nend", $statement);
        }
    }

    #[Test]
    public function the_drop_names_every_possible_per_event_trigger(): void
    {
        self::assertSame(
            [
                'drop trigger if exists trg_x',
                'drop trigger if exists trg_x_insert',
                'drop trigger if exists trg_x_update',
                'drop trigger if exists trg_x_delete',
            ],
            $this->dialect->dropTriggerSql('widgets', 'trg_x'),
        );
    }

    #[Test]
    public function a_malformed_token_reaching_a_dialect_is_map029(): void
    {
        $trigger = new TriggerDefinition('trg_bad', TriggerTiming::After, [TriggerEvent::Update], 'select {nope};');

        try {
            $this->dialect->createTriggerSql('widgets', false, $trigger);
            self::fail('expected MAP-029');
        } catch (SimpleOrmException $e) {
            self::assertSame('MAP-029', $e->errorCode);
        }
    }

    #[Test]
    public function instead_of_on_a_table_is_ddl005_and_on_a_view_renders(): void
    {
        $trigger = new TriggerDefinition('trg_io', TriggerTiming::InsteadOf, [TriggerEvent::Delete], 'delete from t where id in (select o.id from {old: id} o);');

        try {
            $this->dialect->createTriggerSql('widgets', false, $trigger);
            self::fail('expected DDL-005');
        } catch (SimpleOrmException $e) {
            self::assertSame('DDL-005', $e->errorCode);
        }

        self::assertCount(1, $this->dialect->createTriggerSql('widget_summaries', true, $trigger));
    }

    // --- the parser: the inverse of the rendering ---------------------------------------------------------------

    #[Test]
    public function a_rendered_declaration_parses_back_to_itself(): void
    {
        foreach (self::declarations() as $label => [$onView, $trigger]) {
            $statements = $this->dialect->createTriggerSql('widgets', $onView, $trigger);
            $note = null;
            $schema = SqliteTriggerParser::parse($trigger->name, $statements, $note);

            self::assertNull($note, $label);
            self::assertNotNull($schema, $label);
            self::assertSame('widgets', $schema->relation, $label);
            self::assertSame($onView, $schema->onView, $label);
            self::assertTrue($schema->trigger->sameAs($trigger), "{$label}: the parsed declaration differs");
            self::assertSame($trigger->name, $schema->name(), $label);
        }
    }

    /** @return array<string, array{bool, TriggerDefinition}> */
    private static function declarations(): array
    {
        return [
            'one event' => [false, new TriggerDefinition('trg_one', TriggerTiming::After, [TriggerEvent::Update], "insert into log (id, at)\nselect n.id, {now} from {new: id, name} n;")],
            'several events, narrowed' => [false, new TriggerDefinition('trg_many', TriggerTiming::Before, [TriggerEvent::Update, TriggerEvent::Delete], 'select 1 from {old: id, name} o;', ['name', 'price'])],
            'instead of on a view' => [true, new TriggerDefinition('trg_view', TriggerTiming::InsteadOf, [TriggerEvent::Insert], 'insert into widgets (name) select n.name from {new: name} n;')],
        ];
    }

    #[Test]
    public function a_stored_trigger_that_does_not_match_the_grammar_is_skipped_with_a_note(): void
    {
        $note = null;
        $schema = SqliteTriggerParser::parse('trg_hand', ['CREATE TRIGGER trg_hand AFTER UPDATE ON widgets BEGIN select 1; END'], $note);

        self::assertNull($schema);
        self::assertNotNull($note);
        self::assertStringContainsString('trg_hand', $note);
    }

    #[Test]
    public function per_event_triggers_that_disagree_do_not_merge(): void
    {
        $note = null;
        $schema = SqliteTriggerParser::parse('trg_x', [
            "CREATE TRIGGER trg_x_insert after insert on widgets for each row\nbegin\nselect 1;\nend",
            "CREATE TRIGGER trg_x_update after update on widgets for each row\nbegin\nselect 2;\nend",
        ], $note);

        self::assertNull($schema);
        self::assertNotNull($note);
    }

    #[Test]
    public function an_absent_trigger_parses_to_nothing_without_a_note(): void
    {
        $note = 'unset';
        self::assertNull(SqliteTriggerParser::parse('trg_none', [], $note));
        self::assertNull($note);
    }

    // --- Db::createTriggers against a real database -----------------------------------------------------------------

    #[Test]
    public function the_declared_trigger_fires_once_per_updated_row(): void
    {
        $db = SampleDatabase::open($this->database);
        $db->createTriggers(Role::class);

        $first = SampleDatabase::insertRole($db, 'first');
        $second = SampleDatabase::insertRole($db, 'second');
        self::assertSame([], $db->queryAll(RoleChange::class), 'an insert fires nothing: the trigger is on update');

        $db->connection()->exec('update roles set is_deleted = 0');   // one statement, two rows

        $changes = $db->queryAll(RoleChange::class);
        self::assertCount(2, $changes);
        self::assertEqualsCanonicalizing([$first->id, $second->id], array_map(static fn (RoleChange $c): int => $c->roleId, $changes));
        self::assertEqualsCanonicalizing(['first', 'second'], array_map(static fn (RoleChange $c): string => $c->roleName, $changes));
        self::assertSame(0, $changes[0]->changedAt->getOffset(), 'the {now} token writes the §7.9 UTC form, which reads back marked');
    }

    #[Test]
    public function creating_the_triggers_again_replaces_them_and_the_trigger_still_fires_once(): void
    {
        $db = SampleDatabase::open($this->database);
        $db->createTriggers(Role::class);
        $db->createTriggers(Role::class);

        $role = SampleDatabase::insertRole($db, 'only');
        $role->name = 'renamed';
        $db->update($role);

        self::assertCount(1, $db->queryAll(RoleChange::class));
    }

    #[Test]
    public function the_live_definition_reads_back_as_exactly_the_rendering(): void
    {
        $db = SampleDatabase::open($this->database);
        $db->createTriggers(Role::class);

        $map = $db->maps()->load(Role::class);
        $rendered = $this->dialect->createTriggerSql('roles', false, $map->triggers[0]->definition);
        $live = SqliteTriggerParser::definitions($db->connection(), 'trg_roles_audit');

        self::assertSame(
            array_map(SchemaSnapshot::normalizeTriggerDdl(...), $rendered),
            array_map(SchemaSnapshot::normalizeTriggerDdl(...), $live),
        );

        $parsed = SqliteTriggerParser::parse('trg_roles_audit', $live);
        self::assertNotNull($parsed);
        self::assertTrue($parsed->trigger->sameAs($map->triggers[0]->definition));
    }

    #[Test]
    public function an_entity_that_links_no_trigger_is_a_no_op_and_a_view_without_one_too(): void
    {
        $db = SampleDatabase::open($this->database);

        $db->createTriggers(User::class);
        $db->createTriggers(UserTransactionTotal::class);

        $count = $db->connection()->query("select count(*) from sqlite_master where type = 'trigger'");
        self::assertNotFalse($count);
        self::assertSame(0, (int) $count->fetchColumn());
    }

    #[Test]
    public function triggers_on_a_statement_entity_are_ddl001(): void
    {
        $db = Db::open($this->database->connectionString(), SampleDatabase::options());

        try {
            $db->createTriggers(\SimpleOrm\Tests\Sample\Models\DailySales::class);
            self::fail('expected DDL-001');
        } catch (SimpleOrmException $e) {
            self::assertSame('DDL-001', $e->errorCode);
        }
    }
}
