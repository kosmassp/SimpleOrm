<?php

declare(strict_types=1);

namespace SimpleOrm\Errors;

/** One SchemaGuard finding (§7.19): a `VAL-`/`PRM-`/`MAP-`/`MIG-` code, the query or entity at fault, and the reason. */
final readonly class ValidationError
{
    public function __construct(
        public string $code,
        public string $target,
        public string $message,
    ) {
    }

    public function __toString(): string
    {
        return "{$this->code} {$this->target}: {$this->message}";
    }
}
