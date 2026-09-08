<?php

declare(strict_types=1);

namespace SimpleOrm\Errors;

use RuntimeException;

/**
 * A failure with a stable error code from spec/errors.md (§2 "errors name
 * things"): the code is the cross-language contract, the target is the thing at
 * fault — a query, `Type.property`, a parameter, the session — and the message
 * says what was expected. Reads as "<code> <target>: <message>".
 *
 * Not final: ConcurrencyException and MappingException specialize it.
 */
class SimpleOrmException extends RuntimeException
{
    public function __construct(
        public readonly string $errorCode,
        public readonly string $target,
        string $message,
    ) {
        parent::__construct("{$errorCode} {$target}: {$message}");
    }
}
