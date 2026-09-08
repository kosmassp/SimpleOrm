<?php

declare(strict_types=1);

namespace SimpleOrm\Metadata;

use ReflectionClass;
use ReflectionNamedType;
use ReflectionProperty;
use SimpleOrm\Errors\MappingError;
use SimpleOrm\Errors\MappingException;
use SimpleOrm\Metadata\Attributes\Column;
use SimpleOrm\Metadata\Attributes\EnumAsInt;
use SimpleOrm\Metadata\Attributes\ForeignKey;
use SimpleOrm\Metadata\Attributes\Generated;
use SimpleOrm\Metadata\Attributes\Ignore;
use SimpleOrm\Metadata\Attributes\Index;
use SimpleOrm\Metadata\Attributes\Key;
use SimpleOrm\Metadata\Attributes\ManyToMany;
use SimpleOrm\Metadata\Attributes\ManyToOne;
use SimpleOrm\Metadata\Attributes\MaterializedView;
use SimpleOrm\Metadata\Attributes\OneToMany;
use SimpleOrm\Metadata\Attributes\OneToOne;
use SimpleOrm\Metadata\Attributes\Owned;
use SimpleOrm\Metadata\Attributes\Procedure;
use SimpleOrm\Metadata\Attributes\Statement;
use SimpleOrm\Metadata\Attributes\Table;
use SimpleOrm\Metadata\Attributes\Version;
use SimpleOrm\Metadata\Attributes\View;
use SimpleOrm\Naming\NamingConvention;
use SimpleOrm\Parameters\SqlPlaceholders;
use SimpleOrm\Query\SortOrder;

/**
 * The attribute loader (ADR-0004..0008, ADR-0019): enforces the opt-in mapping
 * rules and produces an {@see EntityMap} through {@see MapAssembler}. Collects
 * every violation before throwing (spec/metadata-model.md). Internal to the
 * loader pipeline — {@see EntityMapLoader} is the public entry point.
 */
final class AttributeMapLoader
{
    private function __construct()
    {
    }

    /** @param class-string $entityType */
    public static function load(string $entityType, NamingConvention $convention): EntityMap
    {
        $class = new ReflectionClass($entityType);
        $errors = [];
        if (self::isOwnedType($class)) {
            // An owned value type (ADR-0030) has no map of its own; it is read
            // through its owner. Loading it directly is a caller error.
            throw new MappingException($entityType, [new MappingError(
                'MAP-024',
                $class->getShortName(),
                'is an #[Owned] value type, not an entity; it maps only as a member of its owner',
            )]);
        }

        [$kind, $relationName, $schema, $statementSql, $statementParameters] =
            self::readRelationSource($class, $convention, $errors);

        $specs = [];
        $relationships = [];
        self::readProperties($class, $kind, $specs, $relationships, $errors);

        $indexSpecs = self::readIndexes($class, $kind, $errors);
        if ($statementSql !== null) {
            // For views the declared list is empty, so any placeholder in the
            // defining SELECT is PRM-010 — view definitions take no parameters.
            self::checkStatementPlaceholders($class, $kind, $statementSql, $statementParameters, $errors);
        }

        $map = MapAssembler::assemble(
            $entityType,
            $kind,
            $relationName,
            $schema,
            $statementSql,
            $statementParameters,
            $specs,
            $indexSpecs,
            $relationships,
            $convention,
            $errors,
        );

        if ($map === null) {
            throw new MappingException($entityType, $errors);
        }

        return $map;
    }

