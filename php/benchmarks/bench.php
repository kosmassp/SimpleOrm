<?php

declare(strict_types=1);

/**
 * The PHP benchmark harness of the performance pass (ADR-0048): the shapes the reference measured, timed with `hrtime()`
 * and sized with `memory_get_peak_usage()`, on SQLite (a temp file) and on PostgreSQL when `SIMPLEORM_POSTGRES` or the
 * local server on 5432 answers. Not a test: nothing is asserted, the numbers are printed.
 *
 *     php -d extension=pdo_sqlite benchmarks/bench.php [--iterations=N] [--only=sqlite|postgres] [--shape=TEXT] [--src=DIR]
 *
 * `--shape=TEXT` runs only the shapes whose name contains it; `--src=DIR` loads the library from another `src/` (see
 * below); add `-d extension=pdo_pgsql` where php.ini does not load it. Per shape: a warm-up, then timed iterations
 * ({@see Bench::measure()}): the CPU time per iteration (the steady number on a shared machine), the median wall time,
 * and the peak memory one iteration takes above what it started with, in KB (PHP has no allocation counter, so this is
 * the nearest proxy for the reference's allocated bytes).
 */

use SimpleOrm\Dialect\PostgresDialect;
use SimpleOrm\Dialect\SqliteDialect;
use SimpleOrm\Metadata\Attributes\Column;
use SimpleOrm\Query\Criteria;
use SimpleOrm\Query\FetchMode;
use SimpleOrm\Session\Db;
use SimpleOrm\Session\DbOptions;
use SimpleOrm\Session\Query;
use SimpleOrm\Tests\Sample\Models\Transaction;
use SimpleOrm\Tests\Sample\Models\TransactionDetail;
use SimpleOrm\Tests\Support\PostgresDatabase;
use SimpleOrm\Tests\Support\SampleDatabase;
use SimpleOrm\Tests\Support\TempDatabase;
use SimpleOrm\Types\Decimal;

require __DIR__ . '/../vendor/autoload.php';

// `--src=DIR` loads the library from another copy of `src/` (an older commit's, extracted with `git archive`), so a
// before and an after run back to back on the same machine state; the fixtures still come from this tree's `tests/`.
// Registered after Composer's loader, in front of it.
$librarySource = getopt('', ['src::'])['src'] ?? null;
if (is_string($librarySource)) {
    spl_autoload_register(static function (string $class) use ($librarySource): void {
        if (str_starts_with($class, 'SimpleOrm\\') && !str_starts_with($class, 'SimpleOrm\\Tests\\')) {
            $file = $librarySource . '/' . str_replace('\\', '/', substr($class, strlen('SimpleOrm\\'))) . '.php';
            if (is_file($file)) {
                require $file;
            }
        }
    }, prepend: true);
}

/** The registry query's args: one placeholder, so the binder's scan and plan are on the measured path. */
final class TransactionsOfUserArgs
{
    public function __construct(
        #[Column]
        public int $userId,
    ) {
    }
}

final class Bench
{
    private const GRAPH_OWNERS = 1000;

    private const CHILDREN_PER_OWNER = 3;

    private int $graphUserId;

    private int $insertUserId;

    private int $someTransactionId;

    private Query $registryQuery;

    /** @var list<Transaction>|null the owners `loadEach` loads into, read once */
    private ?array $owners = null;

    public function __construct(private readonly Db $db, private readonly int $iterations)
    {
        $this->registryQuery = Query::inline(
            TransactionsOfUserArgs::class,
            Transaction::class,
            'select id, user_id, status, amount, version, note, created_at, updated_at from transactions'
                . ' where user_id = @userId order by id',
        );
    }

    /** 1000 transactions of one user with 3 details each; a second user takes the inserts. */
    public function seed(): void
    {
        $scope = $this->db->begin();
        $graphUser = SampleDatabase::insertUser($this->db, 'graph', 'graph@example.com');
        $insertUser = SampleDatabase::insertUser($this->db, 'inserts', 'inserts@example.com');
        for ($i = 0; $i < self::GRAPH_OWNERS; $i++) {
            $transaction = SampleDatabase::insertTransaction($this->db, $graphUser, sprintf('%d.%02d', $i, $i % 100));
            for ($c = 0; $c < self::CHILDREN_PER_OWNER; $c++) {
                $detail = new TransactionDetail();
                $detail->transactionId = $transaction->id;
                $detail->description = "detail {$c}";
                $detail->quantity = $c + 1;
                $detail->unitPrice = Decimal::of('1.50');
                $detail->createdAtUtc = SampleDatabase::seedTime();
                $this->db->insert($detail);
            }

            $this->someTransactionId ??= $transaction->id;
        }

        $scope->commit();
        $this->graphUserId = $graphUser->id;
        $this->insertUserId = $insertUser->id;
    }

