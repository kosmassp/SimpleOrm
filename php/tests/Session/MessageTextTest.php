<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Session;

use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use SimpleOrm\Parameters\BinaryParameter;
use SimpleOrm\Session\MessageText;
use SimpleOrm\Session\PreparedWrite;
use SimpleOrm\Session\ResultKind;

/**
 * The multi-statement realization of a planned message (ADR-0044, spec/dialects.md "Statement batching"): the text SQL
 * Server receives and the names its values bind under, rendered without an engine — the live legs prove the round trip
 * (`SqlServerLiveTest`, skipped where no server answers). Pure text work, so these tests need no database.
 */
final class MessageTextTest extends TestCase
{
    /** @param array<string, mixed> $parameters */
    private static function write(string $sql, array $parameters, ResultKind $result = ResultKind::None): PreparedWrite
    {
        return new PreparedWrite($sql, $parameters, $result, static fn (mixed $r): int => 1, static function (): void {
        });
    }

    #[Test]
    public function each_statements_placeholders_are_renamed_apart_by_its_position(): void
    {
        [$sql, $parameters] = MessageText::render([
            self::write('insert into [users] ([name]) values (@name); select cast(scope_identity() as bigint)', ['name' => 'Ada'], ResultKind::Key),
            self::write('insert into [users] ([name]) values (@name); select cast(scope_identity() as bigint)', ['name' => 'Grace'], ResultKind::Key),
        ]);

        self::assertSame(
            "insert into [users] ([name]) values (:p0_name); select cast(scope_identity() as bigint);\n"
            . 'insert into [users] ([name]) values (:p1_name); select cast(scope_identity() as bigint)',
            $sql,
        );
        self::assertSame(['p0_name' => 'Ada', 'p1_name' => 'Grace'], $parameters);
    }

    #[Test]
    public function a_reported_count_select_and_a_literal_survive_the_renaming(): void
    {
        [$sql, $parameters] = MessageText::render([
            self::write("insert into [log] ([note]) values (N'@name is not a parameter')", []),
            self::write(
                'set nocount on; update [t] set [a] = @a where [id] = @id and [version] = @version; select @@rowcount; set nocount off',
                ['a' => 'x', 'id' => 7, 'version' => 2],
                ResultKind::Count,
            ),
        ]);

        self::assertSame(
            "insert into [log] ([note]) values (N'@name is not a parameter');\n"
            . 'set nocount on; update [t] set [a] = :p1_a where [id] = :p1_id and [version] = :p1_version; select @@rowcount; set nocount off',
            $sql,
        );
        self::assertSame(['p1_a' => 'x', 'p1_id' => 7, 'p1_version' => 2], $parameters);
    }

    #[Test]
    public function a_binary_value_keeps_its_wrapper_under_the_new_name(): void
    {
        $bytes = new BinaryParameter("\x00\xff");
        [, $parameters] = MessageText::render([
            self::write('select 1', []),
            self::write('update [p] set [avatar] = @avatar where [id] = @id', ['avatar' => $bytes, 'id' => 1], ResultKind::Count),
        ]);

        self::assertSame($bytes, $parameters['p1_avatar']);
        self::assertSame(1, $parameters['p1_id']);
    }

    #[Test]
    public function statements_are_joined_by_a_semicolon_and_a_newline_so_a_trailing_comment_cannot_reach_the_next_statement(): void
    {
        [$sql] = MessageText::render([self::write('select 1 -- trailing', []), self::write('select 2', [])]);

        self::assertSame("select 1 -- trailing;\nselect 2", $sql);   // the newline ends the comment's line
    }
}
