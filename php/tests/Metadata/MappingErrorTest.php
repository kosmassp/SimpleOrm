<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Metadata;

use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use SimpleOrm\Errors\MappingException;
use SimpleOrm\Metadata\Attributes\Column;
use SimpleOrm\Metadata\Attributes\EnumAsInt;
use SimpleOrm\Metadata\Attributes\ForeignKey;
use SimpleOrm\Metadata\Attributes\Generated;
use SimpleOrm\Metadata\Attributes\Index;
use SimpleOrm\Metadata\Attributes\Key;
use SimpleOrm\Metadata\Attributes\ManyToMany;
use SimpleOrm\Metadata\Attributes\ManyToOne;
use SimpleOrm\Metadata\Attributes\OneToMany;
use SimpleOrm\Metadata\Attributes\OneToOne;
use SimpleOrm\Metadata\Attributes\Statement;
use SimpleOrm\Metadata\Attributes\Table;
use SimpleOrm\Metadata\Attributes\Version;
use SimpleOrm\Metadata\Attributes\View;
use SimpleOrm\Metadata\ColumnType;
use SimpleOrm\Metadata\EntityMapLoader;
use SimpleOrm\Query\SortOrder;
use SimpleOrm\Tests\Sample\Models\Role;
use SimpleOrm\Tests\Sample\Models\Transaction;
use SimpleOrm\Tests\Sample\Models\User;
use SimpleOrm\Tests\Sample\Models\UserRole;

/**
 * Every loader error code (spec/errors.md) has a fixture that must fail with
 * it — mirrors the C# reference's MappingErrorTests and RelationshipMetadataTests
 * error scenarios, PHP-ized (asymmetric visibility instead of `private set`,
 * explicit target classes on collection navigations).
 */
final class MappingErrorTest extends TestCase
{
    /** @param class-string $entityType */
    private static function codesOf(string $entityType): array
    {
        try {
            (new EntityMapLoader())->load($entityType);
            self::fail("{$entityType} was expected to fail loading");
        } catch (MappingException $e) {
            return array_map(static fn ($error) => $error->code, $e->errors);
        }
    }

    /** @param class-string $entityType */
    private static function assertCode(string $code, string $entityType): void
    {
        self::assertContains($code, self::codesOf($entityType));
    }

    #[Test]
    public function unannotated_public_settable_property_is_map010(): void
    {
        self::assertCode('MAP-010', Map010Fixture::class);
    }

    #[Test]
    public function navigation_with_public_setter_is_map011(): void
    {
        self::assertCode('MAP-011', Map011Fixture::class);
    }

    #[Test]
    public function public_setter_on_a_collection_navigation_is_map011(): void
    {
        self::assertCode('MAP-011', Map011CollectionFixture::class);
    }

    #[Test]
    public function two_relation_sources_is_map012(): void
    {
        self::assertCode('MAP-012', Map012Fixture::class);
    }

    #[Test]
    public function version_on_view_is_map013(): void
    {
        self::assertCode('MAP-013', Map013ViewFixture::class);
    }

    #[Test]
    public function key_on_statement_is_map013(): void
    {
        self::assertCode('MAP-013', Map013StatementFixture::class);
    }

    #[Test]
    public function index_on_view_is_map014(): void
    {
        self::assertCode('MAP-014', Map014Fixture::class);
    }

    #[Test]
    public function index_with_unknown_property_is_map015(): void
    {
        self::assertCode('MAP-015', Map015UnknownFixture::class);
    }

    #[Test]
    public function index_with_leading_sort_order_is_map015(): void
    {
        self::assertCode('MAP-015', Map015LeadingFixture::class);
    }

    #[Test]
    public function index_with_doubled_sort_order_is_map015(): void
    {
        self::assertCode('MAP-015', Map015DoubledFixture::class);
    }

    #[Test]
    public function index_with_alien_token_is_map015(): void
    {
        self::assertCode('MAP-015', Map015TokenFixture::class);
    }

    #[Test]
    public function many_to_one_with_unknown_fk_is_map016(): void
    {
        self::assertCode('MAP-016', Map016Fixture::class);
    }

    #[Test]
    public function foreign_key_arity_must_match_the_target_key(): void
    {
        self::assertCode('MAP-016', CompositeArityFixture::class);
    }

    #[Test]
    public function statement_with_malformed_parameter_entry_is_map017(): void
    {
        self::assertCode('MAP-017', Map017Fixture::class);
    }

    #[Test]
    public function duplicate_column_is_map018(): void
    {
        self::assertCode('MAP-018', Map018Fixture::class);
    }

