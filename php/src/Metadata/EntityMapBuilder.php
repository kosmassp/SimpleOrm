<?php

declare(strict_types=1);

namespace SimpleOrm\Metadata;

use ReflectionProperty;
use SimpleOrm\Errors\MappingException;
use SimpleOrm\Naming\NamingConvention;
use SimpleOrm\Naming\SnakeCaseNamingConvention;

/**
 * The manual loader (§7.2): a fluent map for types you can't or won't
 * annotate, mirroring the C# reference's `EntityMapBuilder<T>` PHP-ized —
 * property names are strings (PHP has no expression trees) and named
 * arguments replace the reference's chained `.Key().Generated()` calls.
 * Mapping stays opt-in: only properties named via {@see self::column()} are
 * mapped. Runs the same validations as the attribute loader, through the
 * shared {@see MapAssembler}.
 */
final class EntityMapBuilder
{
    /** @var list<MappedPropertySpec> */
    private array $specs = [];

    private ?string $tableName = null;

    private ?string $schema = null;

    private readonly NamingConvention $naming;

    /** @param class-string $entityType */
    private function __construct(
        private readonly string $entityType,
        ?NamingConvention $naming,
    ) {
        $this->naming = $naming ?? new SnakeCaseNamingConvention();
    }

    /**
     * @param class-string $entityType
     * @param NamingConvention|null $naming derives the table name when {@see self::table()} is never
     *        called, and column names when {@see self::column()}'s `$name` is omitted; default snake_case
     */
    public static function for(string $entityType, ?NamingConvention $naming = null): self
    {
        return new self($entityType, $naming);
    }

    /** Sets the table name; when never called, the naming convention derives it from the class short name. */
    public function table(string $name, ?string $schema = null): self
    {
        $this->tableName = $name;
        $this->schema = $schema;

        return $this;
    }

    /** Maps one property; `$name` overrides the convention-derived column name. */
    public function column(
        string $property,
        ?string $name = null,
        bool $key = false,
        bool $generated = false,
        bool $version = false,
        ?ColumnType $type = null,
        bool $enumAsInt = false,
    ): self {
        $reflection = new ReflectionProperty($this->entityType, $property);
        $resolved = PropertyTypes::resolve($reflection, $type, $enumAsInt);
        $this->specs[] = new MappedPropertySpec(
            $reflection,
            $resolved->type,
            $resolved->phpType,
            $resolved->nullable,
            explicitColumn: $name,
            isKey: $key,
            isGenerated: $generated,
            isVersion: $version,
        );

        return $this;
    }

    public function build(): EntityMap
    {
        $errors = [];
        $map = MapAssembler::assemble(
            $this->entityType,
            RelationKind::Table,
            $this->tableName ?? $this->naming->toDatabase(self::shortName($this->entityType)),
            $this->schema,
            statementSql: null,
            statementParameters: [],
            specs: $this->specs,
            indexSpecs: [],
            relationshipSpecs: [],
            convention: $this->naming,
            errors: $errors,
        );

        if ($map === null) {
            throw new MappingException($this->entityType, $errors);
        }

        return $map;
    }

    private static function shortName(string $fqcn): string
    {
        $slash = strrpos($fqcn, '\\');

        return $slash === false ? $fqcn : substr($fqcn, $slash + 1);
    }
}
