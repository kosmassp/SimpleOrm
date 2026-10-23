<?php

declare(strict_types=1);

namespace SimpleOrm\Session;

use SimpleOrm\Errors\SimpleOrmException;

/**
 * The explicit batch (Level 4, ADR-0044, spec/session.md "The explicit batch"): a queue of writes and registry commands
 * bound to a session, sent together by {@see self::send()} — the visible round trip(s).
 *
 * ```php
 * $batch = $db->batch();                       // queuing does no I/O
 * $batch->insert($order);
 * $batch->updateOnly($user, ['name']);
 * $batch->execute(Commands::markShipped(), new MarkShippedArgs(id: 7));
 * $batch->delete(Role::class, 3);
 * $result = $batch->send();                    // BatchResult: ->affected per operation, ->messages
 * ```
 *
 * Queuing binds no value and checks nothing but the batch's own state: whatever the immediate call would refuse,
 * `send()` refuses, at that operation's turn, exactly as the immediate call would at that point of the sequence — keys do
 * not flow between queued operations (the unit of work is the form that flows keys). `send()` always runs in a
 * transaction (the session's, else one it begins and commits after the last message) and fails as the plan fails
 * (`Db::runCall()`); the batch is spent by it, sent or failed. A batch that was sent or disposed takes nothing more:
 * queuing on it or sending it again is `TX-003`; one disposed unsent is dropped silently — nothing is ever sent
 * implicitly. Under `BatchingMode::Off` it behaves the same, one statement per message. Reads are not batched.
 *
 * PHP names the operations as the immediate calls do (`insert`, `update`, `updateOnly`, `delete`, `execute`;
 * CODING-STANDARD §10) and `dispose()` stands in for the reference's `DisposeAsync`.
 */
final class DbBatch
{
    private bool $spent = false;

    /** @var list<WriteOperation> */
    private array $operations = [];

    /** @internal created by {@see Db::batch()} */
    public function __construct(private readonly Db $db)
    {
    }

    public function insert(object $entity): void
    {
        $this->queue($this->db->insertOperation($entity));
    }

    /** The full-row update; on a tracked session a clean instance issues no statement and counts 0. */
    public function update(object $entity): void
    {
        $this->queue($this->db->updateOperation($entity));
    }

    /** @param list<string> $properties */
    public function updateOnly(object $entity, array $properties): void
    {
        $this->queue($this->db->updateOnlyOperation($entity, $properties));
    }

    /** The key or the entity form, dispatched as `Db::delete()` dispatches it. */
    public function delete(string $entityType, mixed $keyOrEntity): void
    {
        $this->queue($this->db->deleteOperation($entityType, $keyOrEntity));
    }

    /** A registry command and its args; its own count is the driver's. */
    public function execute(Command $command, object $args): void
    {
        $this->queue($this->db->commandOperation($command, $args));
    }

    /**
     * Sends the queued operations in queue order, as messages by the plan, and spends the batch either way. An empty
     * send returns an empty result and begins no transaction. `TX-003` when the batch was already sent or disposed.
     */
    public function send(): BatchResult
    {
        $this->requireOpen();
        $this->spent = true;
        $operations = $this->operations;
        $this->operations = [];

        return $this->db->runCall($operations);
    }

    /** Drops the queue unsent and spends the batch. */
    public function dispose(): void
    {
        $this->spent = true;
        $this->operations = [];
    }

    private function queue(WriteOperation $operation): void
    {
        $this->requireOpen();
        $this->operations[] = $operation;
    }

    private function requireOpen(): void
    {
        if ($this->spent) {
            throw new SimpleOrmException(
                'TX-003',
                'batch',
                'the batch was already sent or disposed; a batch is spent by its send — start another with $db->batch()',
            );
        }
    }
}