    /**
     * @param ReflectionClass<object> $class
     * @param list<MappingError> $errors
     * @return array{0: RelationKind, 1: ?string, 2: ?string, 3: ?string, 4: list<StatementParameter>}
     */
    private static function readRelationSource(ReflectionClass $class, NamingConvention $convention, array &$errors): array
    {
        /** @var list<array{0: RelationKind, 1: ?string, 2: ?string, 3: ?string, 4: mixed}> $sources */
        $sources = [];

        if (($table = self::attr($class, Table::class)) !== null) {
            $sources[] = [RelationKind::Table, $table->name, $table->schema, null, null];
        }

        if (($view = self::attr($class, View::class)) !== null) {
            $sources[] = [RelationKind::View, $view->name, $view->schema, $view->sql, null];
        }

        if (($materialized = self::attr($class, MaterializedView::class)) !== null) {
            $sources[] = [RelationKind::MaterializedView, $materialized->name, $materialized->schema, $materialized->sql, null];
        }

        if (($statement = self::attr($class, Statement::class)) !== null) {
            $sources[] = [RelationKind::Statement, null, null, $statement->sql, $statement->parameters];
        }

        if (($procedure = self::attr($class, Procedure::class)) !== null) {
            $sources[] = [RelationKind::Procedure, $procedure->name, $procedure->schema, $procedure->sql, $procedure->parameters];
        }

        if (count($sources) > 1) {
            $errors[] = new MappingError(
                'MAP-012',
                $class->getShortName(),
                'carries ' . count($sources) . ' relation sources; exactly one of '
                    . '#[Table]/#[View]/#[MaterializedView]/#[Statement]/#[Procedure] is allowed',
            );
        }

        if ($sources === []) {
            // Property attributes without a source attribute: a table named by convention.
            return [RelationKind::Table, $convention->toDatabase($class->getShortName()), null, null, []];
        }

        [$kind, $name, $schema, $sql, $rawParameters] = $sources[0];
        $parameters = $kind === RelationKind::Statement || $kind === RelationKind::Procedure
            ? self::parseStatementParameters($class, is_array($rawParameters) ? $rawParameters : [], $errors)
            : [];

        if ($kind !== RelationKind::Table && trim((string) $sql) === '') {
            $errors[] = new MappingError('MAP-019', $class->getShortName(), "the {$kind->value} defining SQL is empty");
        }

        return [$kind, $name, $schema, $sql, $parameters];
    }

    /**
     * @param array<mixed, mixed> $parameters
     * @param list<MappingError> $errors
     * @return list<StatementParameter>
     */
    private static function parseStatementParameters(ReflectionClass $class, array $parameters, array &$errors): array
    {
        $target = $class->getShortName() . ' [Statement]';
        $result = [];
        foreach ($parameters as $name => $type) {
            if (!is_string($name) || $name === '') {
                $errors[] = new MappingError(
                    'MAP-017',
                    $target,
                    'parameter entries must be name => ColumnType pairs; found key ' . self::describe($name),
                );
                continue;
            }

            if (!$type instanceof ColumnType) {
                $errors[] = new MappingError(
                    'MAP-017',
                    $target,
                    "parameter '{$name}' must map to a ColumnType; found " . self::describe($type),
                );
                continue;
            }

            $result[] = new StatementParameter($name, $type);
        }

        return $result;
    }

    /**
     * @param list<StatementParameter> $declared
     * @param list<MappingError> $errors
     */
    private static function checkStatementPlaceholders(
        ReflectionClass $class,
        RelationKind $kind,
        string $sql,
        array $declared,
        array &$errors,
    ): void {
        $target = $class->getShortName() . " [{$kind->value}]";
        $placeholders = SqlPlaceholders::find($sql);
        $declaredNames = array_map(static fn (StatementParameter $p): string => $p->name, $declared);

        foreach ($placeholders as $placeholder) {
            if (!in_array($placeholder, $declaredNames, true)) {
                $errors[] = new MappingError('PRM-010', $target, "SQL uses @{$placeholder}, which is not declared in the attribute");
            }
        }

        foreach ($declared as $parameter) {
            if (!in_array($parameter->name, $placeholders, true)) {
                $errors[] = new MappingError('PRM-011', $target, "declared parameter '{$parameter->name}' is never used by the SQL");
            }
        }
    }

