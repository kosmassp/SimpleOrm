<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Support;

use PDO;
use PDOException;
use PHPUnit\Framework\Assert;
use SimpleOrm\Dialect\PdoDsn;

/**
 * The probe behind every live (non-SQLite) test leg (spec/dialects.md "Testing
 * contract"): the admin DSN comes from an environment variable or the dialect's
 * conventional default, is tried **once** per process, and a test leg skips as a
 * group where nothing answers — so `composer test` still needs only PHP and
 * `pdo_sqlite`. Mirrors the C# `[PostgresFact]`/`[SqlServerFact]` pattern.
 */
final class LiveServer
{
    /** @var array<string, string|null> admin DSN per environment variable, null where the probe failed */
    private static array $probed = [];

    private function __construct()
    {
    }

    /**
     * The admin DSN for a server, or null where it does not answer. A PDO DSN
     * with the credentials embedded (`user=`/`password=` for pgsql, the driver's
     * own keys otherwise).
     */
    public static function adminDsn(string $environmentVariable, string $defaultDsn): ?string
    {
        if (array_key_exists($environmentVariable, self::$probed)) {
            return self::$probed[$environmentVariable];
        }

        $dsn = getenv($environmentVariable);
        $dsn = is_string($dsn) && $dsn !== '' ? $dsn : $defaultDsn;

        try {
            new PDO($dsn, null, null, [PDO::ATTR_ERRMODE => PDO::ERRMODE_EXCEPTION, PDO::ATTR_TIMEOUT => 5]);
            self::$probed[$environmentVariable] = $dsn;
        } catch (PDOException) {
            self::$probed[$environmentVariable] = null;   // no driver, or no server: the leg skips
        }

        return self::$probed[$environmentVariable];
    }

    /** Skips the calling test (PHPUnit's skip, never a silent pass) when the server is absent. */
    public static function skipUnless(?string $adminDsn, string $what): void
    {
        if ($adminDsn === null) {
            Assert::markTestSkipped($what);
        }
    }

    /** An open admin connection with the library's PDO attributes. */
    public static function connect(string $dsn): PDO
    {
        $pdo = new PDO($dsn);
        $pdo->setAttribute(PDO::ATTR_ERRMODE, PDO::ERRMODE_EXCEPTION);
        $pdo->setAttribute(PDO::ATTR_EMULATE_PREPARES, false);

        return $pdo;
    }

    /** A fresh per-fixture database name (spec/dialects.md: `simpleorm_test_<random hex>`). */
    public static function databaseName(): string
    {
        return 'simpleorm_test_' . bin2hex(random_bytes(8));
    }

    /** Replaces one `key=value` entry of a PDO DSN (the library's own editor, {@see PdoDsn}). */
    public static function withDsnValue(string $dsn, string $key, string $value): string
    {
        return PdoDsn::withValue($dsn, $key, $value);
    }
}
