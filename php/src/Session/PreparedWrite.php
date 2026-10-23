<?php

declare(strict_types=1);

namespace SimpleOrm\Session;

use Closure;

/**
 * One statement ready to go out (ADR-0044): the prepare half of a write — every gate passed, the hooks that fill or
 * stamp applied to the instance, the SQL rendered (`@name` placeholders) and its values converted — split from the two
 * halves that need the database: {@see self::complete()} judges and applies the statement's result exactly as the
 * immediate operation always did (key written back, zero rows `CRUD-010`/`CRUD-001`, version bumped, the identity map
 * and snapshots moved) and {@see self::undo()} puts back what preparing and completing moved on the instance.
 *
 * The immediate operations, the unit of work's flush, and the explicit batch all go through this one shape
 * (CODING-STANDARD §8): they differ only in how many prepared statements share a message ({@see WriteRun}).
 *
 * @internal
 */
final readonly class PreparedWrite
{
    /**
     * @param array<string, mixed> $parameters keys without the leading `@`
     * @param Closure(mixed): int $complete given the statement's result (the key, the count, or null), judges and
     *        applies it and returns the affected count the explicit batch reports
     * @param Closure(): void $undo restores the instance state this statement's prepare and completion moved
     * @param object|null $instance the entity the statement writes (plan rule 1a); null for a registry command or a by-key delete
     * @param bool $insertsGeneratedKey an insert whose key the database generates — what a dependent statement waits for (plan rule 1b)
     * @param string|null $identity the (entity type, key) the statement writes, taken as the statement was prepared, when that
     *        key is set (plan rule 1a's key clause; {@see Db::writeIdentity()}); null for a registry command or an unset key
     */
    public function __construct(
        public string $sql,
        public array $parameters,
        public ResultKind $result,
        private Closure $complete,
        private Closure $undo,
        public ?object $instance = null,
        public bool $insertsGeneratedKey = false,
        public ?string $identity = null,
    ) {
    }

    public function complete(mixed $result): int
    {
        return ($this->complete)($result);
    }

    public function undo(): void
    {
        ($this->undo)();
    }

    /** Whether the session reads a result of this statement (plan rule 2): a generated key, or a count the dialect reports by select. */
    public function readsResult(bool $countsBySelect): bool
    {
        return match ($this->result) {
            ResultKind::Key => true,
            ResultKind::Count => $countsBySelect,
            ResultKind::CommandCount, ResultKind::None => false,
        };
    }

    /** The bound parameters the statement adds to a message (plan rule 3). */
    public function parameterCount(): int
    {
        return count($this->parameters);
    }
}