    /**
     * @param ReflectionClass<object> $class
     * @param list<MappedPropertySpec> $specs
     * @param list<RelationshipSpec> $relationships
     * @param list<MappingError> $errors
     */
    private static function readProperties(
        ReflectionClass $class,
        RelationKind $kind,
        array &$specs,
        array &$relationships,
        array &$errors,
    ): void {
        foreach (PropertyDiscovery::propertiesInDeclarationOrder($class) as $property) {
            $target = $class->getShortName() . '.' . $property->getName();
            $column = self::attr($property, Column::class);
            $ignore = self::attr($property, Ignore::class);
            $manyToOne = self::attr($property, ManyToOne::class);
            $oneToOne = self::attr($property, OneToOne::class);
            $oneToMany = self::attr($property, OneToMany::class);
            $manyToMany = self::attr($property, ManyToMany::class);
            $key = self::attr($property, Key::class);
            $generated = self::attr($property, Generated::class);
            $version = self::attr($property, Version::class);
            $enumAsInt = self::attr($property, EnumAsInt::class);
            $foreignKey = self::attr($property, ForeignKey::class);
            $owned = self::attr($property, Owned::class);

            $navigationCount = ($manyToOne !== null ? 1 : 0) + ($oneToOne !== null ? 1 : 0)
                + ($oneToMany !== null ? 1 : 0) + ($manyToMany !== null ? 1 : 0);

            if ($owned !== null) {
                if ($column !== null || $ignore !== null || $key !== null || $generated !== null || $version !== null
                    || $enumAsInt !== null || $foreignKey !== null || $navigationCount > 0) {
                    $errors[] = new MappingError('MAP-019', $target, '#[Owned] cannot combine with any other mapping attribute');
                } else {
                    self::readOwnedType($property, $target, $owned, $specs, $errors);
                }

                continue;
            }

            if ($navigationCount > 0) {
                if ($navigationCount > 1) {
                    $errors[] = new MappingError('MAP-019', $target, 'a property carries at most one relationship attribute');
                    continue;
                }

                if ($column !== null || $ignore !== null) {
                    $errors[] = new MappingError('MAP-019', $target, 'a navigation cannot combine with #[Column] or #[Ignore]');
                }

                if (PropertyVisibility::isInvalidNavigation($property)) {
                    $errors[] = new MappingError(
                        'MAP-011',
                        $target,
                        "a navigation must not expose a public setter; declare it 'public private(set)'",
                    );
                }

                if ($manyToOne !== null) {
                    self::readManyToOne($property, $target, $manyToOne, $relationships, $errors);
                } elseif ($oneToOne !== null) {
                    self::readOneToOne($property, $target, $oneToOne, $relationships, $errors);
                } else {
                    self::readCollectionNavigation($class, $property, $target, $oneToMany, $manyToMany, $relationships, $errors);
                }

                continue;
            }

            if ($ignore !== null) {
                if ($column !== null) {
                    $errors[] = new MappingError('MAP-019', $target, '#[Ignore] cannot combine with #[Column]');
                }

                continue;
            }

            if ($column === null) {
                if ($key !== null || $generated !== null || $version !== null || $enumAsInt !== null || $foreignKey !== null) {
                    $errors[] = new MappingError('MAP-019', $target, 'mapping attributes require #[Column] on the same property');
                } elseif (PropertyVisibility::isPubliclySettable($property)) {
                    $errors[] = new MappingError(
                        'MAP-010',
                        $target,
                        'a public settable property must carry #[Column], #[Ignore], or a relationship attribute (ADR-0004)',
                    );
                }

                continue;
            }

            if ($enumAsInt !== null && !PropertyTypes::isEnumType($property)) {
                $errors[] = new MappingError('MAP-019', $target, '#[EnumAsInt] requires an enum property');
            }

            if ($kind !== RelationKind::Table && ($generated !== null || $version !== null)) {
                $errors[] = new MappingError(
                    'MAP-013',
                    $target,
                    "#[Generated]/#[Version] are only valid on a table-backed entity, not a {$kind->value}",
                );
            }

            if ($key !== null && ($kind === RelationKind::Statement || $kind === RelationKind::Procedure)) {
                $errors[] = new MappingError('MAP-013', $target, "#[Key] is not valid on a {$kind->value}-backed entity");
            }

            $resolved = PropertyTypes::resolve($property, $column->type, $enumAsInt !== null);
            $specs[] = new MappedPropertySpec(
                $property,
                $resolved->type,
                $resolved->phpType,
                $resolved->nullable,
                explicitColumn: $column->name,
                isKey: $key !== null,
                isGenerated: $generated !== null,
                isVersion: $version !== null,
                foreignKeyReferences: $foreignKey?->references,
            );
        }
    }

