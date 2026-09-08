<?php

declare(strict_types=1);

namespace SimpleOrm\Sample\Models\Tables;

/** Stored as TEXT by case name, matched case-insensitively on read (§7.9); a pure enum, so the name is the value. */
enum TransactionStatus
{
    case Pending;
    case Completed;
    case Cancelled;
}
