<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Parameters;

use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use SimpleOrm\Errors\SimpleOrmException;
use SimpleOrm\Mapping\TypeConverter;
use SimpleOrm\Mapping\TypeHandlerRegistry;
use SimpleOrm\Parameters\ParameterBinder;
use SimpleOrm\Parameters\SqlPlaceholders;

/**
 * The placeholder scan and the binding plan are computed once per SQL text (ADR-0048 item 2), and a cached plan judges
 * every call exactly as the uncached binder did: the same errors in the same order, the same expansion.
 */
final class BindingPlanCacheTest extends TestCase
{
    #[Test]
    public function the_scan_and_the_plan_run_once_per_text_and_args_class(): void
    {
        $converter = new TypeConverter(new TypeHandlerRegistry());
        $sql = 'select id from users where id = @id and name <> \'@id\' -- @nope ' . bin2hex(random_bytes(4));
        $args = new class (7) {
            public function __construct(public readonly int $id)
            {
            }
        };

        $scans = SqlPlaceholders::scanCount();
        $plans = ParameterBinder::planCount();
        $first = ParameterBinder::bind($sql, $args, 'q', $converter);
        $second = ParameterBinder::bind($sql, $args, 'q', $converter);

        self::assertSame(1, SqlPlaceholders::scanCount() - $scans);
        self::assertSame(1, ParameterBinder::planCount() - $plans);
        self::assertEquals($first, $second);
        self::assertSame(['id' => 7], $second->parameters);
        self::assertStringContainsString("id = :id and name <> '@id' -- @nope", $second->sql);
    }

    #[Test]
    public function a_cached_plan_refuses_every_call_in_the_same_order(): void
    {
        $converter = new TypeConverter(new TypeHandlerRegistry());
        $unmatched = new class (1) {
            public function __construct(public readonly int $id)
            {
            }
        };
        $unused = new class (1, 2) {
            public function __construct(public readonly int $id, public readonly int $extra)
            {
            }
        };

        foreach ([1, 2] as $call) {
            self::assertSame('PRM-001', self::codeOf(fn () => ParameterBinder::bind('select @id, @nope', $unmatched, 'q', $converter)), "call {$call}");
            self::assertSame('PRM-002', self::codeOf(fn () => ParameterBinder::bind('select @id', $unused, 'q', $converter)), "call {$call}");
        }
    }

    #[Test]
    public function in_lists_of_different_lengths_expand_from_one_plan(): void
    {
        $converter = new TypeConverter(new TypeHandlerRegistry());
        $sql = 'select id from users where id in (@ids) or owner in (@ids)';
        $args = static fn (array $ids): object => new class ($ids) {
            public function __construct(public readonly array $ids)
            {
            }
        };

        self::assertSame(
            'select id from users where id in (:ids_0, :ids_1) or owner in (:ids_0, :ids_1)',
            ParameterBinder::bind($sql, $args([1, 2]), 'q', $converter)->sql,
        );
        self::assertSame(
            'select id from users where id in (NULL) or owner in (NULL)',
            ParameterBinder::bind($sql, $args([]), 'q', $converter)->sql,
        );
        self::assertSame(
            'select id from users where id in (:ids_0) or owner in (:ids_0)',
            ParameterBinder::bind($sql, $args([3]), 'q', $converter)->sql,
        );
    }

    private static function codeOf(callable $call): ?string
    {
        try {
            $call();
        } catch (SimpleOrmException $e) {
            return $e->errorCode;
        }

        return null;
    }
}