    /**
     * @param list<RelationshipSpec> $relationships
     * @param list<MappingError> $errors
     */
    private static function readManyToOne(
        ReflectionProperty $property,
        string $target,
        ManyToOne $manyToOne,
        array &$relationships,
        array &$errors,
    ): void {
        $targetType = PropertyTypes::toOneTargetClass($property);
        if ($targetType === null) {
            $errors[] = new MappingError(
                'MAP-020',
                $target,
                'a #[ManyToOne] navigation must be a nullable entity class (e.g. ?User), not the declared type',
            );

            return;
        }

        if ($manyToOne->foreignKeyProperties === []) {
            $errors[] = new MappingError('MAP-016', $target, '#[ManyToOne] needs at least one foreign-key property');

            return;
        }

        $relationships[] = new RelationshipSpec(
            $property->getName(),
            RelationshipKind::ManyToOne,
            $targetType,
            $manyToOne->foreignKeyProperties,
        );
    }

    /**
     * Resolves a `#[OneToOne]` navigation (ADR-0019 add.1): a single entity
     * reference — a collection here is MAP-020 — whose foreign key lives on the
     * target, named by property (MAP-021 when absent), exactly like
     * `#[OneToMany]` but singular.
     *
     * @param list<RelationshipSpec> $relationships
     * @param list<MappingError> $errors
     */
    private static function readOneToOne(
        ReflectionProperty $property,
        string $target,
        OneToOne $oneToOne,
        array &$relationships,
        array &$errors,
    ): void {
        $targetType = PropertyTypes::toOneTargetClass($property);
        if ($targetType === null) {
            $errors[] = new MappingError(
                'MAP-020',
                $target,
                'a #[OneToOne] navigation must be a nullable entity reference, not a collection',
            );

            return;
        }

        if (self::validateTargetForeignKeys($target, '#[OneToOne]', $targetType, $oneToOne->targetForeignKeyProperties, $errors)) {
            $relationships[] = new RelationshipSpec(
                $property->getName(),
                RelationshipKind::OneToOne,
                $targetType,
                $oneToOne->targetForeignKeyProperties,
            );
        }
    }

    /**
     * Resolves a `#[OneToMany]`/`#[ManyToMany]` collection navigation
     * (ADR-0019): the property must be declared `array` (MAP-020 — PHP arrays
     * carry no element type, so the target class comes from the attribute's
     * `$of`); a `#[OneToMany]` target FK that exists on the element type
     * (MAP-021); a `#[ManyToMany]` link whose `#[ForeignKey]` declarations
     * reference each side exactly once (MAP-022).
     *
     * @param ReflectionClass<object> $class
     * @param list<RelationshipSpec> $relationships
     * @param list<MappingError> $errors
     */
    private static function readCollectionNavigation(
        ReflectionClass $class,
        ReflectionProperty $property,
        string $target,
        ?OneToMany $oneToMany,
        ?ManyToMany $manyToMany,
        array &$relationships,
        array &$errors,
    ): void {
        if (!PropertyTypes::isArrayType($property)) {
            $errors[] = new MappingError('MAP-020', $target, 'a collection navigation must be declared as array (CODING-STANDARD §10)');

            return;
        }

        if ($oneToMany !== null) {
            if (self::validateTargetForeignKeys($target, '#[OneToMany]', $oneToMany->of, $oneToMany->targetForeignKeyProperties, $errors)) {
                $relationships[] = new RelationshipSpec(
                    $property->getName(),
                    RelationshipKind::OneToMany,
                    $oneToMany->of,
                    $oneToMany->targetForeignKeyProperties,
                );
            }

            return;
        }

        /** @var ManyToMany $manyToMany */
        $link = $manyToMany->through;
        $toOwner = self::linkForeignKeys($link, $class->getName(), $target, 'this type', $errors);
        $toTarget = self::linkForeignKeys($link, $manyToMany->of, $target, "'" . self::shortName($manyToMany->of) . "'", $errors);
        if ($toOwner === [] || $toTarget === []) {
            return;
        }

        $relationships[] = new RelationshipSpec(
            $property->getName(),
            RelationshipKind::ManyToMany,
            $manyToMany->of,
            foreignKeyProperties: [],
            linkType: $link,
            linkForeignKeysToOwner: $toOwner,
            linkForeignKeysToTarget: $toTarget,
        );
    }

