<?php

declare(strict_types=1);

namespace SimpleOrm\Dialect;

use PDO;
use SimpleOrm\Migrations\TriggerSchema;
use SimpleOrm\Session\StatementLog;

/**
 * The tooling contract beside the dialect seam that gives `shadow` its throwaway database (ADR-0043; the C#
 * `IShadowHost`, spec/dialects.md "Migration tooling"). Never consulted at runtime — only `SchemaShadow` asks for it.
 * A host *provisions* a database from the admin connection and returns the connection string that opens it, *drops* it
 * afterwards (the caller does so on failure too), and — where the dialect's catalog hands a trigger back losslessly —
 * *reads a trigger back* to its neutral declaration for the shadow's self-check.
 */
interface ShadowHost
{
    /**
     * Creates the throwaway database and returns the connection string that opens it. `$adminConnectionString` is the
     * dialect's native connection string with the right to create databases (ignored by SQLite, which uses a temp file).
     */
    public function provision(string $adminConnectionString): string;

    /** Drops what {@see self::provision()} made; called with the same admin connection string and the one provision returned. */
    public function drop(string $adminConnectionString, string $shadowConnectionString): void;

    /** Whether {@see self::readTrigger()} exists for this dialect (SQLite, PostgreSQL; not SQL Server, whose `inserted` has lost a token's column list). */
    public function canReadTriggers(): bool;

    /**
     * The inverse of `Dialect::createTriggerSql()` over the catalog's text: the declaration the stored trigger `$name`
     * renders, or null — with `$note` set when statements exist but do not match the dialect's grammar (hand-written?),
     * absent: null with no note. The catalog read goes to `$log` (ADR-0045) when given.
     */
    public function readTrigger(PDO $connection, string $name, ?string &$note = null, ?StatementLog $log = null): ?TriggerSchema;
}
