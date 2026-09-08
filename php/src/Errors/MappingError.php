<?php

declare(strict_types=1);

namespace SimpleOrm\Errors;

/**
 * One violation found while loading an EntityMap (spec/metadata-model.md): a
 * stable `MAP-`/`PRM-` code, the member or artifact at fault, and what was
 * expected. Loaders collect every violation for a type before failing.
 */
final readonly class MappingError
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
