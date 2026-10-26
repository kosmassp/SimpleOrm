<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Parameters;

use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use SimpleOrm\Parameters\SqlPlaceholders;

/**
 * Mirrors dotnet's `SqlPlaceholders` behavior byte-for-byte: placeholders inside
 * string literals and SQL line/block comments are never real, for detection,
 * occurrence spans, and PDO rewriting alike.
 */
final class SqlPlaceholdersTest extends TestCase
{
    #[Test]
    public function find_returns_distinct_names_in_first_occurrence_order(): void
    {
        self::assertSame(
            ['Id', 'Name'],
            SqlPlaceholders::find('select * from users where id = @Id and (name = @Name or id = @Id)'),
        );
    }

    #[Test]
    public function find_ignores_lookalikes_inside_string_literals(): void
    {
        self::assertSame(['Ids'], SqlPlaceholders::find("select * from users where email like '%@ids' and id in (@Ids)"));
    }

    #[Test]
    public function find_ignores_lookalikes_inside_line_comments(): void
    {
        self::assertSame(['Ids'], SqlPlaceholders::find("select * from users -- narrows by @ids\nwhere id in (@Ids)"));
    }

    #[Test]
    public function find_ignores_lookalikes_inside_block_comments(): void
    {
        self::assertSame(['Ids'], SqlPlaceholders::find('select * from users /* @ids not real */ where id in (@Ids)'));
    }

    #[Test]
    public function find_treats_a_doubled_quote_as_an_escaped_quote_inside_the_literal(): void
    {
        self::assertSame(['Id'], SqlPlaceholders::find("select 'it''s @not a param' as x where id = @Id"));
    }

    #[Test]
    public function occurrences_returns_every_real_span_case_insensitively(): void
    {
        $sql = 'select * from users where id = @Id or id = @id';
        $spans = SqlPlaceholders::occurrences($sql, 'ID');

        self::assertCount(2, $spans);
        foreach ($spans as [$start, $length]) {
            self::assertMatchesRegularExpression('/^@id$/i', substr($sql, $start, $length));
        }
    }

    #[Test]
    public function occurrences_excludes_a_lookalike_inside_a_literal(): void
    {
        $sql = "select '@id' as literal, id from users where id = @id";
        $spans = SqlPlaceholders::occurrences($sql, 'id');

        self::assertCount(1, $spans);
        [$start, $length] = $spans[0];
        self::assertSame('@id', substr($sql, $start, $length));
        self::assertGreaterThan(strpos($sql, "'@id'"), $start);
    }

    #[Test]
    public function to_pdo_rewrites_every_real_placeholder_to_colon_syntax(): void
    {
        self::assertSame(
            'select * from users where id = :Id and name = :Name',
            SqlPlaceholders::toPdo('select * from users where id = @Id and name = @Name'),
        );
    }

    #[Test]
    public function to_pdo_leaves_literals_and_comments_untouched(): void
    {
        self::assertSame(
            "select id from users where email like '%@ids' -- narrows by @ids\n and id in (:ids)",
            SqlPlaceholders::toPdo("select id from users where email like '%@ids' -- narrows by @ids\n and id in (@ids)"),
        );
    }

    #[Test]
    public function to_pdo_preserves_length_equality_so_repeated_names_do_not_drift(): void
    {
        self::assertSame(
            'select :a, :b, :a from t',
            SqlPlaceholders::toPdo('select @a, @b, @a from t'),
        );
    }

    #[Test]
    public function a_system_function_is_not_a_placeholder(): void
    {
        // SQL Server's generated writes end in `select @@rowcount` (ADR-0040 add.3): its second `@` is not a name.
        $sql = 'set nocount on; update t set a = @a where id = @id; select @@rowcount; set nocount off';

        self::assertSame(['a', 'id'], SqlPlaceholders::find($sql));
        self::assertSame(
            'set nocount on; update t set a = :a where id = :id; select @@rowcount; set nocount off',
            SqlPlaceholders::toPdo($sql),
        );
    }

    #[Test]
    public function prefixed_renames_every_real_placeholder_and_only_those(): void
    {
        self::assertSame(
            "select @p3_a, @p3_b, @p3_a, '@a', @@rowcount from t -- @a\n where id = @p3_id",
            SqlPlaceholders::prefixed("select @a, @b, @a, '@a', @@rowcount from t -- @a\n where id = @id", 'p3_'),
        );
    }
}
