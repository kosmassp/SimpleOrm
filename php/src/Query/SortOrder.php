<?php

declare(strict_types=1);

namespace SimpleOrm\Query;

/** Ordering direction: an ORDER BY term's, and an `#[Index]` token (ADR-0007 add.3). */
enum SortOrder: string
{
    case Asc = 'asc';
    case Desc = 'desc';
}
