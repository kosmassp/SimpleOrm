<?php

declare(strict_types=1);

namespace SimpleOrm\Errors;

use RuntimeException;

/**
 * The complete SchemaGuard report (§7.20): every violation across every
 * registered query and entity, never the first one only. Thrown once at
 * startup; the message is the file-by-file report.
 */
final class SchemaValidationException extends RuntimeException
{
    /** @param non-empty-list<ValidationError> $errors */
    public function __construct(public readonly array $errors)
    {
        parent::__construct(
            count($errors) . " schema validation error(s):\n  " . implode("\n  ", array_map('strval', $errors)),
        );
    }
}
