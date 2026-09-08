<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Metadata;

use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use SimpleOrm\Errors\MappingException;
use SimpleOrm\Errors\SimpleOrmException;
use SimpleOrm\Metadata\Attributes\Column;
use SimpleOrm\Metadata\Attributes\Generated;
use SimpleOrm\Metadata\Attributes\Key;
use SimpleOrm\Metadata\Attributes\Owned;
use SimpleOrm\Metadata\Attributes\Table;
use SimpleOrm\Metadata\EntityMapJson;
use SimpleOrm\Metadata\EntityMapLoader;
use SimpleOrm\Query\Criteria;
use SimpleOrm\Session\Db;
use SimpleOrm\Tests\Sample\Models\Address;
use SimpleOrm\Tests\Sample\Models\Role;
use SimpleOrm\Tests\Sample\Models\UserProfile;
use SimpleOrm\Tests\Support\SampleDatabase;
use SimpleOrm\Tests\Support\TempDatabase;

/**
 * Owned value types (ADR-0030): a class-level #[Owned] type whose #[Column]
 * members flatten into the owner's table under a prefix; read back as one
 * instance (or null when every member column is NULL and the navigation is
 * nullable); written, filtered, and column-listed through dotted property
 * paths. Mirrors dotnet's `OwnedTypesTests`.
 */
final class OwnedTypesTest extends TestCase
{
    private TempDatabase $fixture;

    private Db $db;

    protected function setUp(): void
    {
        $this->fixture = TempDatabase::create();
        $this->db = SampleDatabase::open($this->fixture);
    }

    protected function tearDown(): void
    {
        $this->db->close();
        $this->fixture->delete();
    }

    private static function newProfile(int $userId, ?Address $address): UserProfile
    {
        $profile = new UserProfile();
        $profile->userId = $userId;
        $profile->bio = 'bio';
        $profile->address = $address;
        $profile->createdAtUtc = SampleDatabase::seedTime();

        return $profile;
    }

    private static function address(string $street, string $city): Address
    {
        $address = new Address();
        $address->street = $street;
        $address->city = $city;

        return $address;
    }

    #[Test]
    public function loader_flattens_members_under_the_navigation_prefix(): void
    {
        $map = (new EntityMapLoader())->load(UserProfile::class);

        self::assertCount(1, $map->ownedTypes);
        $owned = $map->ownedTypes[0];
        self::assertSame('address', $owned->propertyName());
        self::assertSame('address_', $owned->prefix);
        self::assertTrue($owned->nullable);
        self::assertSame(
            ['address.street', 'address.city', 'address.postalCode'],
            array_map(static fn ($m) => $m->propertyName(), $owned->members()),
        );
        self::assertSame(
            ['address_street', 'address_city', 'address_postal_code'],
            array_map(static fn ($m) => $m->columnName, $owned->members()),
        );
        foreach ($owned->members() as $member) {
            self::assertTrue($member->nullable);   // a nullable navigation makes every member nullable
            self::assertContains($member, $map->properties);   // the same instances appear in the owner's list
        }
    }

    #[Test]
    public function round_trips_an_address_and_reads_an_all_null_segment_as_no_address(): void
    {
        $ada = SampleDatabase::insertUser($this->db, 'Ada', 'ada@example.com');
        $grace = SampleDatabase::insertUser($this->db, 'Grace', 'grace@example.com');

        $withAddress = self::newProfile($ada->id, self::address('1 Main St', 'Paris'));
        $this->db->insert($withAddress);
        $without = self::newProfile($grace->id, null);
        $this->db->insert($without);

        $loaded = $this->db->get(UserProfile::class, $withAddress->id);
        self::assertNotNull($loaded->address);
        self::assertSame('1 Main St', $loaded->address->street);
        self::assertSame('Paris', $loaded->address->city);
        self::assertNull($loaded->address->postalCode);

        $none = $this->db->get(UserProfile::class, $without->id);
        self::assertNull($none->address);   // all three columns NULL → no instance
    }

    #[Test]
    public function criteria_and_column_lists_use_the_dotted_path_and_the_navigation_name(): void
    {
        $ada = SampleDatabase::insertUser($this->db, 'Ada', 'ada@example.com');
        $profile = self::newProfile($ada->id, self::address('1 Main St', 'Paris'));
        $this->db->insert($profile);

        $found = $this->db->from(UserProfile::class)
            ->where(Criteria::eq('address.city', 'Paris'))
            ->orderBy('address.street')
            ->toList();
        self::assertCount(1, $found);
        self::assertSame($profile->id, $found[0]->id);

        $profile->address->city = 'Lyon';
        $profile->address->postalCode = '69001';
        $profile->bio = 'not written';
        $this->db->updateOnly($profile, ['address']);   // the navigation name expands to its members

        $loaded = $this->db->get(UserProfile::class, $profile->id);
        self::assertSame('Lyon', $loaded->address?->city);
        self::assertSame('69001', $loaded->address?->postalCode);
        self::assertSame('bio', $loaded->bio);

        try {
            $this->db->updateOnly($profile, ['address', 'address.city']);
            self::fail('expected CRUD-007');
        } catch (SimpleOrmException $e) {
            self::assertSame('CRUD-007', $e->errorCode);
        }

        try {
            $this->db->from(UserProfile::class)->where(Criteria::eq('address.nope', 'x'))->toList();
            self::fail('expected QRY-006');
        } catch (SimpleOrmException $e) {
            self::assertSame('QRY-006', $e->errorCode);
        }
    }

