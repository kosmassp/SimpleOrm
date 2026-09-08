<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Metadata;

use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use SimpleOrm\Metadata\EntityIndex;
use SimpleOrm\Metadata\EntityMap;
use SimpleOrm\Metadata\EntityMapBuilder;
use SimpleOrm\Metadata\EntityMapLoader;
use SimpleOrm\Metadata\KeyStrategy;
use SimpleOrm\Metadata\MappingOptions;
use SimpleOrm\Metadata\PropertyMap;
use SimpleOrm\Metadata\RelationKind;
use SimpleOrm\Metadata\RelationshipKind;
use SimpleOrm\Metadata\RelationshipMap;
use SimpleOrm\Tests\Sample\Models\DailySales;
use SimpleOrm\Tests\Sample\Models\MonthlySalesTotal;
use SimpleOrm\Tests\Sample\Models\Role;
use SimpleOrm\Tests\Sample\Models\Transaction;
use SimpleOrm\Tests\Sample\Models\TransactionDetail;
use SimpleOrm\Tests\Sample\Models\User;
use SimpleOrm\Tests\Sample\Models\UserActivityReport;
use SimpleOrm\Tests\Sample\Models\UserProfile;
use SimpleOrm\Tests\Sample\Models\UserRole;
use SimpleOrm\Tests\Sample\Models\UserTransactionTotal;

/**
 * Happy-path metadata loading across the sample models: loader precedence
 * (explicit → attribute → convention), caching, key strategies, and the
 * inherited-property ordering rule (spec/metadata-model.md "Loader
 * precedence": most-derived class first, base classes' columns last).
 */
final class EntityMapLoaderTest extends TestCase
{
    private EntityMapLoader $loader;

    protected function setUp(): void
    {
        $this->loader = new EntityMapLoader();
    }

    #[Test]
    public function user_maps_table_generated_key_and_inherited_audit_columns(): void
    {
        $map = $this->loader->load(User::class);

        self::assertSame(RelationKind::Table, $map->kind);
        self::assertSame('users', $map->relationName);
        self::assertSame(KeyStrategy::DatabaseGenerated, $map->keyStrategy);
        self::assertSame(['id'], array_map(self::columnName(...), $map->keyProperties));

        // Derived class columns first, BaseModel audit columns last.
        self::assertSame(
            ['id', 'name', 'email', 'display_name', 'created_at', 'updated_at'],
            array_map(self::columnName(...), $map->properties),
        );
        self::assertFalse($map->property('name')?->nullable);
        self::assertTrue($map->property('updatedAtUtc')?->nullable);

        self::assertCount(2, $map->indexes);
        self::assertTrue(self::indexNamed($map, 'ix_users_email')->unique);
        self::assertFalse(self::indexNamed($map, 'ix_users_display_name')->unique);
    }

    #[Test]
    public function user_role_maps_composite_natural_key_and_two_relationships(): void
    {
        $map = $this->loader->load(UserRole::class);

        self::assertSame(KeyStrategy::Natural, $map->keyStrategy);
        self::assertSame(['user_id', 'role_id'], array_map(self::columnName(...), $map->keyProperties));
        self::assertCount(2, $map->relationships);

        $user = self::relationshipNamed($map, 'user');
        self::assertSame(User::class, $user->targetType);
        $role = self::relationshipNamed($map, 'role');
        self::assertSame(['roleId'], $role->foreignKeyProperties);
    }

    #[Test]
    public function transaction_maps_version_indexes_and_foreign_key(): void
    {
        $map = $this->loader->load(Transaction::class);

        self::assertSame('version', $map->versionProperty?->columnName);
        self::assertSame(User::class, $map->property('userId')?->foreignKeyReferences);

        self::assertCount(2, $map->indexes);
        self::assertSame('ix_transactions_user_id', $map->indexes[0]->name);
        $named = $map->indexes[1];
        self::assertSame('ix_transactions_status_created', $named->name);
        self::assertSame([false, true], array_map(static fn ($c) => $c->descending, $named->columns));

        $manyToOne = self::relationshipNamed($map, 'user');
        self::assertSame(RelationshipKind::ManyToOne, $manyToOne->kind);
        self::assertSame(['userId'], $manyToOne->foreignKeyProperties);

        $details = self::relationshipNamed($map, 'details');
        self::assertSame(RelationshipKind::OneToMany, $details->kind);
        self::assertSame(TransactionDetail::class, $details->targetType);
    }

