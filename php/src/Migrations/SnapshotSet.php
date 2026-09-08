<?php

declare(strict_types=1);

namespace SimpleOrm\Migrations;

use FilesystemIterator;
use RecursiveDirectoryIterator;
use RecursiveIteratorIterator;

/**
 * Every committed schema snapshot of a migrations tree, indexed by
 * (object, version) — the history the derived-rollback deriver reads from
 * (ADR-0018). Loads from a directory of `*.schema.json` files (the PHP
 * counterpart of the C# reference's embedded-resource loading — no assemblies
 * in PHP, CODING-STANDARD §10), or from documents already in memory (the
 * conformance runner feeds cases this way).
 */
final class SnapshotSet
{
    /** @var array<string, array<int, SnapshotSetEntry>> lowercased object name => version => entry */
    private array $byObject = [];

    /** @param list<string> $documents raw JSON snapshot documents (table- or DDL-shaped) */
    public function __construct(array $documents = [])
    {
        foreach ($documents as $document) {
            $this->add($document);
        }
    }

    public static function fromDirectory(string $directory): self
    {
        $documents = [];
        if (is_dir($directory)) {
            foreach (self::schemaFilesUnder($directory) as $file) {
                $content = file_get_contents($file);
                if ($content !== false) {
                    $documents[] = $content;
                }
            }
        }

        return new self($documents);
    }

    /** The object's snapshot at exactly this version, or null when the version didn't touch it. */
    public function at(string $objectName, int $version): ?SnapshotSetEntry
    {
        return $this->byObject[strtolower($objectName)][$version] ?? null;
    }

    /** The object's latest snapshot strictly before the version — null means the version created it. */
    public function latestBefore(string $objectName, int $version): ?SnapshotSetEntry
    {
        $versions = $this->byObject[strtolower($objectName)] ?? null;
        if ($versions === null) {
            return null;
        }

        $latestVersion = null;
        $latestEntry = null;
        foreach ($versions as $atVersion => $entry) {
            if ($atVersion >= $version) {
                continue;
            }

            if ($latestVersion === null || $atVersion > $latestVersion) {
                $latestVersion = $atVersion;
                $latestEntry = $entry;
            }
        }

        return $latestEntry;
    }

    private function add(string $json): void
    {
        /** @var array{object: string, asOfVersion: int|string} $data */
        $data = json_decode($json, true, flags: JSON_THROW_ON_ERROR);
        $objectName = strtolower($data['object']);
        $version = (int) $data['asOfVersion'];
        $isDdl = array_key_exists('ddl', $data);

        $entry = $isDdl
            ? new SnapshotSetEntry(null, self::ddlFrom($json))
            : new SnapshotSetEntry(SchemaSnapshot::parse($json)->schema, null);

        $this->byObject[$objectName][$version] = $entry;
    }

    private static function ddlFrom(string $json): string
    {
        return SchemaSnapshot::parseDdl($json)->ddl;
    }

    /**
     * Immediate subdirectories of `$dir`, unsorted — the one implementation of
     * "each object's own directory under a kind root" (CODING-STANDARD §8),
     * shared by `SqliteShadow`'s replay and `Diff\DiffCommand`'s generation.
     *
     * @return list<string>
     */
    public static function subdirectories(string $dir): array
    {
        $result = [];
        foreach (scandir($dir) ?: [] as $entry) {
            if ($entry === '.' || $entry === '..') {
                continue;
            }

            $path = "{$dir}/{$entry}";
            if (is_dir($path)) {
                $result[] = $path;
            }
        }

        return $result;
    }

    /** @return list<string> */
    private static function schemaFilesUnder(string $directory): array
    {
        $files = [];
        $iterator = new RecursiveIteratorIterator(new RecursiveDirectoryIterator($directory, FilesystemIterator::SKIP_DOTS));
        foreach ($iterator as $fileInfo) {
            if ($fileInfo->isFile() && str_ends_with($fileInfo->getFilename(), '.schema.json')) {
                $files[] = $fileInfo->getPathname();
            }
        }

        return $files;
    }
}