    #[Test]
    public function ddl_from_metadata_and_export_carry_the_flattened_columns(): void
    {
        $this->db->createTable(Parcel::class);

        $parcel = new Parcel();
        $parcel->label = 'box';
        $parcel->origin = new Point();
        $parcel->origin->x = 1;
        $parcel->origin->y = 2;
        $parcel->destination = new Point();
        $parcel->destination->x = 3;
        $parcel->destination->y = 4;
        $this->db->insert($parcel);

        $loaded = $this->db->get(Parcel::class, $parcel->id);
        self::assertSame([1, 2], [$loaded->origin->x, $loaded->origin->y]);
        self::assertSame([3, 4], [$loaded->destination->x, $loaded->destination->y]);

        $map = $this->db->maps()->load(Parcel::class);
        self::assertSame(['id', 'label', 'from_x', 'from_y', 'x', 'y'], array_map(static fn ($p) => $p->columnName, $map->properties));
        self::assertFalse($map->property('origin.x')?->nullable);   // a required navigation keeps member nullability

        $json = EntityMapJson::export($map, $this->db->maps());
        self::assertStringContainsString('"column": "from_x"', $json);
        self::assertStringNotContainsString('origin', $json);   // the export is column-centric: no owned structure
    }

    #[Test]
    public function loader_refuses_invalid_owned_declarations(): void
    {
        $codesOf = static function (string $type): array {
            try {
                (new EntityMapLoader())->load($type);
                self::fail("expected a mapping failure for {$type}");
            } catch (MappingException $e) {
                return array_map(static fn ($error) => $error->code, $e->errors);
            }
        };

        self::assertContains('MAP-024', $codesOf(OwnsAnEntity::class));       // the owned type carries #[Table]
        self::assertContains('MAP-024', $codesOf(OwnsUndeclared::class));     // the owned type lacks the class-level #[Owned]
        self::assertContains('MAP-024', $codesOf(OwnsAKeyedType::class));     // a #[Key] inside the owned type
        self::assertContains('MAP-024', $codesOf(OwnsANestedOwned::class));   // nested #[Owned]
        self::assertContains('MAP-024', $codesOf(OwnsAScalar::class));        // a scalar navigation
        self::assertContains('MAP-024', $codesOf(OwnsNothingMapped::class));  // no #[Column] member
        self::assertContains('MAP-019', $codesOf(OwnedAndColumn::class));     // #[Owned] combined with #[Column]
        self::assertContains('MAP-018', $codesOf(PrefixCollision::class));    // an owned column collides with a direct one
        self::assertContains('MAP-024', $codesOf(Point::class));              // an owned type loaded as if it were an entity
    }
}

// --- fixtures (test-local, never in Sample) -----------------------------------------

#[Owned]
final class Point
{
    #[Column]
    public int $x = 0;

    #[Column]
    public int $y = 0;
}

#[Table('parcels')]
final class Parcel
{
    #[Key]
    #[Generated]
    #[Column]
    public int $id;

    #[Column]
    public string $label;

    #[Owned(prefix: 'from_')]
    public Point $origin;

    #[Owned(prefix: '')]
    public Point $destination;
}

#[Table('t1')]
final class OwnsAnEntity
{
    #[Key]
    #[Column]
    public int $id;

    #[Owned]
    public ?Role $role = null;
}

final class Undeclared
{
    #[Column]
    public ?string $name = null;
}

#[Table('t2')]
final class OwnsUndeclared
{
    #[Key]
    #[Column]
    public int $id;

    #[Owned]
    public ?Undeclared $value = null;
}

#[Owned]
final class Keyed
{
    #[Key]
    #[Column]
    public int $id;
}

#[Table('t3')]
final class OwnsAKeyedType
{
    #[Key]
    #[Column]
    public int $id;

    #[Owned]
    public ?Keyed $value = null;
}

#[Owned]
final class Nested
{
    #[Column]
    public ?string $name = null;

    #[Owned]
    public ?Point $inner = null;
}

#[Table('t4')]
final class OwnsANestedOwned
{
    #[Key]
    #[Column]
    public int $id;

    #[Owned]
    public ?Nested $value = null;
}

#[Table('t5')]
final class OwnsAScalar
{
    #[Key]
    #[Column]
    public int $id;

    #[Owned]
    public ?string $value = null;
}

#[Owned]
final class Empty_
{
    public ?string $notMapped = null;
}

#[Table('t6')]
final class OwnsNothingMapped
{
    #[Key]
    #[Column]
    public int $id;

    #[Owned]
    public ?Empty_ $value = null;
}

#[Table('t7')]
final class OwnedAndColumn
{
    #[Key]
    #[Column]
    public int $id;

    #[Owned]
    #[Column]
    public ?Point $value = null;
}

#[Table('t8')]
final class PrefixCollision
{
    #[Key]
    #[Column]
    public int $id;

    #[Column('x')]
    public int $direct = 0;

    #[Owned(prefix: '')]
    public ?Point $value = null;
}
