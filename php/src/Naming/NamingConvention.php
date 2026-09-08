<?php

declare(strict_types=1);

namespace SimpleOrm\Naming;

/**
 * Translates language-side names to database names wherever a name is derived
 * rather than explicit (spec/metadata-model.md "Naming convention"). An explicit
 * name always bypasses it. Pluggable via `MappingOptions`.
 */
interface NamingConvention
{
    /** A property name → column name, or a class short name → relation name (never pluralized). */
    public function toDatabase(string $name): string;
}
