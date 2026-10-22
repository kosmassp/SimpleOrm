<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Dialect;

use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use SimpleOrm\Dialect\PostgresDialect;
use SimpleOrm\Dialect\PostgresTriggerParser;
use SimpleOrm\Metadata\TriggerDefinition;
use SimpleOrm\Metadata\TriggerEvent;
use SimpleOrm\Metadata\TriggerTiming;

/**
 * The PostgreSQL trigger read-back (ADR-0043, spec/migrations.md "Dialects"): the inverse of
 * `PostgresDialect::createTriggerSql()` over the two rows `triggerDefinitionSql()` reconstructs. PostgreSQL keeps the
 * body verbatim between the rendered `begin` and `return` tail, and the header is the dialect's fixed grammar, so the
 * parse is exact: every declaration the dialect can render parses back to itself.
 */
final class PostgresTriggerParserTest extends TestCase
{
    /** @return iterable<string, array{TriggerDefinition, bool}> */
    public static function declarations(): iterable
    {
        yield 'an after update with a row token and now' => [
            new TriggerDefinition('trg_a', TriggerTiming::After, [TriggerEvent::Update], 'insert into log (id, at) select n.id, {now} from {new: id, name} n;'),
            false,
        ];
        yield 'several events, deleted rows' => [
            new TriggerDefinition('trg_b', TriggerTiming::After, [TriggerEvent::Update, TriggerEvent::Delete], 'delete from log where id in (select o.id from {old: id} o);'),
            false,
        ];
        yield 'update of columns, before' => [
            new TriggerDefinition('trg_c', TriggerTiming::Before, [TriggerEvent::Update], 'select 1;', ['name', 'code']),
            false,
        ];
        yield 'update of beside insert' => [
            new TriggerDefinition('trg_d', TriggerTiming::After, [TriggerEvent::Insert, TriggerEvent::Update], 'select {new: id};', ['name']),
            false,
        ];
        yield 'instead of on a view' => [
            new TriggerDefinition('trg_e', TriggerTiming::InsteadOf, [TriggerEvent::Insert], 'insert into base (id) select n.id from {new: id} n;'),
            true,
        ];
        yield 'a body with no token and a multi-line layout' => [
            new TriggerDefinition('trg_f', TriggerTiming::After, [TriggerEvent::Insert], "update t\n   set n = n + 1\n where id = 1;"),
            false,
        ];
    }

    #[Test]
    #[DataProvider('declarations')]
    public function a_rendered_trigger_parses_back_to_its_declaration(TriggerDefinition $declaration, bool $onView): void
    {
        $statements = (new PostgresDialect())->createTriggerSql('widgets', $onView, $declaration);

        $note = null;
        $schema = PostgresTriggerParser::parse($declaration->name, array_reverse($statements), $note);

        self::assertNull($note);
        self::assertNotNull($schema);
        self::assertSame('widgets', $schema->relation);
        self::assertSame($onView, $schema->onView);
        self::assertSame($declaration->timing, $schema->trigger->timing);
        self::assertSame($declaration->events, $schema->trigger->events);
        self::assertSame($declaration->updateOf, $schema->trigger->updateOf);
        self::assertSame($declaration->normalizedSql(), $schema->trigger->normalizedSql());
    }

    #[Test]
    public function an_absent_trigger_is_null_without_a_note(): void
    {
        $note = 'unset';

        self::assertNull(PostgresTriggerParser::parse('trg_none', [], $note));
        self::assertNull($note);
    }

    #[Test]
    public function a_function_the_grammar_does_not_match_is_skipped_with_a_note(): void
    {
        $declaration = new TriggerDefinition('trg_x', TriggerTiming::After, [TriggerEvent::Update], 'select 1;');
        [$function, $trigger] = (new PostgresDialect())->createTriggerSql('widgets', false, $declaration);

        $note = null;
        $schema = PostgresTriggerParser::parse(
            'trg_x',
            [str_replace('return null;', 'perform pg_sleep(0); return null;', $function), $trigger],
            $note,
        );

        self::assertNotNull($schema, 'extra statements before the return tail are simply part of the body');

        $note = null;
        $broken = PostgresTriggerParser::parse('trg_x', [str_replace('return null;', 'return new;', $function), $trigger], $note);

        self::assertNull($broken);
        self::assertNotNull($note);
    }
}
