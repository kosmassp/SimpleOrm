<?php

declare(strict_types=1);

namespace SimpleOrm\Session;

/**
 * The session's statement-batching option, chosen at open (Level 4, ADR-0044, spec/session.md "Batching"): `Off` — the
 * default — is the Level 0–3 behaviour exactly, one message per statement; `Auto` forms messages by the plan of
 * {@see WriteRun} for the statements of one flush or one explicit batch. Whether a planned message reaches the
 * database as one round trip is the dialect's capability (`Db::batchingInEffect()`); where it does not, the same plan
 * runs its statements one at a time.
 */
enum BatchingMode: string
{
    case Off = 'off';
    case Auto = 'auto';
}
