<?php

declare(strict_types=1);

namespace SimpleOrm\Metadata;

use ReflectionClass;

/**
 * The single entry point for metadata (§7.2). Loader precedence per type: an
 * explicit registration in `MappingOptions::$explicitMaps`, else the attribute
 * loader when any mapping attribute is present, else the convention loader.
 * Maps are cached per instance (CODING-STANDARD §3: no static mutable state —
 * the cache lives here, not in a global). Other areas (session, migrations,
 * validation) call exactly this API.
 */
final class EntityMapLoader
{
    /** @var array<class-string, EntityMap> */
    private array $cache = [];

    private readonly MappingOptions $options;

    public function __construct(?MappingOptions $options = null)
    {
        $this->options = $options ?? MappingOptions::default();
    }

    /** @param class-string $entityType */
    public function load(string $entityType): EntityMap
    {
        return $this->cache[$entityType] ??= $this->loadCore($entityType);
    }

    /** Whether the type carries any mapping attributes (used by tooling such as export-metadata). */
    public static function hasMappingAttributes(string $entityType): bool
    {
        return AttributeMapLoader::hasMappingAttributes(new ReflectionClass($entityType));
    }

    /** @param class-string $entityType */
    private function loadCore(string $entityType): EntityMap
    {
        if (isset($this->options->explicitMaps[$entityType])) {
            return $this->options->explicitMaps[$entityType];
        }

        return self::hasMappingAttributes($entityType)
            ? AttributeMapLoader::load($entityType, $this->options->naming)
            : ConventionMapLoader::load($entityType, $this->options->naming);
    }
}
