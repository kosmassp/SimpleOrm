<?php

declare(strict_types=1);

namespace SimpleOrm\Metadata;

use ReflectionClass;
use SimpleOrm\Errors\MappingError;
use SimpleOrm\Errors\MappingException;
use SimpleOrm\Errors\SimpleOrmException;
use SimpleOrm\Metadata\Attributes\MaterializedView;
use SimpleOrm\Metadata\Attributes\Procedure;
use SimpleOrm\Metadata\Attributes\Statement;
use SimpleOrm\Metadata\Attributes\Table;
use SimpleOrm\Metadata\Attributes\Trigger;
use SimpleOrm\Metadata\Attributes\View;

/**
 * `MAP-029` — triggers in the mapping (Level 4, ADR-0042, spec/metadata-model.md "Triggers"), the one home of the
 * rules (the shape of {@see BaseClassRules}):
 *
 *  1. {@see self::resolve()} — an entity's links, resolved to {@see EntityTrigger}s: every misplaced or malformed
 *     declaration is one collected error (never first-error-only), shared by the attribute loader and the builder;
 *  2. {@see self::assertNotTriggerClass()} — a trigger class loaded as an entity;
 *  3. {@see self::scanErrors()} — in an assembly scan, a trigger class linked by two entities and two classes
 *     declaring one name, the conflicts a lone load cannot see (SchemaGuard and the CLI both run it).
 */
final class TriggerRules
{
    /** @var list<class-string> */
    private const RELATION_SOURCES = [Table::class, View::class, MaterializedView::class, Statement::class, Procedure::class];

    private function __construct()
    {
    }

    /**
     * The `#[Trigger]` declarations (not links) a class carries — a trigger class has exactly one.
     *
     * @param ReflectionClass<object> $class
     * @return list<Trigger>
     */
    public static function declarations(ReflectionClass $class): array
    {
        $declarations = [];
        foreach ($class->getAttributes(Trigger::class) as $attribute) {
            $trigger = $attribute->newInstance();
            if (!$trigger->isLink()) {
                $declarations[] = $trigger;
            }
        }

        return $declarations;
    }

    /**
     * The trigger classes an entity links, as written, in link order.
     *
     * @param ReflectionClass<object> $class
     * @return list<string>
     */
    public static function linksOf(ReflectionClass $class): array
    {
        $links = [];
        foreach ($class->getAttributes(Trigger::class) as $attribute) {
            $trigger = $attribute->newInstance();
            if ($trigger->isLink()) {
                $links[] = $trigger->nameOrClass;
            }
        }

        return $links;
    }

    /** Whether the class is a trigger declaration object (carries a declaration `#[Trigger]`). */
    public static function isTriggerClass(ReflectionClass $class): bool
    {
        return self::declarations($class) !== [];
    }

    /**
     * A trigger class is a declaration object, never an entity: loading it as one is `MAP-029`.
     *
     * @param class-string $entityType
     * @throws MappingException `MAP-029`
     */
    public static function assertNotTriggerClass(string $entityType): void
    {
        $class = new ReflectionClass($entityType);
        if (self::isTriggerClass($class)) {
            throw new MappingException($entityType, [new MappingError(
                'MAP-029',
                $class->getShortName(),
                'is a #[Trigger] declaration class, not an entity: it has no columns and no key; an entity links it with #[Trigger(' . $class->getShortName() . '::class)]',
            )]);
        }
    }

    /**
     * The declared name of a trigger class — the migration object's identity, readable whether or not an entity still
     * links the class (a removal step needs it after the unlink).
     *
     * @param class-string $triggerClass
     */
    public static function declaredName(string $triggerClass): string
    {
        $declarations = self::declarations(new ReflectionClass($triggerClass));
        if (count($declarations) !== 1) {
            throw new SimpleOrmException(
                'MAP-029',
                $triggerClass,
                'must carry exactly one #[Trigger] declaration, found ' . count($declarations),
            );
        }

        return $declarations[0]->nameOrClass;
    }

