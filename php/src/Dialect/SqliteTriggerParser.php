<?php

declare(strict_types=1);

namespace SimpleOrm\Dialect;

use PDO;
use SimpleOrm\Metadata\TriggerDefinition;
use SimpleOrm\Metadata\TriggerEvent;
use SimpleOrm\Metadata\TriggerTiming;
use SimpleOrm\Migrations\TriggerSchema;
use SimpleOrm\Parameters\PdoBinder;
use SimpleOrm\Parameters\SqlPlaceholders;

/**
 * Reads a stored SQLite trigger back to its neutral declaration (Level 4, ADR-0042): the shadow's history side of the
 * snapshot equality, and the migration conformance runner's deep `triggers` expect. The inverse of
 * {@see SqliteDialect::createTriggerSql()}: the header fields come from the dialect's fixed grammar (`create trigger
 * <name> <timing> <event>[ of a, b] on <relation> for each row begin … end`), the body's dialect expansions fold back
 * into the tokens (`(select new.a as a, new.b as b)` → `{new: a, b}`, the `strftime` UTC form → `{now}`), and the
 * per-event triggers of a multi-event declaration (`<name>_<event>`) merge into one. A hand-written trigger the grammar
 * does not match is not parsed: the result is null with a note. The shadow tooling is SQLite-only in every
 * implementation (spec/dialects.md "Deferred"), so this is the one parser.
 */
final class SqliteTriggerParser
{
    private const string NOW_EXPANSION = "strftime('%Y-%m-%dT%H:%M:%fZ', 'now')";

    private function __construct()
    {
    }

    /**
     * The stored create statements of a trigger name — the declared name and its per-event forms.
     *
     * @return list<string>
     */
    public static function definitions(PDO $connection, string $name): array
    {
        $statement = $connection->prepare(SqlPlaceholders::toPdo((new SqliteDialect())->triggerDefinitionSql()));
        PdoBinder::bindAndExecute($statement, ['relation' => $name]);

        return array_map(static fn (mixed $definition): string => (string) $definition, $statement->fetchAll(PDO::FETCH_COLUMN));
    }

    /**
     * The declaration the stored statements of `$name` render, or null — with `$note` set when the statements exist but
     * do not match the dialect's grammar or do not merge into one declaration (absent: null, no note).
     *
     * @param list<string> $definitions
     */
    public static function parse(string $name, array $definitions, ?string &$note = null): ?TriggerSchema
    {
        $note = null;
        if ($definitions === []) {
            return null;
        }

        $parsed = [];
        foreach ($definitions as $definition) {
            $one = self::parseOne($definition);
            if ($one === null) {
                $note = "trigger {$name}: a stored definition does not match the dialect's grammar (hand-written?); skipped";

                return null;
            }

            $parsed[] = $one;
        }

        return self::merge($name, $parsed, $note);
    }

    /**
     * @param list<array{name: string, timing: TriggerTiming, event: TriggerEvent, columns: list<string>, relation: string, body: string}> $parsed
     */
    private static function merge(string $name, array $parsed, ?string &$note): ?TriggerSchema
    {
        $first = $parsed[0];
        $events = [];
        $updateOf = [];
        foreach ($parsed as $one) {
            $expectedName = count($parsed) === 1 ? $name : $name . '_' . $one['event']->value;
            if (strcasecmp($one['name'], $expectedName) !== 0
                || $one['timing'] !== $first['timing']
                || strcasecmp($one['relation'], $first['relation']) !== 0
                || $one['body'] !== $first['body']) {
                $note = "trigger {$name}: the stored per-event triggers do not form one declaration; skipped";

                return null;
            }

            $events[] = $one['event'];
            if ($one['event'] === TriggerEvent::Update) {
                $updateOf = $one['columns'];
            }
        }

        return new TriggerSchema(
            $first['relation'],
            $first['timing'] === TriggerTiming::InsteadOf,
            new TriggerDefinition($name, $first['timing'], $events, $first['body'], $updateOf),
        );
    }

    /**
     * @return array{name: string, timing: TriggerTiming, event: TriggerEvent, columns: list<string>, relation: string, body: string}|null
     */
    private static function parseOne(string $sql): ?array
    {
        $collapsed = (string) preg_replace('/\s+/', ' ', trim($sql));
        $pattern = '/^create (?:temp |temporary )?trigger (?:if not exists )?(?<name>\S+) (?<timing>before|after|instead of) '
            . '(?<event>insert|update|delete)(?: of (?<columns>.+?))? on (?<relation>\S+) for each row begin (?<body>.*) end ?;?$/i';
        if (preg_match($pattern, $collapsed, $matches) !== 1) {
            return null;
        }

        $timing = strtolower($matches['timing']) === 'instead of' ? TriggerTiming::InsteadOf : TriggerTiming::from(strtolower($matches['timing']));
        $columns = [];
        if (($matches['columns'] ?? '') !== '') {
            foreach (explode(',', $matches['columns']) as $column) {
                $columns[] = self::unquote($column);
            }
        }

        return [
            'name' => self::unquote($matches['name']),
            'timing' => $timing,
            'event' => TriggerEvent::from(strtolower($matches['event'])),
            'columns' => $columns,
            'relation' => self::unquote($matches['relation']),
            'body' => self::fold($matches['body']),
        ];
    }

    /** Folds the dialect's token expansions back into the tokens (the whitespace is already collapsed). */
    private static function fold(string $body): string
    {
        $folded = (string) preg_replace_callback(
            '/\(select ((?:new|old)\.\w+ as \w+(?:, (?:new|old)\.\w+ as \w+)*)\)/i',
            static function (array $matches): string {
                $row = null;
                $columns = [];
                foreach (explode(', ', $matches[1]) as $part) {
                    preg_match('/^(new|old)\.(\w+) as (\w+)$/i', $part, $pieces);
                    if (($row !== null && strtolower($pieces[1]) !== $row) || $pieces[2] !== $pieces[3]) {
                        return $matches[0];   // not an expansion of ours: leave it as the author's SQL
                    }

                    $row = strtolower($pieces[1]);
                    $columns[] = $pieces[2];
                }

                return '{' . $row . ': ' . implode(', ', $columns) . '}';
            },
            $body,
        );

        return trim(str_replace(self::NOW_EXPANSION, '{now}', $folded));
    }

    private static function unquote(string $identifier): string
    {
        return trim(trim($identifier), "\"`[]");
    }
}
