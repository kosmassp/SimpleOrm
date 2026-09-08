<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Mapping;

use DateTimeImmutable;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use SimpleOrm\Errors\SimpleOrmException;
use SimpleOrm\Mapping\ResultMapper;
use SimpleOrm\Mapping\TypeConverter;
use SimpleOrm\Mapping\TypeHandlerRegistry;
use SimpleOrm\Metadata\EntityMapLoader;
use SimpleOrm\Tests\Sample\Models\Transaction;
use SimpleOrm\Tests\Sample\Models\User;

/**
 * The one row-mapping pipeline (§7.11/spec/mapping-rules.md), tested directly
 * against hand-built rows (no database: `ResultMapper` maps arrays to objects
 * and touches nothing else) — mirrors dotnet's `MappingErrorTests` for the
 * row-mapping codes (`MAP-001/002/003/031`, as opposed to the loader's
 * `MAP-010..019`, already covered by `tests/Metadata/MappingErrorTest.php`)
 * plus the §7.8 naming-convention matching for DTOs.
 */
final class ResultMapperTest extends TestCase
{
    private function mapper(): ResultMapper
    {
        return new ResultMapper(new EntityMapLoader(), new TypeConverter(new TypeHandlerRegistry()));
    }

    #[Test]
    public function entity_column_with_no_mapped_property_is_map001(): void
    {
        try {
            $this->mapper()->createPlan(
                User::class,
                ['id', 'name', 'email', 'display_name', 'created_at', 'updated_at', 'mystery'],
                'test',
            );
            self::fail('expected MAP-001');
        } catch (SimpleOrmException $e) {
            self::assertSame('MAP-001', $e->errorCode);
            self::assertStringContainsString('mystery', $e->getMessage());
        }
    }

    #[Test]
    public function entity_missing_a_mapped_column_is_map002(): void
    {
        try {
            $this->mapper()->createPlan(User::class, ['id', 'name'], 'test');
            self::fail('expected MAP-002');
        } catch (SimpleOrmException $e) {
            self::assertSame('MAP-002', $e->errorCode);
            self::assertStringContainsString('email', $e->getMessage());
        }
    }

    #[Test]
    public function null_into_a_non_nullable_entity_property_is_map031(): void
    {
        $plan = $this->mapper()->createPlan(User::class, ['id', 'name', 'email', 'display_name', 'created_at', 'updated_at'], 'test');

        try {
            $plan(['id' => 1, 'name' => null, 'email' => 'a@example.com', 'display_name' => null,
                'created_at' => '2026-01-01T00:00:00.0000000Z', 'updated_at' => null]);
            self::fail('expected MAP-031');
        } catch (SimpleOrmException $e) {
            self::assertSame('MAP-031', $e->errorCode);
        }
    }

    #[Test]
    public function an_unknown_enum_name_is_map031(): void
    {
        $columns = ['id', 'user_id', 'status', 'amount', 'version', 'note', 'created_at', 'updated_at'];
        $plan = $this->mapper()->createPlan(Transaction::class, $columns, 'test');

        try {
            $plan([
                'id' => 1, 'user_id' => 1, 'status' => 'Nope', 'amount' => '1', 'version' => 0,
                'note' => null, 'created_at' => '2026-01-01T00:00:00Z', 'updated_at' => null,
            ]);
            self::fail('expected MAP-031');
        } catch (SimpleOrmException $e) {
            self::assertSame('MAP-031', $e->errorCode);
        }
    }

    #[Test]
    public function a_dto_constructor_binds_columns_by_exact_name(): void
    {
        $dto = new class(0, '') {
            public function __construct(
                public readonly int $id,
                public readonly string $email,
            ) {
            }
        };
        $type = $dto::class;

        $plan = $this->mapper()->createPlan($type, ['id', 'email'], 'test');
        $result = $plan(['id' => 7, 'email' => 'a@example.com']);

        self::assertSame(7, $result->id);
        self::assertSame('a@example.com', $result->email);
    }