    #[Test]
    public function table_without_key_is_map019(): void
    {
        self::assertCode('MAP-019', Map019NoKeyFixture::class);
    }

    #[Test]
    public function version_of_wrong_type_is_map019(): void
    {
        self::assertCode('MAP-019', Map019VersionFixture::class);
    }

    #[Test]
    public function generated_on_composite_key_is_map019(): void
    {
        self::assertCode('MAP-019', Map019CompositeFixture::class);
    }

    #[Test]
    public function key_without_column_is_map019(): void
    {
        self::assertCode('MAP-019', Map019BareKeyFixture::class);
    }

    #[Test]
    public function enum_as_int_on_non_enum_is_map019(): void
    {
        self::assertCode('MAP-019', Map019EnumFixture::class);
    }

    #[Test]
    public function view_with_empty_sql_is_map019(): void
    {
        self::assertCode('MAP-019', Map019EmptyViewFixture::class);
    }

    #[Test]
    public function column_on_a_navigation_is_map019(): void
    {
        self::assertCode('MAP-019', Map019NavigationFixture::class);
    }

    #[Test]
    public function one_to_one_on_a_collection_is_map020(): void
    {
        self::assertCode('MAP-020', Map020OneToOneFixture::class);
    }

    #[Test]
    public function non_collection_navigation_is_map020(): void
    {
        self::assertCode('MAP-020', Map020Fixture::class);
    }

    #[Test]
    public function unknown_target_foreign_key_is_map021(): void
    {
        self::assertCode('MAP-021', Map021Fixture::class);
    }

    #[Test]
    public function link_missing_a_side_is_map022(): void
    {
        self::assertCode('MAP-022', Map022MissingFixture::class);
    }

    #[Test]
    public function link_with_an_ambiguous_side_is_map022(): void
    {
        self::assertCode('MAP-022', Map022AmbiguousFixture::class);
    }

    #[Test]
    public function undeclared_placeholder_is_prm010(): void
    {
        self::assertCode('PRM-010', Prm010Fixture::class);
    }

    #[Test]
    public function view_sql_with_placeholder_is_prm010(): void
    {
        self::assertCode('PRM-010', Prm010ViewFixture::class);
    }

    #[Test]
    public function unused_declared_parameter_is_prm011(): void
    {
        self::assertCode('PRM-011', Prm011Fixture::class);
    }

    #[Test]
    public function all_violations_are_collected_before_throwing(): void
    {
        $codes = self::codesOf(MultiErrorFixture::class);
        self::assertContains('MAP-010', $codes);
        self::assertContains('MAP-019', $codes);
    }
}

// --- fixtures ----------------------------------------------------------

#[Table('map010')]
final class Map010Fixture
{
    #[Key]
    #[Column]
    public int $id;

    public ?string $forgotten = null;
}

#[Table('map011')]
final class Map011Fixture
{
    #[Key]
    #[Column]
    public int $id;

    #[Column]
    public int $otherId;

    #[ManyToOne('otherId')]
    public ?Map010Fixture $other = null;
}

#[Table('map012')]
#[View('map012_view', 'select 1 as id')]
final class Map012Fixture
{
    #[Key]
    #[Column]
    public int $id;
}

#[View('map013_view', 'select 1 as version')]
final class Map013ViewFixture
{
    #[Column]
    #[Version]
    public int $version;
}

#[Statement('select 1 as id')]
final class Map013StatementFixture
{
    #[Key]
    #[Column]
    public int $id;
}

#[View('map014_view', 'select 1 as id')]
#[Index(['id'])]
final class Map014Fixture
{
    #[Column]
    public int $id;
}

#[Table('map015_unknown')]
#[Index(['missing'])]
final class Map015UnknownFixture
{
    #[Key]
    #[Column]
    public int $id;
}

#[Table('map015_leading')]
#[Index([SortOrder::Desc, 'id'])]
final class Map015LeadingFixture
{
    #[Key]
    #[Column]
    public int $id;
}

#[Table('map015_doubled')]
#[Index(['id', SortOrder::Desc, SortOrder::Asc])]
final class Map015DoubledFixture
{
    #[Key]
    #[Column]
    public int $id;
}

#[Table('map015_token')]
#[Index(['id', 42])]
final class Map015TokenFixture
{
    #[Key]
    #[Column]
    public int $id;
}

#[Table('map016')]
final class Map016Fixture
{
    #[Key]
    #[Column]
    public int $id;

    #[ManyToOne('nope')]
    public private(set) ?Map010Fixture $other = null;
}

#[Table('composite_arity_widgets')]
final class CompositeArityFixture
{
    #[Key]
    #[Generated]
    #[Column]
    public int $id;

