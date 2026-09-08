<?php

declare(strict_types=1);

namespace SimpleOrm\Migrations;

/** The state of one recorded (version, object) pair (§7.23), as reported by `MigrationRunner::status()`. */
enum MigrationState: string
{
    /** Declared in code, not yet recorded as applied. */
    case Pending = 'pending';

    /** Recorded, and the checksum matches the current rendering. */
    case Applied = 'applied';

    /** Recorded under this version, but no matching row — or a checksum mismatch (`MIG-010`). */
    case Drifted = 'drifted';

    /** Recorded in the database under a version the code no longer declares (`MIG-011`). */
    case Unknown = 'unknown';
}