    /** Every named FK property must exist on the target, and at least one must be named (MAP-021). */
    private static function validateTargetForeignKeys(
        string $target,
        string $attributeName,
        string $targetType,
        array $names,
        array &$errors,
    ): bool {
        if ($names === []) {
            $errors[] = new MappingError('MAP-021', $target, "{$attributeName} needs at least one target foreign-key property");

            return false;
        }

        $targetClass = new ReflectionClass($targetType);
        $missing = array_values(array_filter(
            $names,
            static fn (string $n): bool => !$targetClass->hasProperty($n) || !$targetClass->getProperty($n)->isPublic(),
        ));
        if ($missing !== []) {
            $errors[] = new MappingError(
                'MAP-021',
                $target,
                "{$attributeName} names foreign-key propert" . (count($missing) === 1 ? 'y' : 'ies') . " '"
                    . implode("', '", $missing) . "' not found on '" . $targetClass->getShortName() . "'",
            );

            return false;
        }

        return true;
    }

    /**
     * The link properties carrying `#[ForeignKey(references: $referenced)]`, in
     * declaration order — a composite-key side legitimately has several,
     * pairing with the referenced key parts in that order (ADR-0019 add.1);
     * none at all is MAP-022. The count-vs-key-arity check happens at assembly,
     * where key shapes are known.
     *
     * @param class-string $link
     * @param class-string $referenced
     * @param list<MappingError> $errors
     * @return list<string>
     */
    private static function linkForeignKeys(string $link, string $referenced, string $target, string $side, array &$errors): array
    {
        $linkClass = new ReflectionClass($link);
        $names = [];
        foreach (PropertyDiscovery::propertiesInDeclarationOrder($linkClass) as $property) {
            $fk = self::attr($property, ForeignKey::class);
            if ($fk !== null && $fk->references === $referenced) {
                $names[] = $property->getName();
            }
        }

        if ($names === []) {
            $errors[] = new MappingError(
                'MAP-022',
                $target,
                "#[ManyToMany] link '{$linkClass->getShortName()}' has no #[ForeignKey] property referencing {$side}",
            );
        }

        return $names;
    }