    /**
     * Resolves an entity's trigger links (every violation `MAP-029`, collected into `$errors`). A link on a source
     * that cannot take one, a link to a class that is no declaration (none, two, an entity, an owned type), a
     * malformed declaration, and a class linked twice are each one error; the remaining links resolve in link order.
     *
     * @param list<string> $links trigger classes as written
     * @param array<string, PropertyMap> $byProperty the entity's mapped properties by property path
     * @param list<MappingError> $errors
     * @return list<EntityTrigger>
     */
    public static function resolve(string $shortName, RelationKind $kind, array $links, array $byProperty, array &$errors): array
    {
        $resolved = [];
        $seen = [];
        foreach ($links as $link) {
            $target = "{$shortName} #[Trigger {$link}]";
            if ($kind !== RelationKind::Table && $kind !== RelationKind::View) {
                $errors[] = new MappingError(
                    'MAP-029',
                    $target,
                    "a trigger link is only valid on a table (any timing) or a view (instead_of), not a {$kind->value}",
                );

                continue;
            }

            if (!class_exists($link)) {
                $errors[] = new MappingError('MAP-029', $target, "'{$link}' is not a class; a link names the trigger class");

                continue;
            }

            $class = new ReflectionClass($link);
            $triggerClass = $class->getName();
            $target = "{$shortName} #[Trigger {$class->getShortName()}]";
            if (isset($seen[$triggerClass])) {
                $errors[] = new MappingError('MAP-029', $target, 'is linked more than once by this entity');

                continue;
            }

            $seen[$triggerClass] = true;
            $definition = self::readDeclaration($class, $target, $kind, $byProperty, $errors);
            if ($definition !== null) {
                $resolved[] = new EntityTrigger($triggerClass, $definition);
            }
        }

        return $resolved;
    }

    /**
     * One linked class's declaration, or null with its errors collected.
     *
     * @param ReflectionClass<object> $class
     * @param array<string, PropertyMap> $byProperty
     * @param list<MappingError> $errors
     */
    private static function readDeclaration(
        ReflectionClass $class,
        string $target,
        RelationKind $kind,
        array $byProperty,
        array &$errors,
    ): ?TriggerDefinition {
        $before = count($errors);
        if (self::isEntityOrOwned($class)) {
            $errors[] = new MappingError(
                'MAP-029',
                $target,
                'is an entity or an #[Owned] type, not a trigger declaration class: the class a link names carries the #[Trigger] declaration and nothing else',
            );
        }

        $declarations = self::declarations($class);
        if ($declarations === []) {
            $errors[] = new MappingError('MAP-029', $target, 'carries no #[Trigger] declaration: the linked class declares the name, timing, events and body');
        } elseif (count($declarations) > 1) {
            $errors[] = new MappingError(
                'MAP-029',
                $target,
                'carries ' . count($declarations) . ' #[Trigger] declarations; a trigger class declares exactly one',
            );
        }

        if (count($declarations) !== 1) {
            return null;
        }

        $declaration = $declarations[0];
        foreach (self::declarationProblems($declaration, $kind, $byProperty) as $problem) {
            $errors[] = new MappingError('MAP-029', $target, $problem);
        }

        if (count($errors) > $before || $declaration->timing === null) {
            return null;
        }

        $columns = array_map(static fn (string $property): string => $byProperty[$property]->columnName, $declaration->updateOf);

        return new TriggerDefinition($declaration->nameOrClass, $declaration->timing, self::eventsOf($declaration), $declaration->sql, $columns);
    }

    /**
     * Every problem with a declaration against the entity it is linked from.
     *
     * @param array<string, PropertyMap> $byProperty
     * @return list<string>
     */
    private static function declarationProblems(Trigger $declaration, RelationKind $kind, array $byProperty): array
    {
        $problems = [];
        if (trim($declaration->nameOrClass) === '') {
            $problems[] = 'the trigger name is blank';
        }

        if ($declaration->timing === null) {
            $problems[] = 'the declaration has no timing: say TriggerTiming::Before, After or InsteadOf';
        } elseif ($kind === RelationKind::View && $declaration->timing !== TriggerTiming::InsteadOf) {
            $problems[] = "a view takes only TriggerTiming::InsteadOf triggers, not {$declaration->timing->value}";
        }

        foreach ($declaration->events as $event) {
            if (!$event instanceof TriggerEvent) {
                $problems[] = 'events must be TriggerEvent cases (Insert, Update, Delete)';

                break;
            }
        }

        $events = self::eventsOf($declaration);
        if ($events === []) {
            $problems[] = 'the declaration names no event: say at least one of TriggerEvent::Insert, Update, Delete';
        }

        if (trim($declaration->sql) === '') {
            $problems[] = 'the trigger body is blank';
        }

        if ($declaration->updateOf !== []) {
            if (!in_array(TriggerEvent::Update, $events, true)) {
                $problems[] = 'updateOf narrows the update event, which this declaration does not name';
            }

            foreach ($declaration->updateOf as $property) {
                if (!isset($byProperty[$property])) {
                    $problems[] = "updateOf names '{$property}', which is not a mapped property";
                }
            }
        }

        return [...$problems, ...self::tokenProblems($declaration->sql, $events, $byProperty)];
    }

