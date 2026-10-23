<?php

declare(strict_types=1);

namespace SimpleOrm\Session;

use Closure;
use PDO;
use PDOStatement;
use SimpleOrm\Parameters\PdoBinder;
use SimpleOrm\Parameters\SqlPlaceholders;

use function array_fill;
use function array_map;
use function count;

/**
 * Runs the operations of **one call** — a flush, or the send of an explicit batch — as messages (Level 4, ADR-0044,
 * spec/session.md "Batching"). It owns the plan and its realization; the caller owns the transaction and the journal.
 *
 * **The plan** (rules 1–3 of the spec, shared by both callers): walking the operations in order, a statement starts a
 * new message when the current one is not empty and (1) it depends on it — decided *before* the statement is prepared,
 * because preparing reads what earlier statements produce: the message holds a statement on the same instance or on
 * another instance of the same entity type with the same set key (so the identity gate reads a map the message's
 * statements have already updated, as the immediate calls would), or the statement's instance points (a many-to-one
 * navigation, a cascade owner) at an instance the message inserts with a database-generated key; (2) it reads a result
 * after a registry command; (3) it would overflow the message — the statement count, or the dialect's parameter cap.
 * Rules 2 and 3 are decided after the statement is prepared. An operation that issues no statement adds nothing. Under
 * `Off` every statement is a message of its own. A message that holds `MaxStatementsPerMessage` statements goes out as
 * soon as it is full, before the next statement is prepared (the spec's timing, like a rule-1 cut).
 *
 * **The realization**: a message's statements are all prepared first, then run — one multi-statement round trip where
 * the dialect supports it (`Session\MessageText`, results read in order), else one at a time, in order, each judged
 * before the next runs, **reusing one prepared PDO statement per distinct text** within the call (under `Auto` only:
 * `Off` is the Level 0–3 session exactly). A registry command, whose own count a multi-statement text cannot tell
 * from its neighbours', is always sent on its own. Failure — a gate, a provider error, a zero-row check — propagates
 * as it is raised; the caller's journal undoes the in-memory effects.
 *
 * @internal
 */
final class WriteRun
{
    /** @var array<string, PDOStatement> the prepared statements of this call, by their PDO text */
    private array $statements = [];

    private int $messages = 0;

    /**
     * @param int $maxStatements `MaxStatementsPerMessage`
     * @param int|null $maxParameters the dialect's `MaxParametersPerMessage`
     * @param bool $countsBySelect the dialect's `ReportsAffectedRowsBySelect` (plan rule 2 and the reading of a count)
     * @param bool $inOneRoundTrip `BatchingInEffect`: a planned message goes out as one multi-statement text
     * @param (Closure(int): void)|null $observer the test hook: told each message's statement count as it is sent
     */
    public function __construct(
        private readonly PDO $connection,
        private readonly BatchingMode $mode,
        private readonly int $maxStatements,
        private readonly ?int $maxParameters,
        private readonly bool $countsBySelect,
        private readonly bool $inOneRoundTrip,
        private readonly ?Closure $observer,
    ) {
    }

    /** @param list<WriteOperation> $operations */
    public function run(array $operations, FlushJournal $journal): BatchResult
    {
        $affected = array_fill(0, count($operations), 0);
        $message = new PlannedMessage();

        foreach ($operations as $index => $operation) {
            if (!$message->isEmpty() && $this->dependsOn($operation, $message)) {
                $this->send($message, $affected);
            }

            $operation->record($journal);
            $write = $operation->prepare();
            if ($write === null) {
                continue;   // no statement: nothing joins the message, the count stays 0
            }

            if (!$message->isEmpty() && $this->overflows($write, $message)) {
                $this->send($message, $affected);
            }

            $message->add($index, $write);
            if ($this->mode === BatchingMode::Off || $message->count() >= $this->maxStatements) {
                $this->send($message, $affected);
            }
        }

        $this->send($message, $affected);

        return new BatchResult($affected, $this->messages);
    }

    /** Plan rule 1: the statement needs what the current message produces, so it cannot join it. */
    private function dependsOn(WriteOperation $operation, PlannedMessage $message): bool
    {
        if ($operation->instance !== null && $message->holdsStatementOn($operation->instance)) {
            return true;   // 1a: the same instance
        }

        // 1a: another instance of the same type with the same set key — the identity gate must read a map the message's
        // statements have already updated, as the immediate calls would (spec/session.md Clarification 4).
        $identity = $operation->identity();
        if ($identity !== null && $message->holdsStatementWithIdentity($identity)) {
            return true;
        }

        foreach ($operation->parents() as $parent) {
            if ($message->insertsGeneratedKeyOf($parent)) {
                return true;   // 1b
            }
        }

        return false;
    }

