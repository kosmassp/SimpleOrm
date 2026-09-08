<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Discovery;

use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use SimpleOrm\Discovery\ClassScanner;
use SimpleOrm\Tests\Discovery\Fixtures\AlphaThing;
use SimpleOrm\Tests\Discovery\Fixtures\Sub\BetaThing;

/**
 * PSR-4 discovery under a directory (CODING-STANDARD §10): the PHP stand-in for
 * assembly scanning. {@see \SimpleOrm\Migrations\MigrationSet::fromDirectory()}
 * builds on exactly this.
 */
final class ClassScannerTest extends TestCase
{
    #[Test]
    public function discovers_classes_recursively_and_sorts_them_by_name(): void
    {
        $classes = ClassScanner::classes(__DIR__ . '/Fixtures', 'SimpleOrm\\Tests\\Discovery\\Fixtures');

        self::assertSame([AlphaThing::class, BetaThing::class], $classes);
    }

    #[Test]
    public function skips_php_files_that_declare_no_matching_class(): void
    {
        $classes = ClassScanner::classes(__DIR__ . '/Fixtures', 'SimpleOrm\\Tests\\Discovery\\Fixtures');

        self::assertNotContains('SimpleOrm\\Tests\\Discovery\\Fixtures\\NotAClass', $classes);
    }

    #[Test]
    public function a_missing_directory_yields_no_classes(): void
    {
        $classes = ClassScanner::classes(__DIR__ . '/Fixtures/DoesNotExist', 'SimpleOrm\\Tests\\Discovery\\Fixtures\\DoesNotExist');

        self::assertSame([], $classes);
    }
}
