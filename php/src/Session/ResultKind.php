<?php

declare(strict_types=1);

namespace SimpleOrm\Session;

/**
 * What a statement of a planned message returns that the session reads (ADR-0044, spec/dialects.md "Reading results"),
 * which is also what plan rule 2 asks about: `Key` — the generated key of an insert (`returning`, SQL Server's
 * `scope_identity()` scalar); `Count` — the judged affected-row count of a generated update or delete (the driver's
 * `rowCount()`, or the dialect's reported scalar under `reportsAffectedRowsBySelect`); `CommandCount` — a registry
 * command's driver count; `None` — an insert whose key the caller supplies.
 */
enum ResultKind
{
    case None;
    case Key;
    case Count;
    case CommandCount;
}
