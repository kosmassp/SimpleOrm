<?php

declare(strict_types=1);

namespace SimpleOrm\Metadata;

use ReflectionClass;
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

    /** @var list<string> the linked trigger classes (Level 4, ADR-0042) */
    private array $triggerLinks = [];

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
        BaseClassRules::assertConcrete($entityType);   // MAP-028 at registration time (ADR-0041)
        TriggerRules::assertNotTriggerClass($entityType);   // MAP-029: a trigger class is a declaration object (ADR-0042)

        return new self($entityType, $naming);
    }

    /**
     * Links a trigger class (Level 4, ADR-0042) — the builder's `#[Trigger(X::class)]`, under the same `MAP-029`
     * rules the attribute loader enforces when {@see self::build()} assembles the map. A table takes any timing, a
     * view only `InsteadOf`.
     *
     * @param class-string $triggerClass
     */
    public function trigger(string $triggerClass): self
    {
        $this->triggerLinks[] = $triggerClass;

        return $this;
    }

    /** Sets the table name; when never called, the naming convention derives it from the class short name. */
    public function table(string $name, ?string $schema = null): self
    {
        $this->tableName = $name;
        $this->schema = $schema;

        return $this;
    }

    /**
     * Maps one property; `$name` overrides the convention-derived column name. `$audit` and `$softDelete`
     * declare the hooks (Level 3 M3, spec/hooks.md) the attributes `#[CreatedAt]`, `#[UpdatedAt]`, and
     * `#[SoftDelete]` declare on annotated types — a property carries at most one (`MAP-026`).
     * `$renamedFrom` declares `#[RenamedFrom]` (Level 3 M5); an empty name is `MAP-027`. `$length` and
     * `$precision` + `$scale` are the storage facets `#[Column(length:, precision:, scale:)]` declares (ADR-0043),
     * under the same `MAP-032` rules.
     */
    public function column(
        string $property,
        ?string $name = null,
        bool $key = false,
        bool $generated = false,
        bool $version = false,
        ?ColumnType $type = null,
        bool $enumAsInt = false,
        AuditRole $audit = AuditRole::None,
        bool $softDelete = false,
        ?string $renamedFrom = null,
        ?int $length = null,
        ?int $precision = null,
        ?int $scale = null,
    ): self {
        // The builder declares hooks like the attribute loader (spec/hooks.md Clarifications): the same MAP-026 rules, run by
        // the one assembler. The convention loader (no markers at all) cannot declare hooks.
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
            audit: $audit,
            isSoftDelete: $softDelete,
            renamedFrom: $renamedFrom,
            length: $length,
            precision: $precision,
            scale: $scale,
        );

        return $this;
    }

    public function build(): EntityMap
    {
        $errors = BaseClassRules::parentDeclarationErrors(new ReflectionClass($this->entityType));   // MAP-028 (ADR-0041)
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
            triggerLinks: $this->triggerLinks,
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
