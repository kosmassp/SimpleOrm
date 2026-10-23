<?php

declare(strict_types=1);

namespace SimpleOrm\Session;

use Closure;

/**
 * One operation of a batched call (ADR-0044): what an explicit batch queued or the unit of work derived from its
 * schedule, held as closures so that **nothing runs until the call does** — queuing does no I/O, binds no value, and
 * checks nothing (spec/session.md "The explicit batch"). {@see WriteRun} asks it, in order, for the dependencies
 * the plan's rule 1 reads (the instance, the key it stands on, the instances it points at), for the journal entry that
 * makes its in-memory effects undoable, and for its {@see PreparedWrite} — null when the operation turns out to issue
 * no statement (a clean tracked update).
 *
 * @internal
 */
final readonly class WriteOperation
{
    /**
     * @param object|null $instance the entity the operation writes (plan rule 1a); null for a registry command or a by-key delete
     * @param Closure(): ?string $identity the (entity type, key) the operation stands on **as it is now, before its
     *        preparation** — null when the key is not set or a by-key delete's argument names none (plan rule 1a's key clause)
     * @param Closure(): list<object> $parents the instances this operation's instance depends on — the targets of its
     *        non-null many-to-one navigations and, in a flush, the cascade owners it takes its key from (plan rule 1b)
     * @param Closure(FlushJournal): void $record remembers what preparing and completing may move, before it moves
     * @param Closure(): ?PreparedWrite $prepare
     */
    public function __construct(
        public ?object $instance,
        private Closure $identity,
        private Closure $parents,
        private Closure $record,
        private Closure $prepare,
    ) {
    }

    public function identity(): ?string
    {
        return ($this->identity)();
    }

    /** @return list<object> */
    public function parents(): array
    {
        return ($this->parents)();
    }

    public function record(FlushJournal $journal): void
    {
        ($this->record)($journal);
    }

    public function prepare(): ?PreparedWrite
    {
        return ($this->prepare)();
    }
}