    /**
     * @param ReflectionClass<object> $class
     * @param list<MappingError> $errors
     * @return list<IndexSpec>
     */
    private static function readIndexes(ReflectionClass $class, RelationKind $kind, array &$errors): array
    {
        $attributes = $class->getAttributes(Index::class);
        if ($attributes === []) {
            return [];
        }

        if ($kind !== RelationKind::Table && $kind !== RelationKind::MaterializedView) {
            $errors[] = new MappingError(
                'MAP-014',
                $class->getShortName(),
                "#[Index] is only valid on tables and materialized views, not a {$kind->value}",
            );

            return [];
        }

        $specs = [];
        foreach ($attributes as $attribute) {
            $index = $attribute->newInstance();
            $target = $class->getShortName() . ' [Index]';
            /** @var list<array{0: string, 1: bool}> $columns */
            $columns = [];
            $valid = true;

            foreach ($index->columns as $token) {
                if (is_string($token)) {
                    $columns[] = [$token, false];
                } elseif ($token instanceof SortOrder) {
                    if ($columns === []) {
                        $errors[] = new MappingError('MAP-015', $target, "SortOrder::{$token->name} has no preceding column to apply to");
                        $valid = false;
                    } else {
                        $last = array_key_last($columns);
                        $columns[$last][1] = $token === SortOrder::Desc;
                    }
                } else {
                    $errors[] = new MappingError(
                        'MAP-015',
                        $target,
                        "token '" . self::describe($token) . "' is neither a property name string nor a SortOrder",
                    );
                    $valid = false;
                }
            }

            for ($i = 1; $i < count($index->columns); $i++) {
                if ($index->columns[$i] instanceof SortOrder && $index->columns[$i - 1] instanceof SortOrder) {
                    $errors[] = new MappingError('MAP-015', $target, 'two consecutive SortOrder tokens; each applies to the column before it');
                    $valid = false;
                }
            }

            if ($columns === []) {
                $errors[] = new MappingError('MAP-015', $target, 'the column list is empty');
                $valid = false;
            }

            if ($valid) {
                $specs[] = new IndexSpec($index->name, $index->unique, $columns);
            }
        }

        return $specs;
    }

    /**
     * Reads an `#[Owned]` value type (ADR-0030): a class with a parameterless
     * constructor, declared `#[Owned]` at class level, whose `#[Column]` members
     * flatten into the owner under the navigation's prefix. The owned type is
     * not an entity — a relation source, `#[Index]`, key, version, generated
     * column, foreign key, relationship, or nested `#[Owned]` inside it is
     * `MAP-024`; the opt-in rule (`MAP-010`) applies to its members exactly as to
     * an entity's.
     *
     * @param list<MappedPropertySpec> $specs
     * @param list<MappingError> $errors
     */
    private static function readOwnedType(
        ReflectionProperty $navigation,
        string $target,
        Owned $owned,
        array &$specs,
        array &$errors,
    ): void {
        $navigationType = $navigation->getType();
        if (!$navigationType instanceof ReflectionNamedType || $navigationType->isBuiltin()
            || !class_exists($navigationType->getName())) {
            $errors[] = new MappingError(
                'MAP-024',
                $target,
                'an #[Owned] navigation must be a single class-typed value; collections and scalars cannot be owned',
            );

            return;
        }

        /** @var class-string $ownedType */
        $ownedType = $navigationType->getName();
        $ownedClass = new ReflectionClass($ownedType);
        foreach ([Table::class, View::class, MaterializedView::class, Statement::class, Procedure::class, Index::class] as $attribute) {
            if ($ownedClass->getAttributes($attribute) !== []) {
                $errors[] = new MappingError(
                    'MAP-024',
                    $target,
                    "'{$ownedClass->getShortName()}' is an entity (it carries a relation source or #[Index]); an owned type has no table of its own",
                );

                return;
            }
        }

        if (!self::isOwnedType($ownedClass)) {
            $errors[] = new MappingError(
                'MAP-024',
                $target,
                "'{$ownedClass->getShortName()}' must itself be declared #[Owned] at class level — that is what keeps it out of the entity set",
            );

            return;
        }

        $constructor = $ownedClass->getConstructor();
        if ($constructor !== null && $constructor->getNumberOfRequiredParameters() > 0) {
            $errors[] = new MappingError(
                'MAP-024',
                $target,
                "'{$ownedClass->getShortName()}' needs a parameterless constructor to be owned",
            );

            return;
        }

        $type = $navigation->getType();
        $spec = new OwnedSpec($navigation, $ownedType, $type === null || $type->allowsNull(), $owned->prefix);
        $mapped = 0;
        foreach (PropertyDiscovery::propertiesInDeclarationOrder($ownedClass) as $member) {
            $memberTarget = $ownedClass->getShortName() . '.' . $member->getName();
            $column = self::attr($member, Column::class);
            $ignore = self::attr($member, Ignore::class);
            $enumAsInt = self::attr($member, EnumAsInt::class);
            $forbidden = false;
            foreach ([Key::class, Generated::class, Version::class, ForeignKey::class, Owned::class,
                ManyToOne::class, OneToOne::class, OneToMany::class, ManyToMany::class] as $attribute) {
                if ($member->getAttributes($attribute) !== []) {
                    $forbidden = true;
                }
            }

            if ($forbidden) {
                $errors[] = new MappingError(
                    'MAP-024',
                    $memberTarget,
                    "an owned type's members carry only #[Column], #[EnumAsInt], or #[Ignore]: no key, version, generated column, foreign key, relationship, or nested #[Owned]",
                );
                continue;
            }

            if ($ignore !== null) {
                if ($column !== null) {
                    $errors[] = new MappingError('MAP-019', $memberTarget, '#[Ignore] cannot combine with #[Column]');
                }

                continue;
            }

            if ($column === null) {
                if ($enumAsInt !== null) {
                    $errors[] = new MappingError('MAP-019', $memberTarget, 'mapping attributes require #[Column] on the same property');
                } elseif (PropertyVisibility::isPubliclySettable($member)) {
                    $errors[] = new MappingError(
                        'MAP-010',
                        $memberTarget,
                        'a public settable property must carry #[Column] or #[Ignore] (ADR-0004)',
                    );
                }

                continue;
            }

            if ($enumAsInt !== null && !PropertyTypes::isEnumType($member)) {
                $errors[] = new MappingError('MAP-019', $memberTarget, '#[EnumAsInt] requires an enum property');
            }

            $resolved = PropertyTypes::resolve($member, $column->type, $enumAsInt !== null);
            $specs[] = new MappedPropertySpec(
                $member,
                $resolved->type,
                $resolved->phpType,
                $resolved->nullable,
                explicitColumn: $column->name,
                owner: $spec,
            );
            $mapped++;
        }

        if ($mapped === 0) {
            $errors[] = new MappingError(
                'MAP-024',
                $target,
                "'{$ownedClass->getShortName()}' maps no columns; an owned type needs at least one #[Column] member",
            );
        }
    }