    /** Plan rules 2 and 3, decided once the statement is prepared. */
    private function overflows(PreparedWrite $write, PlannedMessage $message): bool
    {
        if ($message->holdsCommand() && $write->readsResult($this->countsBySelect)) {
            return true;   // 2: the command's own result sets would come first
        }

        // The statement count is not asked here: a message that holds MaxStatementsPerMessage statements was sent the moment
        // it filled (run()), so the one this statement meets is never full.
        return $this->maxParameters !== null
            && $message->parameterCount() + $write->parameterCount() > $this->maxParameters;
    }

    /**
     * @param array<int, int> $affected per operation, written by reference as each statement completes
     */
    private function send(PlannedMessage $message, array &$affected): void
    {
        if ($message->isEmpty()) {
            return;
        }

        $this->messages++;
        if ($this->observer !== null) {
            ($this->observer)($message->count());
        }

        $entries = $message->entries();
        $message->clear();

        if (!$this->inOneRoundTrip) {
            foreach ($entries as [$index, $write]) {
                $affected[$index] = $this->runOne($write);
            }

            return;
        }

        foreach ($this->groups($entries) as $group) {
            if (count($group) === 1) {
                $affected[$group[0][0]] = $this->runOne($group[0][1]);

                continue;
            }

            $this->runGroup($group, $affected);
        }
    }

    /**
     * The consecutive statements one multi-statement text can carry; a statement whose result a text cannot tell apart
     * from its neighbours' (a registry command's count) forms a group of its own.
     *
     * @param list<array{0: int, 1: PreparedWrite}> $entries
     * @return list<list<array{0: int, 1: PreparedWrite}>>
     */
    private function groups(array $entries): array
    {
        $groups = [];
        $open = [];
        foreach ($entries as $entry) {
            if ($this->carriable($entry[1])) {
                $open[] = $entry;

                continue;
            }

            if ($open !== []) {
                $groups[] = $open;
                $open = [];
            }

            $groups[] = [$entry];
        }

        if ($open !== []) {
            $groups[] = $open;
        }

        return $groups;
    }

    private function carriable(PreparedWrite $write): bool
    {
        return match ($write->result) {
            ResultKind::None, ResultKind::Key => true,
            ResultKind::Count => $this->countsBySelect,
            ResultKind::CommandCount => false,
        };
    }

    /** One statement on its own round trip: execute, read what it returns, complete. */
    private function runOne(PreparedWrite $write): int
    {
        $statement = $this->statementFor($write->sql);
        PdoBinder::bindAndExecute($statement, $write->parameters);

        return $write->complete(ResultReader::read($statement, $write->result, $this->countsBySelect));
    }

    /**
     * Several statements as one text and one round trip, their results read in order (SQL Server).
     *
     * @param list<array{0: int, 1: PreparedWrite}> $group
     * @param array<int, int> $affected
     */
    private function runGroup(array $group, array &$affected): void
    {
        [$sql, $parameters] = MessageText::render(array_map(static fn (array $entry): PreparedWrite => $entry[1], $group));
        $statement = $this->connection->prepare($sql);
        PdoBinder::bindAndExecute($statement, $parameters);

        $reader = new ResultReader($statement);
        try {
            foreach ($group as [$index, $write]) {
                $result = match ($write->result) {
                    ResultKind::None => null,
                    ResultKind::Count => ResultReader::reportedCount($reader->scalar()),
                    default => $reader->scalar(),
                };
                $affected[$index] = $write->complete($result);
            }

            $reader->drain();   // an error in a statement whose result nobody reads must still fail the call
        } finally {
            $reader->finish();   // a failure midway must not leave results pending on the connection
        }
    }

    /** The PDO statement for a text: prepared once per distinct text within the call under `Auto`, fresh every time under `Off`. */
    private function statementFor(string $sql): PDOStatement
    {
        $text = SqlPlaceholders::toPdo($sql);
        if ($this->mode === BatchingMode::Off) {
            return $this->connection->prepare($text);
        }

        return $this->statements[$text] ??= $this->connection->prepare($text);
    }
}
