<?php

declare(strict_types=1);

namespace SimpleOrm\Session;

/**
 * The session's tracking mode, chosen at open (ADR-0034 add.1, spec/session.md "Tracking"):
 * `Untracked` — the default — is the Level 0–2 session exactly (fresh instances, full-row
 * update); `Tracked` adds the identity map and change snapshots ({@see DbTracking}).
 */
enum TrackingMode
{
    case Untracked;
    case Tracked;
}
