<?php

declare(strict_types=1);

namespace SimpleOrm\Metadata;

/** How key values come to exist (§7.14). The backing value is the export token. */
enum KeyStrategy: string
{
    /** No key declared (statements, procedures, keyless views). */
    case None = 'none';

    /** The database generates the key (`INTEGER PRIMARY KEY`); inserts read it back via RETURNING. */
    case DatabaseGenerated = 'database_generated';

    /** The client supplies a GUID before insert. */
    case ClientGuid = 'client_guid';

    /** The caller supplies natural or composite key values. */
    case Natural = 'natural';
}