    /**
     * Body token problems: malformed or unknown tokens, a `--` line comment, row tokens naming unmapped columns
     * (matched exactly), and row tokens impossible for the events — the one body serves every declared event, so a
     * row token needs rows on each: `{new}` refuses a declaration that includes delete, `{old}` one that includes
     * insert.
     *
     * @param list<TriggerEvent> $events
     * @param array<string, PropertyMap> $byProperty
     * @return list<string>
     */
    private static function tokenProblems(string $sql, array $events, array $byProperty): array
    {
        $problems = [];
        $segments = TriggerBody::parse($sql, $problems);

        $columns = [];
        foreach ($byProperty as $property) {
            $columns[$property->columnName] = true;
        }

        $insert = in_array(TriggerEvent::Insert, $events, true);
        $delete = in_array(TriggerEvent::Delete, $events, true);
        foreach ($segments as $segment) {
            if (!$segment instanceof TriggerToken || $segment->kind === TriggerTokenKind::Now) {
                continue;
            }

            $token = $segment->canonical();
            if ($segment->kind === TriggerTokenKind::New && $delete) {
                $problems[] = "{$token} has no rows on the delete event; a body is shared by every declared event";
            }

            if ($segment->kind === TriggerTokenKind::Old && $insert) {
                $problems[] = "{$token} has no rows on the insert event; a body is shared by every declared event";
            }

            foreach ($segment->columns as $column) {
                if (!isset($columns[$column])) {
                    $problems[] = "{$token} names '{$column}', which is not a column of the entity's relation";
                }
            }
        }

        return $problems;
    }

    /**
     * @return list<TriggerEvent>
     */
    private static function eventsOf(Trigger $declaration): array
    {
        return TriggerEvent::ordered(array_values(array_filter(
            $declaration->events,
            static fn (mixed $event): bool => $event instanceof TriggerEvent,
        )));
    }

    /** @param ReflectionClass<object> $class */
    private static function isEntityOrOwned(ReflectionClass $class): bool
    {
        foreach (self::RELATION_SOURCES as $attribute) {
            if ($class->getAttributes($attribute) !== []) {
                return true;
            }
        }

        return AttributeMapLoader::isOwnedType($class) || AttributeMapLoader::hasMappingAttributes($class);
    }

    /**
     * The assembly-scan conflicts (SchemaGuard, every CLI command that enumerates entities): a trigger class linked by
     * two entities, and two classes declaring one name — one `MAP-029` per conflict, sorted by name.
     *
     * @param list<class-string> $classes the scanned classes
     * @return list<MappingError>
     */
    public static function scanErrors(array $classes): array
    {
        /** @var array<class-string, string> $names trigger class => declared name: every declaration class the scan sees, linked or not */
        $names = [];
        /** @var array<class-string, list<class-string>> $linkedBy trigger class => linking entities */
        $linkedBy = [];

        foreach ($classes as $className) {
            $class = new ReflectionClass($className);
            if ($class->isInterface() || $class->isTrait() || $class->isEnum()) {
                continue;
            }

            $declarations = self::declarations($class);
            if ($declarations !== []) {
                $names[$class->getName()] = $declarations[0]->nameOrClass;

                continue;
            }

            if ($class->isAbstract() || AttributeMapLoader::isOwnedType($class) || !AttributeMapLoader::hasMappingAttributes($class)) {
                continue;
            }

            foreach (array_unique(self::linksOf($class)) as $link) {
                if (!class_exists($link)) {
                    continue;
                }

                $linked = new ReflectionClass($link);
                $linkedDeclarations = self::declarations($linked);
                if (count($linkedDeclarations) === 1) {
                    $names[$linked->getName()] = $linkedDeclarations[0]->nameOrClass;
                    $linkedBy[$linked->getName()][] = $class->getName();
                }
            }
        }

        $errors = [];
        ksort($linkedBy, SORT_STRING);
        foreach ($linkedBy as $triggerClass => $entities) {
            if (count($entities) < 2) {
                continue;
            }

            sort($entities, SORT_STRING);
            $errors[] = new MappingError(
                'MAP-029',
                self::shortName($triggerClass),
                'is linked by ' . implode(', ', array_map(self::shortName(...), $entities))
                    . '; a trigger belongs to one table, so declare one trigger class per table',
            );
        }

        /** @var array<string, list<class-string>> $byName */
        $byName = [];
        foreach ($names as $triggerClass => $name) {
            $byName[strtolower(trim($name))][] = $triggerClass;
        }

        ksort($byName, SORT_STRING);
        foreach ($byName as $name => $triggerClasses) {
            if ($name === '' || count($triggerClasses) < 2) {
                continue;
            }

            sort($triggerClasses, SORT_STRING);
            $errors[] = new MappingError(
                'MAP-029',
                trim($names[$triggerClasses[0]]),
                'is declared by ' . implode(', ', array_map(self::shortName(...), $triggerClasses))
                    . '; trigger names are unique across the model (database-wide on SQLite and SQL Server)',
            );
        }

        return $errors;
    }

    private static function shortName(string $fqcn): string
    {
        $slash = strrpos($fqcn, '\\');

        return $slash === false ? $fqcn : substr($fqcn, $slash + 1);
    }
}
