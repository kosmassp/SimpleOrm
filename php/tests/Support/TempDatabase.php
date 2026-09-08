<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Support;

/**
 * A real SQLite test database (ADR-0003, CODING-STANDARD §7): a temp file per
 * test class, deleted afterwards. Never a mock, never in-memory.
 */
final class TempDatabase
{
    private function __construct(public readonly string $path)
    {
    }

    public static function create(): self
    {
        $path = sys_get_temp_dir() . DIRECTORY_SEPARATOR . 'simpleorm_php_' . bin2hex(random_bytes(8)) . '.db';

        return new self($path);
    }

    /** The connection string `Db::open` accepts: a bare path is SQLite. */
    public function connectionString(): string
    {
        return $this->path;
    }

    public function delete(): void
    {
        foreach ([$this->path, $this->path . '-wal', $this->path . '-shm', $this->path . '-journal'] as $file) {
            if (is_file($file)) {
                @unlink($file);
            }
        }
    }
}