    /** @return array<string, Closure(): void> */
    public function shapes(): array
    {
        $db = $this->db;

        return [
            'insert 100, one transaction' => function () use ($db): void {
                $scope = $db->begin();
                for ($i = 0; $i < 100; $i++) {
                    $transaction = new Transaction();
                    $transaction->userId = $this->insertUserId;
                    $transaction->amount = Decimal::of('12.34');
                    $transaction->createdAtUtc = SampleDatabase::seedTime();
                    $db->insert($transaction);
                }

                $scope->commit();
            },
            'registry query, 1000 rows' => function () use ($db): void {
                $rows = $db->query($this->registryQuery, new TransactionsOfUserArgs($this->graphUserId));
                self::expectCount(self::GRAPH_OWNERS, $rows);
            },
            'get by key' => function () use ($db): void {
                $db->get(Transaction::class, $this->someTransactionId);
            },
            'criteria page, 100 rows' => function () use ($db): void {
                $rows = $db->from(Transaction::class)
                    ->where(Criteria::eq('userId', $this->graphUserId))
                    ->orderBy('id')
                    ->limit(100)
                    ->toList();
                self::expectCount(100, $rows);
            },
            'graph 1000x3, MultiQuery' => fn () => $this->graph(FetchMode::MultiQuery),
            'graph 1000x3, SubSelect' => fn () => $this->graph(FetchMode::SubSelect),
            'graph 1000x3, Join' => fn () => $this->graph(FetchMode::Join),
            'graph 1000x3, loadEach' => function () use ($db): void {
                $this->owners ??= $db->from(Transaction::class)->where(Criteria::eq('userId', $this->graphUserId))->toList();
                $db->loadEach($this->owners, 'details');
                self::expectCount(self::CHILDREN_PER_OWNER, $this->owners[0]->details);
            },
        ];
    }

    private function graph(FetchMode $mode): void
    {
        $rows = $this->db->from(Transaction::class)
            ->where(Criteria::eq('userId', $this->graphUserId))
            ->include('details')
            ->fetch($mode)
            ->toList();
        self::expectCount(self::GRAPH_OWNERS, $rows);
        self::expectCount(self::CHILDREN_PER_OWNER, $rows[0]->details);
    }

    /**
     * At least N iterations and at least {@see MIN_SECONDS} of wall time. The CPU time is this process's user + system
     * time over all iterations divided by their count (getrusage — coarse per call on Windows, exact enough summed over a
     * few seconds); it excludes a database server's own work and is far steadier than the wall time on a shared machine.
     *
     * @return array{0: float, 1: float, 2: float, 3: float, 4: int} CPU µs per iteration, median and fastest wall µs, median peak KB, iterations
     */
    public function measure(Closure $shape): array
    {
        for ($i = 0; $i < 3; $i++) {
            $shape();
        }

        $times = [];
        $peaks = [];
        $cpu = 0.0;
        $started = hrtime(true);
        for ($i = 0; $i < $this->iterations || (hrtime(true) - $started) / 1e9 < self::MIN_SECONDS; $i++) {
            gc_collect_cycles();
            memory_reset_peak_usage();
            $before = memory_get_usage();
            $usage = getrusage();
            $start = hrtime(true);
            $shape();
            $times[] = (hrtime(true) - $start) / 1000;
            $cpu += self::cpuMicros(getrusage()) - self::cpuMicros($usage);
            $peaks[] = (memory_get_peak_usage() - $before) / 1024;
        }

        sort($times);
        sort($peaks);

        return [$cpu / count($times), $times[intdiv(count($times), 2)], $times[0], $peaks[intdiv(count($peaks), 2)], count($times)];
    }

    private const MIN_SECONDS = 3.0;

    /** @param array<string, int> $usage */
    private static function cpuMicros(array $usage): float
    {
        return $usage['ru_utime.tv_sec'] * 1e6 + $usage['ru_utime.tv_usec'] + $usage['ru_stime.tv_sec'] * 1e6 + $usage['ru_stime.tv_usec'];
    }

    private static function expectCount(int $expected, array $rows): void
    {
        if (count($rows) !== $expected) {
            throw new RuntimeException(sprintf('expected %d rows, got %d', $expected, count($rows)));
        }
    }
}

$options = getopt('', ['iterations::', 'only::', 'shape::']);
$iterations = (int) ($options['iterations'] ?? 30);
$only = $options['only'] ?? null;
$shapeFilter = $options['shape'] ?? null;

/** @param Closure(): Db $open */
function run(string $label, Closure $open, int $iterations, ?string $shapeFilter): void
{
    $bench = new Bench($open(), $iterations);
    $bench->seed();
    printf("%s (at least %d iterations)\n", $label, $iterations);
    foreach ($bench->shapes() as $name => $shape) {
        if ($shapeFilter !== null && !str_contains($name, $shapeFilter)) {
            continue;
        }

        [$cpu, $wall, $fastest, $kb, $count] = $bench->measure($shape);
        printf("  %-28s %9.0f µs CPU %9.0f µs wall median %9.0f µs min %7.1f KB peak  n=%d\n", $name, $cpu, $wall, $fastest, $kb, $count);
    }
}

if ($only === null || $only === 'sqlite') {
    $fixture = TempDatabase::create();
    try {
        run('SQLite', static fn (): Db => SampleDatabase::open($fixture, new DbOptions(new SqliteDialect())), $iterations, $shapeFilter);
    } finally {
        gc_collect_cycles();
        $fixture->delete();
    }
}

if (($only === null || $only === 'postgres') && extension_loaded('pdo_pgsql') && PostgresDatabase::available()) {
    $database = PostgresDatabase::create();
    try {
        run('PostgreSQL', static function () use ($database): Db {
            $db = Db::open($database->connectionString(), new DbOptions(new PostgresDialect()));
            foreach ([
                SimpleOrm\Tests\Sample\Models\User::class,
                SimpleOrm\Tests\Sample\Models\Transaction::class,
                TransactionDetail::class,
            ] as $type) {
                $db->createTable($type);
            }

            return $db;
        }, $iterations, $shapeFilter);
    } finally {
        gc_collect_cycles();
        $database->drop();
    }
} elseif ($only === null || $only === 'postgres') {
    echo "PostgreSQL: not available (pdo_pgsql not loaded, or no server answers)\n";
}
