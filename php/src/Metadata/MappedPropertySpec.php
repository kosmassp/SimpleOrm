<?php

declare(strict_types=1);

namespace SimpleOrm\Metadata;

use ReflectionProperty;

/**
 * Loader-internal working shape of one mapped property before assembly
 * (§7.2; mirrors the C# reference's `MappedPropertySpec`): the loaders (attribute,
 * convention, manual builder) each resolve a property's neutral type and
 * declared flags into one of these; {@see MapAssembler} resolves the column
 * name and turns it into a {@see PropertyMap}. `$length`/`$precision`/`$scale` are the facets exactly as declared
 * (ADR-0043): the assembler validates them (`MAP-032`) before they become the property's resolved facets.
 */
final readonly class MappedPropertySpec
{
    public function __construct(
        public ReflectionProperty $property,
        public ColumnType $type,
        public ?string $phpType,
        public bool $nullable,
        public ?string $explicitColumn = null,
        public bool $isKey = false,
        public bool $isGenerated = false,
        public bool $isVersion = false,
        public ?string $foreignKeyReferences = null,
        public ?OwnedSpec $owner = null,
        public AuditRole $audit = AuditRole::None,
        public bool $isSoftDelete = false,
        public ?string $renamedFrom = null,
        public ?int $length = null,
        public ?int $precision = null,
        public ?int $scale = null,
    ) {
    }
}
