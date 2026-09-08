<?php

declare(strict_types=1);

namespace SimpleOrm\Session;

/**
 * A transaction scope on one session (§7.17). Commit explicitly; an uncommitted
 * scope rolls back — on an explicit {@see rollback()}, when the scope is
 * garbage-collected still uncommitted, and (belt and suspenders, mirroring
 * dotnet's `Db.DisposeAsync`) when the owning {@see Db} closes with a
 * transaction still active.
 */
final class DbTransactionScope
{
    private bool $completed = false;

    public function __construct(private readonly Db $db)
    {
    }

    public function commit(): void
    {
        $this->db->commitTransaction();
        $this->completed = true;
    }

    public function rollback(): void
    {
        $this->db->rollbackTransaction();
        $this->completed = true;
    }

    public function __destruct()
    {
        if (!$this->completed) {
            $this->db->rollbackTransaction();
            $this->completed = true;
        }
    }
}