    #[Column]
    public int $userId;

    // UserRole's key has two parts; this declares only one.
    #[ManyToOne('userId')]
    public private(set) ?UserRole $grant = null;
}

#[Statement('select 1 as id', parameters: ['bad' => 'not-a-column-type'])]
final class Map017Fixture
{
    #[Column]
    public int $id;
}

#[Table('map018')]
final class Map018Fixture
{
    #[Key]
    #[Column('same')]
    public int $id;

    #[Column('same')]
    public ?string $twin = null;
}

#[Table('map019_no_key')]
final class Map019NoKeyFixture
{
    #[Column]
    public ?string $value = null;
}

#[Table('map019_version')]
final class Map019VersionFixture
{
    #[Key]
    #[Column]
    public int $id;

    #[Column]
    #[Version]
    public ?string $version = null;
}

#[Table('map019_composite')]
final class Map019CompositeFixture
{
    #[Key]
    #[Generated]
    #[Column]
    public int $left;

    #[Key]
    #[Column]
    public int $right;
}

#[Table('map019_bare_key')]
final class Map019BareKeyFixture
{
    #[Key]
    public int $id;

    #[Column]
    public ?string $value = null;
}

#[Table('map019_enum')]
final class Map019EnumFixture
{
    #[Key]
    #[Column]
    public int $id;

    #[Column]
    #[EnumAsInt]
    public ?string $notAnEnum = null;
}

#[View('map019_empty_view', ' ')]
final class Map019EmptyViewFixture
{
    #[Column]
    public int $id;
}

#[Table('map019_navigation')]
final class Map019NavigationFixture
{
    #[Key]
    #[Generated]
    #[Column]
    public int $id;

    #[Column]
    #[OneToMany(Transaction::class, 'userId')]
    public private(set) array $children = [];
}

#[Table('map020_one_to_one')]
final class Map020OneToOneFixture
{
    #[Key]
    #[Generated]
    #[Column]
    public int $id;

    // A collection is not one-to-one.
    #[OneToOne('userId')]
    public private(set) array $child = [];
}

#[Table('map020_widgets')]
final class Map020Fixture
{
    #[Key]
    #[Generated]
    #[Column]
    public int $id;

    // Not a collection of an entity.
    #[OneToMany(User::class, 'id')]
    public private(set) string $children = '';
}

#[Table('map021_widgets')]
final class Map021Fixture
{
    #[Key]
    #[Generated]
    #[Column]
    public int $id;

    #[OneToMany(Transaction::class, 'noSuchProperty')]
    public private(set) array $children = [];
}

#[Table('map022_missing_widgets')]
final class Map022MissingFixture
{
    #[Key]
    #[Generated]
    #[Column]
    public int $id;

    // UserRole's #[ForeignKey] declarations reference User and Role — neither side is this type.
    #[ManyToMany(Role::class, through: UserRole::class)]
    public private(set) array $roles = [];
}

#[Table('map022_links')]
final class AmbiguousLink
{
    #[Key]
    #[Column]
    #[ForeignKey(Map022AmbiguousFixture::class)]
    public int $widgetId;

    #[Key]
    #[Column]
    #[ForeignKey(Map022AmbiguousFixture::class)]
    public int $otherWidgetId;

    #[Column]
    #[ForeignKey(Role::class)]
    public int $roleId;
}

#[Table('map022_ambiguous_widgets')]
final class Map022AmbiguousFixture
{
    // Single-part key, but the link declares two [ForeignKey]s to this type:
    // the FK count must equal the key arity (ADR-0019 add.1).
    #[Key]
    #[Generated]
    #[Column]
    public int $id;

    #[ManyToMany(Role::class, through: AmbiguousLink::class)]
    public private(set) array $roles = [];
}

#[Table('map011_collection')]
final class Map011CollectionFixture
{
    #[Key]
    #[Generated]
    #[Column]
    public int $id;

    #[OneToMany(Transaction::class, 'userId')]
    public array $children = [];
}

#[Statement('select 1 as id where x = @mystery')]
final class Prm010Fixture
{
    #[Column]
    public int $id;
}

#[View('prm010_view', 'select 1 as id where x = @oops')]
final class Prm010ViewFixture
{
    #[Column]
    public int $id;
}

#[Statement('select 1 as id', parameters: ['unused' => ColumnType::Int32])]
final class Prm011Fixture
{
    #[Column]
    public int $id;
}

#[Table('multi_error')]
final class MultiErrorFixture
{
    #[Column]
    public ?string $value = null;

    public ?string $forgotten = null;
}
