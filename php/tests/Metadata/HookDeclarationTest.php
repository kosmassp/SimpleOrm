<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Metadata;

use DateTimeImmutable;
use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use SimpleOrm\Errors\MappingException;
use SimpleOrm\Metadata\Attributes\Column;
use SimpleOrm\Metadata\Attributes\CreatedAt;
use SimpleOrm\Metadata\Attributes\Generated;
use SimpleOrm\Metadata\Attributes\Ignore;
use SimpleOrm\Metadata\Attributes\Key;
use SimpleOrm\Metadata\Attributes\ManyToOne;
use SimpleOrm\Metadata\Attributes\Owned;
use SimpleOrm\Metadata\Attributes\Procedure;
use SimpleOrm\Metadata\Attributes\SoftDelete;
use SimpleOrm\Metadata\Attributes\Statement;
use SimpleOrm\Metadata\Attributes\Table;
use SimpleOrm\Metadata\Attributes\UpdatedAt;
use SimpleOrm\Metadata\Attributes\Version;
use SimpleOrm\Metadata\AuditRole;
use SimpleOrm\Metadata\EntityMapBuilder;
use SimpleOrm\Metadata\EntityMapLoader;
use SimpleOrm\Tests\Sample\Models\User;

/**
 * Every misplaced hook is exactly `MAP-026` (Level 3 M3, spec/hooks.md "Declaration rules" and Clarifications): on a
 * navigation, an ignored or unmapped property, an owned navigation or an owned type's member, a non-table source, a
 * key, version, or generated column, with the wrong type — and the manual builder holds to the same rules. "Exactly"
 * matters: the Clarifications say never `MAP-019`/`MAP-024` for these, so the test fails on any other code beside it.
 */
final class HookDeclarationTest extends TestCase
{
    /** @return array<string, array{class-string}> */
    public static function misplacedAttributeHooks(): array
    {
        return [
            'on a navigation' => [HookDeclOnNavigation::class],
            'on an #[Ignore]d property' => [HookDeclOnIgnored::class],
            'on an owned navigation' => [HookDeclOnOwnedNavigation::class],
            'on an owned type\'s member' => [HookDeclOnOwnedMember::class],
            'on a statement-backed entity' => [HookDeclOnStatement::class],
            'on a procedure-backed entity' => [HookDeclOnProcedure::class],
            'on the version column' => [HookDeclOnVersion::class],
            'on a generated column' => [HookDeclOnGenerated::class],
            'soft delete on an int' => [HookDeclSoftDeleteOnInt::class],
        ];
    }

    /** @param class-string $entityType */
    #[Test]
    #[DataProvider('misplacedAttributeHooks')]
    public function a_misplaced_attribute_hook_is_exactly_map026(string $entityType): void
    {
        try {
            (new EntityMapLoader())->load($entityType);
            self::fail("{$entityType} was expected to fail loading");
        } catch (MappingException $e) {
            self::assertNotSame([], $e->errors);
            foreach ($e->errors as $error) {
                self::assertSame('MAP-026', $error->code, (string) $error);
            }
        }
    }

    /** @return array<string, array{callable(EntityMapBuilder): EntityMapBuilder}> */
    public static function misplacedBuilderHooks(): array
    {
        return [
            'a hook on the key' => [static fn (EntityMapBuilder $b) => $b->column('id', key: true, audit: AuditRole::CreatedAt)],
            'a hook on the version' => [static fn (EntityMapBuilder $b) => $b->column('id', key: true)
                ->column('version', version: true, audit: AuditRole::UpdatedAt)],
            'a hook on a generated column' => [static fn (EntityMapBuilder $b) => $b->column('id', key: true)
                ->column('flag', generated: true, softDelete: true)],
            'created at on a string' => [static fn (EntityMapBuilder $b) => $b->column('id', key: true)
                ->column('name', audit: AuditRole::CreatedAt)],
            'soft delete on a nullable' => [static fn (EntityMapBuilder $b) => $b->column('id', key: true)
                ->column('maybe', softDelete: true)],
            'two hooks on one property' => [static fn (EntityMapBuilder $b) => $b->column('id', key: true)
                ->column('stamp', audit: AuditRole::CreatedAt, softDelete: true)],
            'one role on two properties' => [static fn (EntityMapBuilder $b) => $b->column('id', key: true)
                ->column('stamp', audit: AuditRole::UpdatedAt)
                ->column('other', audit: AuditRole::UpdatedAt)],
        ];
    }

