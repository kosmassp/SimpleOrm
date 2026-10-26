<?php

declare(strict_types=1);

namespace SimpleOrm\Dialect;

use PDO;
use SimpleOrm\Migrations\TriggerSchema;
use SimpleOrm\Session\StatementLog;

/**
 * The SQLite {@see ShadowHost} (ADR-0043): the throwaway database is a temp file — nothing to provision on an admin
 * connection, which is ignored — deleted afterwards, and SQLite's `sqlite_master` keeps a trigger as submitted, so the
 * trigger read-back is lossless ({@see SqliteTriggerParser}).
 */
final class SqliteShadowHost implements ShadowHost
{
    public function provision(string $adminConnectionString): string
    {
        return sys_get_temp_dir() . '/simpleorm-shadow-' . bin2hex(random_bytes(8)) . '.db';
    }

    public function drop(string $adminConnectionString, string $shadowConnectionString): void
    {
        foreach ([$shadowConnectionString, $shadowConnectionString . '-wal', $shadowConnectionString . '-shm', $shadowConnectionString . '-journal'] as $file) {
            self::tryDelete($file);
        }
    }

    public function canReadTriggers(): bool
    {
        return true;
    }

    public function readTrigger(PDO $connection, string $name, ?string &$note = null, ?StatementLog $log = null): ?TriggerSchema
    {
        return SqliteTriggerParser::parse($name, SqliteTriggerParser::definitions($connection, $name, $log), $note);
    }

    /** Best-effort cleanup of the throwaway shadow database: Windows can briefly hold a delete lock after SQLite closes its handle, so a failure here is not reported (never `@` — the warning is caught and discarded explicitly, scoped to this one call). */
    private static function tryDelete(string $path): void
    {
        if (!is_file($path)) {
            return;
        }

        set_error_handler(static fn (): bool => true);
        try {
            unlink($path);
        } finally {
            restore_error_handler();
        }
    }
}
