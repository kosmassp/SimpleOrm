<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Conformance;

use PHPUnit\Framework\Attributes\DataProvider;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use SimpleOrm\Metadata\EntityMapJson;
use SimpleOrm\Metadata\EntityMapLoader;
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
use SimpleOrm\Tests\Support\ConformancePaths;

/**
 * The conformance runner for entity metadata (CLAUDE.md §9): exports every
 * fixture entity and compares it against `conformance/entities/*.json` — the
 * files every port must reproduce byte-for-byte. CRLF is normalized before
 * comparison because a Windows git checkout can rewrite the pinned files'
 * line endings on disk; the C# reference's own conformance runner does the
 * same for the same reason.
 */
final class EntitiesTest extends TestCase
{
    /** @return array<string, array{0: string, 1: class-string}> */
    public static function entities(): array
    {
        return [
            'user' => ['user', User::class],
            'role' => ['role', Role::class],
            'user_role' => ['user_role', UserRole::class],
            'transaction' => ['transaction', Transaction::class],
            'transaction_detail' => ['transaction_detail', TransactionDetail::class],
            'user_profile' => ['user_profile', UserProfile::class],
            'user_transaction_total' => ['user_transaction_total', UserTransactionTotal::class],
            'monthly_sales_total' => ['monthly_sales_total', MonthlySalesTotal::class],
            'daily_sales' => ['daily_sales', DailySales::class],
            'user_activity_report' => ['user_activity_report', UserActivityReport::class],
        ];
    }

    #[Test]
    #[DataProvider('entities')]
    public function export_matches_the_pinned_conformance_file(string $fileName, string $entityType): void
    {
        $loader = new EntityMapLoader();
        $json = self::normalizeNewlines(EntityMapJson::export($loader->load($entityType), $loader));

        $path = ConformancePaths::dir('entities') . DIRECTORY_SEPARATOR . $fileName . '.json';
        self::assertFileExists($path, "missing conformance file {$path}");
        $expected = rtrim(self::normalizeNewlines((string) file_get_contents($path)), "\n");

        self::assertSame($expected, $json);
    }

    /** Every file conformance/entities/ actually has is covered by a case, so a stray fixture is caught too. */
    #[Test]
    public function every_conformance_entity_file_has_a_case(): void
    {
        $covered = array_map(static fn (array $row): string => $row[0] . '.json', self::entities());
        sort($covered, SORT_STRING);
        self::assertSame(ConformancePaths::cases('entities'), $covered);
    }

    private static function normalizeNewlines(string $text): string
    {
        return str_replace("\r\n", "\n", $text);
    }
}
