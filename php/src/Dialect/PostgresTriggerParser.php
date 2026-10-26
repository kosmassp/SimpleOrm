<?php

declare(strict_types=1);

namespace SimpleOrm\Dialect;

use PDO;
use SimpleOrm\Metadata\TriggerDefinition;
use SimpleOrm\Metadata\TriggerEvent;
use SimpleOrm\Metadata\TriggerTiming;
use SimpleOrm\Migrations\SchemaSnapshot;
use SimpleOrm\Migrations\TriggerSchema;
use SimpleOrm\Session\StatementLog;

/**
 * Reads a stored PostgreSQL trigger back to its neutral declaration (Level 4, ADR-0043): the shadow's self-check on that
 * dialect and the migration conformance runner's deep `triggers` expect. The inverse of {@see PostgresDialect::createTriggerSql()}
 * over the two rows of `triggerDefinitionSql()` — PostgreSQL keeps no create statement, so the catalog query *reconstructs*
 * both in the dialect's own spelling and the parse is exact: the trigger row's header (`before|after|instead of`, the
 * events joined by ` or ` with `update` carrying ` of "a", "b"`, the relation), the function row's body between its
 * `begin` and the rendered `return` tail, with `(select new."a" as "a", …)` folded back to `{new: a, …}` and `now()` to
 * `{now}`. A trigger the grammar does not match (hand-written, or a changed function) is not parsed: null with a note.
 */
final class PostgresTriggerParser
{
    private function __construct()
    {
    }

    /**
     * The reconstructed statements of a trigger name (the function, then the trigger).
     *
     * @return list<string>
     */
    public static function definitions(PDO $connection, string $name, ?StatementLog $log = null): array
    {
        return TriggerCatalog::definitions($connection, new PostgresDialect(), $name, $log);
    }

    /**
     * The declaration the statements of `$name` render, or null — with `$note` set when they exist but do not match the
     * dialect's grammar (absent: null, no note).
     *
     * @param list<string> $definitions
     */
    public static function parse(string $name, array $definitions, ?string &$note = null): ?TriggerSchema
    {
        $note = null;
        if ($definitions === []) {
            return null;
        }

        $function = null;
        $trigger = null;
        foreach ($definitions as $definition) {
            $collapsed = SchemaSnapshot::normalizeTriggerDdl($definition);
            if (str_starts_with($collapsed, 'create or replace function')) {
                $function = $collapsed;
            } elseif (str_starts_with($collapsed, 'create trigger')) {
                $trigger = $collapsed;
            }
        }

        $header = $trigger === null ? null : self::parseHeader($trigger);
        $body = $function === null ? null : self::parseBody($function);
        if ($header === null || $body === null || strcasecmp($header['name'], $name) !== 0 || $header['function'] !== $name . '_fn') {
            $note = "trigger {$name}: the stored definition does not match the dialect's grammar (hand-written?); skipped";

            return null;
        }

        return new TriggerSchema(
            $header['relation'],
            $header['timing'] === TriggerTiming::InsteadOf,
            new TriggerDefinition($name, $header['timing'], $header['events'], self::fold($body), $header['updateOf']),
        );
    }

    /**
     * @return array{name: string, timing: TriggerTiming, events: list<TriggerEvent>, updateOf: list<string>, relation: string, function: string}|null
     */
    private static function parseHeader(string $sql): ?array
    {
        $pattern = '/^create trigger "(?<name>[^"]+)" (?<timing>before|after|instead of) (?<events>.+?) on "(?<relation>[^"]+)" '
            . 'for each row execute function "(?<function>[^"]+)"\(\)$/';
        if (preg_match($pattern, $sql, $matches) !== 1) {
            return null;
        }

        $events = [];
        $updateOf = [];
        foreach (preg_split('/ or (?=insert|update|delete)/', $matches['events']) ?: [] as $event) {
            if ($event === 'insert' || $event === 'delete') {
                $events[] = TriggerEvent::from($event);

                continue;
            }

            if (preg_match('/^update(?: of (?<columns>.+))?$/', $event, $update) !== 1) {
                return null;
            }

            $events[] = TriggerEvent::Update;
            if (($update['columns'] ?? '') !== '') {
                foreach (explode(', ', $update['columns']) as $column) {
                    $updateOf[] = trim($column, '"');
                }
            }
        }

        return [
            'name' => $matches['name'],
            'timing' => $matches['timing'] === 'instead of' ? TriggerTiming::InsteadOf : TriggerTiming::from($matches['timing']),
            'events' => $events,
            'updateOf' => $updateOf,
            'relation' => $matches['relation'],
            'function' => $matches['function'],
        ];
    }

    /** The body between the function's `begin` and the rendered `return` tail (whitespace already collapsed). */
    private static function parseBody(string $sql): ?string
    {
        $pattern = '/^create or replace function "[^"]+"\(\) returns trigger language plpgsql as \$\$ begin (?<body>.*) '
            . "(?:return null;|if tg_op = 'DELETE' then return old; end if; return new;) end \\$\\$$/s";

        return preg_match($pattern, $sql, $matches) === 1 ? $matches['body'] : null;
    }

    /** Folds the dialect's token expansions back into the tokens (the whitespace is already collapsed). */
    private static function fold(string $body): string
    {
        $row = '(?:new|old)\."[^"]+" as "[^"]+"';
        $folded = (string) preg_replace_callback(
            '/\(select (' . $row . '(?:, ' . $row . ')*)\)/',
            static function (array $matches): string {
                $kind = null;
                $columns = [];
                foreach (explode(', ', $matches[1]) as $part) {
                    preg_match('/^(new|old)\."([^"]+)" as "([^"]+)"$/', $part, $pieces);
                    if (($kind !== null && $pieces[1] !== $kind) || $pieces[2] !== $pieces[3]) {
                        return $matches[0];   // not an expansion of ours: leave it as the author's SQL
                    }

                    $kind = $pieces[1];
                    $columns[] = $pieces[2];
                }

                return '{' . $kind . ': ' . implode(', ', $columns) . '}';
            },
            $body,
        );

        return trim(str_replace('now()', '{now}', $folded));
    }
}
