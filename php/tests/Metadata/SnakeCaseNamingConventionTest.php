<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Metadata;

use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use SimpleOrm\Naming\SnakeCaseNamingConvention;

/**
 * These vectors ARE the spec of the default convention (spec/metadata-model.md
 * "Naming convention"): every port must produce these database names from its
 * own idiom. PHP's `NamingConvention` has one method (`toDatabase`) for both
 * property-to-column and class-to-table translation.
 */
final class SnakeCaseNamingConventionTest extends TestCase
{
    private SnakeCaseNamingConvention $convention;

    protected function setUp(): void
    {
        $this->convention = new SnakeCaseNamingConvention();
    }

    /** @return list<array{0: string, 1: string}> */
    public static function vectors(): array
    {
        return [
            ['Name', 'name'],
            ['UserId', 'user_id'],
            ['UserID', 'user_id'],
            ['APIKey', 'api_key'],
            ['HTMLParser', 'html_parser'],
            ['Address2', 'address2'],
            ['Address2B', 'address2_b'],
            ['CreatedAtUtc', 'created_at_utc'],
            ['ID', 'id'],
            ['camelCase', 'camel_case'],
            ['already_snake', 'already_snake'],
        ];
    }

    #[Test]
    #[DataProvider('vectors')]
    public function names_follow_the_pinned_vectors(string $input, string $expected): void
    {
        self::assertSame($expected, $this->convention->toDatabase($input));
    }

    #[Test]
    public function table_names_are_snake_case_without_pluralization(): void
    {
        self::assertSame('transaction_detail', $this->convention->toDatabase('TransactionDetail'));
    }

    #[Test]
    public function empty_name_stays_empty(): void
    {
        self::assertSame('', $this->convention->toDatabase(''));
    }
}