    /** @param callable(EntityMapBuilder): EntityMapBuilder $declare */
    #[Test]
    #[DataProvider('misplacedBuilderHooks')]
    public function the_manual_builder_refuses_a_misplaced_hook_as_exactly_map026(callable $declare): void
    {
        try {
            $declare(EntityMapBuilder::for(HookDeclBuilt::class)->table('hook_decl_built'))->build();
            self::fail('the misplaced hook was expected to fail the build');
        } catch (MappingException $e) {
            self::assertNotSame([], $e->errors);
            foreach ($e->errors as $error) {
                self::assertSame('MAP-026', $error->code, (string) $error);
            }
        }
    }
}

/** A hook on a `#[ManyToOne]` navigation. */
#[Table('hook_decl_on_navigation')]
final class HookDeclOnNavigation
{
    #[Key]
    #[Generated]
    #[Column]
    public int $id;

    #[Column]
    public int $ownerId;

    #[ManyToOne('ownerId')]
    #[UpdatedAt]
    public private(set) ?User $owner = null;
}

/** A hook on an `#[Ignore]`d property. */
#[Table('hook_decl_on_ignored')]
final class HookDeclOnIgnored
{
    #[Key]
    #[Generated]
    #[Column]
    public int $id;

    #[Ignore]
    #[CreatedAt]
    public ?DateTimeImmutable $at = null;
}

/** An owned type whose only member carries a hook: the member's error is the one error. */
#[Owned]
final class HookDeclStampedAddress
{
    #[Column]
    #[CreatedAt]
    public ?DateTimeImmutable $at = null;
}

/** A hook on an owned type's member. */
#[Table('hook_decl_on_owned_member')]
final class HookDeclOnOwnedMember
{
    #[Key]
    #[Generated]
    #[Column]
    public int $id;

    #[Owned]
    public ?HookDeclStampedAddress $address = null;
}

/** An owned type with an ordinary member. */
#[Owned]
final class HookDeclPlainAddress
{
    #[Column]
    public string $city = '';
}

/** A hook on the owned navigation itself. */
#[Table('hook_decl_on_owned_navigation')]
final class HookDeclOnOwnedNavigation
{
    #[Key]
    #[Generated]
    #[Column]
    public int $id;

    #[Owned]
    #[CreatedAt]
    public ?HookDeclPlainAddress $address = null;
}

/** A hook on a statement-backed entity: it could never act. */
#[Statement('select 1 as id, 0 as flag')]
final class HookDeclOnStatement
{
    #[Column]
    public int $id;

    #[Column]
    #[SoftDelete]
    public bool $flag;
}

/** A hook on a procedure-backed entity: it could never act. */
#[Procedure('hook_decl_procedure', 'select 1 as id, null as at')]
final class HookDeclOnProcedure
{
    #[Column]
    public int $id;

    #[Column]
    #[UpdatedAt]
    public ?DateTimeImmutable $at = null;
}

/** A hook on the version column. */
#[Table('hook_decl_on_version')]
final class HookDeclOnVersion
{
    #[Key]
    #[Generated]
    #[Column]
    public int $id;

    #[Version]
    #[Column]
    #[UpdatedAt]
    public int $version = 0;
}

/** A hook on a generated column. */
#[Table('hook_decl_on_generated')]
final class HookDeclOnGenerated
{
    #[Key]
    #[Generated]
    #[Column]
    public int $id;

    #[Generated]
    #[Column]
    #[CreatedAt]
    public ?DateTimeImmutable $at = null;
}

/** A soft-delete flag that is not a bool. */
#[Table('hook_decl_soft_delete_on_int')]
final class HookDeclSoftDeleteOnInt
{
    #[Key]
    #[Generated]
    #[Column]
    public int $id;

    #[Column]
    #[SoftDelete]
    public int $flag = 0;
}

/** The properties the manual-builder cases pick from. */
final class HookDeclBuilt
{
    public int $id = 0;

    public int $version = 0;

    public DateTimeImmutable $stamp;

    public DateTimeImmutable $other;

    public string $name = '';

    public bool $flag = false;

    public ?bool $maybe = null;
}
