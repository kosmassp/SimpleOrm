<?php

declare(strict_types=1);

namespace SimpleOrm\Session;

use function count;
use function spl_object_id;

/**
 * The message {@see WriteRun} is forming (ADR-0044, spec/session.md "Batching"): the prepared statements it holds in
 * order, with the facts the plan's rules ask about — which instances it holds a statement on (rule 1a), which
 * instances it inserts with a database-generated key (rule 1b), which (entity type, key) identities its statements write
 * (rule 1a's key clause), whether it holds a registry command (rule 2), and how many parameters it carries (rule 3).
 *
 * @internal
 */
final class PlannedMessage
{
    /** @var list<array{0: int, 1: PreparedWrite}> operation index and statement */
    private array $entries = [];

    /** @var array<int, true> */
    private array $instances = [];

    /** @var array<int, true> */
    private array $generatedInserts = [];

    /** @var array<string, true> */
    private array $identities = [];

    private bool $holdsCommand = false;

    private int $parameters = 0;

    public function isEmpty(): bool
    {
        return $this->entries === [];
    }

    public function count(): int
    {
        return count($this->entries);
    }

    public function parameterCount(): int
    {
        return $this->parameters;
    }

    public function holdsCommand(): bool
    {
        return $this->holdsCommand;
    }

    /** Rule 1a: the message already holds a statement on this very instance. */
    public function holdsStatementOn(object $instance): bool
    {
        return isset($this->instances[spl_object_id($instance)]);
    }

    /** Rule 1a's key clause: the message holds a statement on an instance of this type with this set key. */
    public function holdsStatementWithIdentity(string $identity): bool
    {
        return isset($this->identities[$identity]);
    }

    /** Rule 1b: the message inserts this instance and the database generates its key. */
    public function insertsGeneratedKeyOf(object $instance): bool
    {
        return isset($this->generatedInserts[spl_object_id($instance)]);
    }

    public function add(int $index, PreparedWrite $write): void
    {
        $this->entries[] = [$index, $write];
        $this->parameters += $write->parameterCount();
        $this->holdsCommand = $this->holdsCommand || $write->result === ResultKind::CommandCount;
        if ($write->identity !== null) {
            $this->identities[$write->identity] = true;
        }

        if ($write->instance !== null) {
            $this->instances[spl_object_id($write->instance)] = true;
            if ($write->insertsGeneratedKey) {
                $this->generatedInserts[spl_object_id($write->instance)] = true;
            }
        }
    }

    /** @return list<array{0: int, 1: PreparedWrite}> */
    public function entries(): array
    {
        return $this->entries;
    }

    public function clear(): void
    {
        $this->entries = [];
        $this->instances = [];
        $this->generatedInserts = [];
        $this->identities = [];
        $this->holdsCommand = false;
        $this->parameters = 0;
    }
}
