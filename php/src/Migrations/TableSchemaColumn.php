<?php

declare(strict_types=1);

namespace SimpleOrm\Migrations;

/** One column of a `TableSchema`, in the dialect's storage type (ADR-0017). */
final readonly class TableSchemaColumn
{
    public function __construct(
        public string $name,
        public string $storageType,
        public bool $nullable,
        public bool $key = false,
        public bool $generated = false,
    ) {
    }
}
