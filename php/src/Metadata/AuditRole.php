<?php

declare(strict_types=1);

namespace SimpleOrm\Metadata;

/**
 * The audit hook a column may carry (Level 3 M3, spec/hooks.md): `CreatedAt` is filled on insert when
 * unset, `UpdatedAt` is stamped by every write that changes a row — both with the session clock. The
 * backing value is the export token (`"audit": "created_at" | "updated_at"`, spec/metadata-model.md);
 * `None` is a column with no audit hook and is never exported.
 */
enum AuditRole: string
{
    case None = 'none';
    case CreatedAt = 'created_at';
    case UpdatedAt = 'updated_at';
}
