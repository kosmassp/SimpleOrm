<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Sample\Models;

/** Stored as TEXT by case name, matched case-insensitively (§7.9); a pure enum, so the name is the value. */
enum TransactionStatus
{
    case Pending;
    case Completed;
    case Cancelled;
}
