<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Support;

use RuntimeException;

/** Locates the shared `conformance/` tree (§9): the executable definition every implementation runs unchanged. */
final class ConformancePaths
{
    public static function root(): string
    {
        $root = realpath(__DIR__ . '/../../../conformance');
        if ($root === false) {
            throw new RuntimeException('conformance/ not found beside php/ — run from the repository checkout');
        }

        return $root;
    }

    public static function dir(string $folder): string
    {
        return self::root() . DIRECTORY_SEPARATOR . $folder;
    }

    /** @return list<string> case file names in a conformance folder, sorted */
    public static function cases(string $folder): array
    {
        $files = glob(self::dir($folder) . DIRECTORY_SEPARATOR . '*.json') ?: [];
        sort($files, SORT_STRING);

        return array_map(basename(...), $files);
    }
}