    #[Test]
    public function a_dto_matches_columns_case_and_underscore_insensitively(): void
    {
        $dto = new class(0, new DateTimeImmutable()) {
            public function __construct(
                public readonly int $userId,
                public readonly DateTimeImmutable $createdAt,
            ) {
            }
        };
        $type = $dto::class;

        // snake_case columns (as SQL would produce) matching camelCase members (§7.8).
        $plan = $this->mapper()->createPlan($type, ['user_id', 'created_at'], 'test');
        $result = $plan(['user_id' => 3, 'created_at' => '2026-01-01T00:00:00.0000000Z']);

        self::assertSame(3, $result->userId);
        self::assertSame('2026-01-01T00:00:00+00:00', $result->createdAt->format('c'));
    }

    #[Test]
    public function a_dto_column_matching_nothing_is_map001(): void
    {
        $dto = new class(0) {
            public function __construct(public readonly int $id)
            {
            }
        };

        try {
            $this->mapper()->createPlan($dto::class, ['id', 'extra'], 'test');
            self::fail('expected MAP-001');
        } catch (SimpleOrmException $e) {
            self::assertSame('MAP-001', $e->errorCode);
        }
    }

    #[Test]
    public function a_dto_required_constructor_parameter_with_no_column_is_map002(): void
    {
        $dto = new class(0, '') {
            public function __construct(
                public readonly int $id,
                public readonly string $email,
            ) {
            }
        };

        try {
            $this->mapper()->createPlan($dto::class, ['id'], 'test');
            self::fail('expected MAP-002');
        } catch (SimpleOrmException $e) {
            self::assertSame('MAP-002', $e->errorCode);
            self::assertStringContainsString('email', $e->getMessage());
        }
    }

    #[Test]
    public function a_dto_required_property_with_no_column_is_map002(): void
    {
        $dto = new class {
            public int $id;

            public string $requiredField;
        };

        try {
            $this->mapper()->createPlan($dto::class, ['id'], 'test');
            self::fail('expected MAP-002');
        } catch (SimpleOrmException $e) {
            self::assertSame('MAP-002', $e->errorCode);
            self::assertStringContainsString('requiredField', $e->getMessage());
        }
    }

    #[Test]
    public function a_dto_optional_property_with_no_column_and_a_default_is_left_alone(): void
    {
        $dto = new class {
            public int $id;

            public ?string $optionalField = 'default';
        };

        $plan = $this->mapper()->createPlan($dto::class, ['id'], 'test');
        $result = $plan(['id' => 1]);

        self::assertSame(1, $result->id);
        self::assertSame('default', $result->optionalField);
    }

    #[Test]
    public function a_constructor_parameter_matching_more_than_one_column_is_map003(): void
    {
        $dto = new class(0) {
            public function __construct(public readonly int $userId)
            {
            }
        };

        try {
            $this->mapper()->createPlan($dto::class, ['user_id', 'userId'], 'test');
            self::fail('expected MAP-003');
        } catch (SimpleOrmException $e) {
            self::assertSame('MAP-003', $e->errorCode);
        }
    }

    #[Test]
    public function plans_are_cached_per_type_and_column_list(): void
    {
        $mapper = $this->mapper();
        $plan1 = $mapper->createPlan(User::class, ['id', 'name', 'email', 'display_name', 'created_at', 'updated_at'], 'test');
        $plan2 = $mapper->createPlan(User::class, ['id', 'name', 'email', 'display_name', 'created_at', 'updated_at'], 'test');

        self::assertSame($plan1, $plan2);
    }

    #[Test]
    public function a_scalar_result_type_reads_the_single_column(): void
    {
        $plan = $this->mapper()->createPlan('int', ['count(id)'], 'test');

        self::assertSame(5, $plan(['count(id)' => 5]));
    }
}
