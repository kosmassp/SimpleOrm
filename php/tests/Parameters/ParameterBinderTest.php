<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Parameters;

use ArrayIterator;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use SimpleOrm\Errors\SimpleOrmException;
use SimpleOrm\Mapping\TypeConverter;
use SimpleOrm\Mapping\TypeHandlerRegistry;
use SimpleOrm\Parameters\BoundSql;
use SimpleOrm\Parameters\ParameterBinder;

/**
 * `ParameterBinder::bind` (§7.12/§7.13, mirrors dotnet's `ParameterBinder`):
 * PRM-001/002 both directions, IN-list expansion (including the empty-list-is-
 * NULL rule and leaving literals/comments alone), and `toPdo` rewriting in the
 * returned {@see BoundSql}. Args fixtures are anonymous classes, scoped to this
 * file (CODING-STANDARD §7: test-local, never in Sample).
 */
final class ParameterBinderTest extends TestCase
{
    private TypeConverter $converter;

    protected function setUp(): void
    {
        $this->converter = new TypeConverter(new TypeHandlerRegistry());
    }

    #[Test]
    public function a_placeholder_without_a_matching_property_is_prm_001(): void
    {
        $args = new class (1) {
            public function __construct(public readonly int $id)
            {
            }
        };

        $exception = $this->assertThrowsSimpleOrm(
            fn () => ParameterBinder::bind('select * from users where id = @Nope', $args, 'q', $this->converter),
        );
        self::assertSame('PRM-001', $exception->errorCode);
    }

    #[Test]
    public function a_property_never_used_by_the_sql_is_prm_002(): void
    {
        $args = new class (1) {
            public function __construct(public readonly int $id)
            {
            }
        };

        $exception = $this->assertThrowsSimpleOrm(
            fn () => ParameterBinder::bind('select * from users', $args, 'q', $this->converter),
        );
        self::assertSame('PRM-002', $exception->errorCode);
    }

    #[Test]
    public function a_matching_placeholder_binds_case_insensitively(): void
    {
        $args = new class (7) {
            public function __construct(public readonly int $id)
            {
            }
        };

        $bound = ParameterBinder::bind('select * from users where id = @id', $args, 'q', $this->converter);

        self::assertSame('select * from users where id = :id', $bound->sql);
        self::assertSame(['id' => 7], $bound->parameters);
    }

    #[Test]
    public function a_collection_property_expands_to_generated_placeholders(): void
    {
        $args = new class ([1, 2, 3]) {
            /** @param list<int> $ids */
            public function __construct(public readonly array $ids)
            {
            }
        };

        $bound = ParameterBinder::bind('select * from users where id in (@Ids)', $args, 'q', $this->converter);

        self::assertSame('select * from users where id in (:Ids_0, :Ids_1, :Ids_2)', $bound->sql);
        self::assertSame(['Ids_0' => 1, 'Ids_1' => 2, 'Ids_2' => 3], $bound->parameters);
    }

    #[Test]
    public function an_empty_collection_renders_sql_null_and_binds_nothing(): void
    {
        $args = new class ([]) {
            /** @param list<int> $ids */
            public function __construct(public readonly array $ids)
            {
            }
        };

        $bound = ParameterBinder::bind('select * from users where id in (@Ids)', $args, 'q', $this->converter);

        self::assertSame('select * from users where id in (NULL)', $bound->sql);
        self::assertSame([], $bound->parameters);
    }

    #[Test]
    public function in_expansion_leaves_a_lookalike_inside_a_literal_or_comment_alone(): void
    {
        $args = new class ([9]) {
            /** @param list<int> $ids */
            public function __construct(public readonly array $ids)
            {
            }
        };

        $sql = "select * from users where email like '%@ids' -- narrows by @ids\n and id in (@ids)";
        $bound = ParameterBinder::bind($sql, $args, 'q', $this->converter);

        self::assertSame(
            "select * from users where email like '%@ids' -- narrows by @ids\n and id in (:ids_0)",
            $bound->sql,
        );
        self::assertSame(['ids_0' => 9], $bound->parameters);
    }

    #[Test]
    public function array_parameters_mode_binds_the_converted_list_as_one_parameter(): void
    {
        $args = new class ([1, 2]) {
            /** @param list<int> $ids */
            public function __construct(public readonly array $ids)
            {
            }
        };

        $bound = ParameterBinder::bind(
            'select * from users where id = any(@ids)',
            $args,
            'q',
            $this->converter,
            arrayParameters: true,
        );

        self::assertSame('select * from users where id = any(:ids)', $bound->sql);
        self::assertSame(['ids' => [1, 2]], $bound->parameters);
    }

    #[Test]
    public function a_traversable_property_is_treated_as_a_collection(): void
    {
        $args = new class (new ArrayIterator([4, 5])) {
            public function __construct(public readonly ArrayIterator $ids)
            {
            }
        };

        $bound = ParameterBinder::bind('select * from users where id in (@Ids)', $args, 'q', $this->converter);

        self::assertSame('select * from users where id in (:Ids_0, :Ids_1)', $bound->sql);
        self::assertSame(['Ids_0' => 4, 'Ids_1' => 5], $bound->parameters);
    }

    #[Test]
    public function a_string_value_is_never_treated_as_a_collection(): void
    {
        $args = new class ('Ada') {
            public function __construct(public readonly string $name)
            {
            }
        };

        $bound = ParameterBinder::bind(
            "select * from users where name = @name and email like '%domain%'",
            $args,
            'q',
            $this->converter,
        );

        self::assertSame(['name' => 'Ada'], $bound->parameters);
    }

    private function assertThrowsSimpleOrm(callable $callback): SimpleOrmException
    {
        try {
            $callback();
        } catch (SimpleOrmException $exception) {
            return $exception;
        }

        self::fail('expected a SimpleOrmException');
    }
}
