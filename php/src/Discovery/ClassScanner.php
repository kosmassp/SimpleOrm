<?php

declare(strict_types=1);

namespace SimpleOrm\Discovery;

use FilesystemIterator;
use RecursiveDirectoryIterator;
use RecursiveIteratorIterator;

/**
 * PSR-4 class discovery under a plain directory (CODING-STANDARD §10: PHP has
 * no assemblies to scan). Walks `$dir` recursively, derives each `*.php`
 * file's fully-qualified class name from its path relative to `$dir` under
 * `$namespace`, and keeps only names Composer autoload actually resolves —
 * never `require`s a file directly (CODING-STANDARD §1). The one implementation
 * of "every class under this directory/namespace"; {@see
 * \SimpleOrm\Migrations\MigrationSet::fromDirectory()} and the CLI both build
 * on it rather than walking the filesystem themselves.
 */
final class ClassScanner
{
    private function __construct()
    {
    }

    /** @return list<class-string> every autoloadable class found under $dir, sorted by name */
    public static function classes(string $dir, string $namespace): array
    {
        $classes = [];
        foreach (self::phpFilesUnder($dir) as $relativePath) {
            $class = rtrim($namespace, '\\') . '\\' . str_replace(['/', '\\'], '\\', substr($relativePath, 0, -4));
            if (class_exists($class)) {
                $classes[] = $class;
            }
        }

        sort($classes, SORT_STRING);

        return $classes;
    }

    /** @return list<string> file paths relative to $dir, using '/' separators, ending in '.php' */
    private static function phpFilesUnder(string $dir): array
    {
        if (!is_dir($dir)) {
            return [];
        }

        $base = rtrim(str_replace('\\', '/', $dir), '/');
        $paths = [];
        $iterator = new RecursiveIteratorIterator(new RecursiveDirectoryIterator($dir, FilesystemIterator::SKIP_DOTS));
        foreach ($iterator as $fileInfo) {
            if (!$fileInfo->isFile() || !str_ends_with($fileInfo->getFilename(), '.php')) {
                continue;
            }

            $full = str_replace('\\', '/', $fileInfo->getPathname());
            $paths[] = ltrim(substr($full, strlen($base)), '/');
        }

        sort($paths, SORT_STRING);

        return $paths;
    }
}