    #[Test]
    public function view_and_materialized_view_map_with_their_capabilities(): void
    {
        $view = $this->loader->load(UserTransactionTotal::class);
        self::assertSame(RelationKind::View, $view->kind);
        self::assertSame(KeyStrategy::Natural, $view->keyStrategy);
        self::assertSame([], $view->indexes);

        $materialized = $this->loader->load(MonthlySalesTotal::class);
        self::assertSame(RelationKind::MaterializedView, $materialized->kind);
        self::assertCount(1, $materialized->indexes);
        self::assertTrue($materialized->indexes[0]->unique);
    }

    #[Test]
    public function statement_maps_sql_and_declared_parameters(): void
    {
        $map = $this->loader->load(DailySales::class);

        self::assertSame(RelationKind::Statement, $map->kind);
        self::assertNull($map->relationName);
        self::assertStringContainsString('@since', (string) $map->definingSql);
        self::assertSame(KeyStrategy::None, $map->keyStrategy);

        self::assertCount(1, $map->statementParameters);
        self::assertSame('since', $map->statementParameters[0]->name);
    }

    #[Test]
    public function procedure_maps_keyless_and_nullable_columns(): void
    {
        $map = $this->loader->load(UserActivityReport::class);

        self::assertSame(RelationKind::Procedure, $map->kind);
        self::assertSame('user_activity_report', $map->relationName);
        self::assertSame(KeyStrategy::None, $map->keyStrategy);
        self::assertStringContainsString('@since', (string) $map->definingSql);
        self::assertSame('since', $map->statementParameters[0]->name);
        self::assertTrue($map->property('lastTransactionAtUtc')?->nullable);
    }

    #[Test]
    public function role_and_user_profile_declare_the_remaining_navigation_kinds(): void
    {
        $role = $this->loader->load(Role::class);
        $manyToMany = self::relationshipNamed($role, 'users');
        self::assertSame(RelationshipKind::ManyToMany, $manyToMany->kind);
        self::assertSame(User::class, $manyToMany->targetType);
        self::assertSame(UserRole::class, $manyToMany->linkType);
        self::assertSame(['roleId'], $manyToMany->linkForeignKeysToOwner);
        self::assertSame(['userId'], $manyToMany->linkForeignKeysToTarget);

        $user = $this->loader->load(User::class);
        $profile = self::relationshipNamed($user, 'profile');
        self::assertSame(RelationshipKind::OneToOne, $profile->kind);
        self::assertSame(UserProfile::class, $profile->targetType);
        self::assertSame(['userId'], $profile->foreignKeyProperties);
    }

    #[Test]
    public function maps_are_cached_per_loader(): void
    {
        self::assertSame($this->loader->load(User::class), $this->loader->load(User::class));
    }

    #[Test]
    public function convention_loader_maps_an_unannotated_type(): void
    {
        $map = $this->loader->load(Person::class);

        self::assertSame(RelationKind::Table, $map->kind);
        self::assertSame('person', $map->relationName);
        self::assertSame(KeyStrategy::DatabaseGenerated, $map->keyStrategy);
        self::assertSame(['id', 'first_name'], array_map(self::columnName(...), $map->properties));
        self::assertTrue($map->property('firstName')?->nullable);
    }

    #[Test]
    public function explicit_registration_wins_over_attributes(): void
    {
        $builder = EntityMapBuilder::for(User::class)->table('users_manual');
        $builder->column('id', key: true, generated: true);
        $builder->column('name');

        $options = new MappingOptions(explicitMaps: [User::class => $builder->build()]);
        $map = (new EntityMapLoader($options))->load(User::class);

        self::assertSame('users_manual', $map->relationName);
        self::assertCount(2, $map->properties);
    }

    private static function columnName(PropertyMap $property): string
    {
        return $property->columnName;
    }

    private static function indexNamed(EntityMap $map, string $name): EntityIndex
    {
        foreach ($map->indexes as $index) {
            if ($index->name === $name) {
                return $index;
            }
        }

        self::fail("no index named '{$name}'");
    }

    private static function relationshipNamed(
        EntityMap $map,
        string $propertyName,
    ): RelationshipMap {
        foreach ($map->relationships as $relationship) {
            if ($relationship->propertyName === $propertyName) {
                return $relationship;
            }
        }

        self::fail("no relationship for property '{$propertyName}'");
    }
}

// Deliberately no SimpleOrm attributes: the convention-loader fixture.
final class Person
{
    public int $id;

    public ?string $firstName = null;
}
