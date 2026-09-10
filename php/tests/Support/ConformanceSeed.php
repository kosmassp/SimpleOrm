<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Support;

use SimpleOrm\Dialect\SqliteDialect;
use SimpleOrm\Parameters\PdoBinder;

/**
 * Seeds a fixture database from `conformance/fixtures/seed.json` (§9) — the
 * same rows every conformance runner seeds from, factored out of
 * `Conformance\CasesTest` so `Conformance\LoadCasesTest` (and any other
 * runner) can build the identical fixture without duplicating the insert
 * loop. Schema creation stays the caller's job (each runner creates only the
 * tables/views it needs); this only inserts rows.
 */
final class ConformanceSeed
{
    public static function insert(TempDatabase $fixture): void
    {
        $seed = json_decode(
            (string) file_get_contents(ConformancePaths::dir('fixtures') . DIRECTORY_SEPARATOR . 'seed.json'),
            associative: true,
            flags: JSON_THROW_ON_ERROR,
        );

        $pdo = (new SqliteDialect())->createConnection($fixture->connectionString());
        foreach ($seed as $table => $rows) {
            foreach ($rows as $row) {
                $columns = array_keys($row);
                // Column names come from the fixture's own keys (trusted test
                // data, never user input); values always bind as parameters.
                $sql = 'insert into ' . $table . ' (' . implode(', ', $columns) . ') values ('
                    . implode(', ', array_map(static fn (string $c): string => ':' . $c, $columns)) . ')';
                PdoBinder::bindAndExecute($pdo->prepare($sql), $row);
            }
        }
    }
}