    /**
     * An `#[Owned]` value type (ADR-0030): never an entity, so assembly scans skip it.
     *
     * @param ReflectionClass<object> $class
     */
    public static function isOwnedType(ReflectionClass $class): bool
    {
        return $class->getAttributes(Owned::class) !== [];
    }

    /** @param ReflectionClass<object> $class */
    public static function hasMappingAttributes(ReflectionClass $class): bool
    {
        foreach ([Table::class, View::class, MaterializedView::class, Statement::class, Procedure::class, Index::class] as $attribute) {
            if ($class->getAttributes($attribute) !== []) {
                return true;
            }
        }

        $propertyAttributes = [
            Column::class, Ignore::class, Key::class, Generated::class, Version::class, EnumAsInt::class,
            ForeignKey::class, ManyToOne::class, OneToOne::class, OneToMany::class, ManyToMany::class, Owned::class,
        ];
        foreach (PropertyDiscovery::propertiesInDeclarationOrder($class) as $property) {
            foreach ($propertyAttributes as $attribute) {
                if ($property->getAttributes($attribute) !== []) {
                    return true;
                }
            }
        }

        return false;
    }

    /**
     * @template T of object
     * @param class-string<T> $attributeClass
     * @return T|null
     */
    private static function attr(ReflectionClass|ReflectionProperty $subject, string $attributeClass): ?object
    {
        $attributes = $subject->getAttributes($attributeClass);

        return $attributes === [] ? null : $attributes[0]->newInstance();
    }

    /** @param class-string $fqcn */
    private static function shortName(string $fqcn): string
    {
        $slash = strrpos($fqcn, '\\');

        return $slash === false ? $fqcn : substr($fqcn, $slash + 1);
    }

    private static function describe(mixed $token): string
    {
        return match (true) {
            $token === null => 'null',
            is_string($token) => "\"{$token}\"",
            is_scalar($token) => get_debug_type($token) . " '{$token}'",
            default => get_debug_type($token),
        };
    }
}
