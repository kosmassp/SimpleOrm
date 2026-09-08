<?php

declare(strict_types=1);

namespace SimpleOrm\Metadata;

use SimpleOrm\Naming\NamingConvention;
use SimpleOrm\Naming\SnakeCaseNamingConvention;

/**
 * Configuration for metadata loading (§7.2): the naming convention (default
 * snake_case) and explicit registrations, which take precedence over
 * attributes and conventions (explicit → attribute → convention). Unlike the
 * C# reference's mutable `MappingOptions.Register<T>` (a deferred
 * `EntityMapBuilder<T>.Build` call), `$explicitMaps` holds already-built
 * {@see EntityMap} instances — PHP's `EntityMapBuilder::build()` takes no
 * convention argument, so building eagerly needs no deferral.
 */
final readonly class MappingOptions
{
    /** @param array<class-string, EntityMap> $explicitMaps */
    public function __construct(
        public NamingConvention $naming = new SnakeCaseNamingConvention(),
        public array $explicitMaps = [],
    ) {
    }

    /** Shared default options: snake_case, no explicit registrations. */
    public static function default(): self
    {
        return new self();
    }
}
