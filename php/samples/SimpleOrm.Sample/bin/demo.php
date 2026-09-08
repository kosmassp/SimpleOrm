#!/usr/bin/env php
<?php

declare(strict_types=1);

// End-to-end walk through the sample against a real SQLite file: migrate the
// whole history, validate with SchemaGuard, then exercise CRUD, criteria, the
// registry (incl. §7.10 JSON nesting), the statement entity, the view,
// optimistic concurrency, transactions, derived rollbacks, and metadata export.
// Every round trip is explicit (§2 "no hidden queries"). Exit code 0 means every
// step behaved as the spec says; any exception prints its error code and exits 1.
//
//   composer demo              (temp database, deleted afterwards)
//   php bin/demo.php path.db   (keep the database for inspection)

foreach ([__DIR__ . '/../vendor/autoload.php', __DIR__ . '/../../../vendor/autoload.php'] as $autoload) {
    if (is_file($autoload)) {
        require $autoload;
        break;
    }
}

use SimpleOrm\Dialect\SqliteDialect;
use SimpleOrm\Discovery\ClassScanner;
use SimpleOrm\Errors\ConcurrencyException;
use SimpleOrm\Errors\SchemaValidationException;
use SimpleOrm\Errors\SimpleOrmException;
use SimpleOrm\Mapping\JsonTypeHandler;
use SimpleOrm\Mapping\TypeHandlerRegistry;
use SimpleOrm\Metadata\EntityMapJson;
use SimpleOrm\Migrations\MigrationRunner;
use SimpleOrm\Migrations\MigrationSet;
use SimpleOrm\Migrations\SnapshotSet;
use SimpleOrm\Query\Criteria;
use SimpleOrm\Sample\Models\Tables\Role;
use SimpleOrm\Sample\Models\Tables\Transaction;
use SimpleOrm\Sample\Models\Tables\TransactionDetail;
use SimpleOrm\Sample\Models\Tables\TransactionStatus;
use SimpleOrm\Sample\Models\Tables\User;
use SimpleOrm\Sample\Models\Tables\UserRole;
use SimpleOrm\Sample\Models\Views\UserTransactionTotal;
use SimpleOrm\Sample\Registry\CancelStaleArgs;
use SimpleOrm\Sample\Registry\Commands;
use SimpleOrm\Sample\Registry\DetailLine;
use SimpleOrm\Sample\Registry\Queries;
use SimpleOrm\Sample\Registry\TransactionsByUserArgs;
use SimpleOrm\Sample\Repositories\TransactionRepository;
use SimpleOrm\Sample\Repositories\UserRepository;
use SimpleOrm\Session\Db;
use SimpleOrm\Session\DbOptions;
use SimpleOrm\Session\EmptyArgs;
use SimpleOrm\Types\Decimal;
use SimpleOrm\Validation\SchemaGuard;

$keepDatabase = isset($argv[1]);
$databasePath = $argv[1] ?? sys_get_temp_dir() . DIRECTORY_SEPARATOR . 'simpleorm_sample_' . bin2hex(random_bytes(4)) . '.db';
$sourceDir = dirname(__DIR__) . '/src';
$migrationsDir = $sourceDir . '/Migrations';

function ok(string $label, string $detail = ''): void
{
    echo '[ok] ' . $label . ($detail !== '' ? ': ' . $detail : '') . PHP_EOL;
}

function expect(bool $condition, string $what): void
{
    if (!$condition) {
        throw new RuntimeException('expectation failed: ' . $what);
    }
}

function utc(string $when): DateTimeImmutable
{
    return new DateTimeImmutable($when, new DateTimeZone('UTC'));
}

// The JSON list handler is what turns the nested `details` column into DetailLine objects (§7.10).
$handlers = new TypeHandlerRegistry();
$handlers->register(new JsonTypeHandler(DetailLine::class, list: true));
$db = Db::open($databasePath, new DbOptions(new SqliteDialect(), typeHandlers: $handlers));
echo 'database: ' . $databasePath . PHP_EOL;

try {
    // --- migrations (§7.22-24): explicit, never at startup -----------------------------
    $set = MigrationSet::fromDirectory($migrationsDir, 'SimpleOrm\Sample\Migrations');
    $snapshots = SnapshotSet::fromDirectory($migrationsDir);
    $runner = new MigrationRunner($db->connection(), $db->options()->dialect, $db->maps(), $set, $snapshots);

    $applied = $runner->migrate();
    expect($applied === 9, "9 versions applied, got {$applied}");
    ok('migrate', "{$applied} versions applied");
    expect($runner->migrate() === 0, 'a second migrate applies nothing');
    ok('migrate again', 'nothing pending');

    foreach ($runner->status() as $entry) {
        echo '     ' . $entry . PHP_EOL;
    }

    // --- SchemaGuard (§7.18-21): every registry entry, entity, and the history ------
    $classes = ClassScanner::classes($sourceDir, 'SimpleOrm\Sample');
    SchemaGuard::validate($db, $classes, $set, $snapshots);
    ok('SchemaGuard', count($classes) . ' classes under SimpleOrm\Sample validated, no pending migrations');

    // --- CRUD through the repositories (§7.14-16) ----------------------------------
    $users = new UserRepository($db);
    $transactions = new TransactionRepository($db);
    $now = utc('2026-09-08T12:00:00');

    $ada = new User();
    $ada->name = 'Ada';
    $ada->email = 'ada@example.com';
    $ada->createdAtUtc = $now;
    $users->insert($ada);

    $grace = new User();
    $grace->name = 'Grace';
    $grace->email = 'grace@example.com';
    $grace->createdAtUtc = $now;
    $users->insert($grace);
    expect($ada->id > 0 && $grace->id === $ada->id + 1, 'generated keys land on the entities');
    ok('insert users', "generated keys written back: ada={$ada->id}, grace={$grace->id}");

    $admin = $db->from(Role::class)->where(Criteria::eq('name', 'admin'))->single();
    $link = new UserRole();
    $link->userId = $ada->id;
    $link->roleId = $admin->id;
    $link->grantedBy = 'demo';
    $link->createdAtUtc = $now;
    $db->insert($link);
    $linkAgain = $db->get(UserRole::class, [$ada->id, $admin->id]);
    expect($linkAgain->grantedBy === 'demo', 'composite key read returns the link');
    ok('composite key', "user_roles ({$ada->id}, {$admin->id}) inserted and read back by key list");

    $first = new Transaction();
    $first->userId = $ada->id;
    $first->amount = Decimal::of('15.25');
    $first->createdAtUtc = utc('2026-09-01T10:00:00');
    $transactions->insert($first);

    $second = new Transaction();
    $second->userId = $ada->id;
    $second->status = TransactionStatus::Completed;
    $second->amount = Decimal::of('40.00');
    $second->createdAtUtc = utc('2026-09-02T10:00:00');
    $transactions->insert($second);

    $old = new Transaction();
    $old->userId = $grace->id;
    $old->amount = Decimal::of('7.50');
    $old->createdAtUtc = utc('2026-08-20T10:00:00');
    $transactions->insert($old);

    foreach ([['cake', 2, '5.00'], ['candle', 1, '5.25']] as [$description, $quantity, $unitPrice]) {
        $detail = new TransactionDetail();
        $detail->transactionId = $first->id;
        $detail->description = $description;
        $detail->quantity = $quantity;
        $detail->unitPrice = Decimal::of($unitPrice);
        $detail->createdAtUtc = $now;
        $db->insert($detail);
    }
    ok('insert transactions', '3 transactions, 2 detail lines (decimal amounts, enum status, UTC dates)');

    // --- reads: by key, by criteria (ADR-0006/0012) -----------------------------------
    $byKey = $transactions->get($first->id);
    expect($byKey->amount->equals(Decimal::of('15.25')) && $byKey->status === TransactionStatus::Pending, 'get by key maps every column');
    expect($users->getByEmail('grace@example.com')->id === $grace->id, 'criteria single()');
    expect(count($users->getByIds([$ada->id, $grace->id, 999])) === 2, 'IN-list expansion');
    $pending = $transactions->getByStatus(TransactionStatus::Pending);
    expect(count($pending) === 2, 'enum criteria binds by name');
    $page = $db->from(Transaction::class)->orderBy('createdAtUtc')->limit(2)->offset(1)->toList();
    expect(count($page) === 2 && $page[0]->id === $first->id, 'ordering + paging');
    expect($db->getOrDefault(User::class, 999) === null, 'getOrDefault on a missing key');
    ok('reads', 'get, criteria single/in/eq(enum), order+limit+offset, getOrDefault');

    // --- registry queries (§6): JSON nesting and an aggregate ------------------------
    $withDetails = $db->query(Queries::transactionsWithDetails(), new TransactionsByUserArgs($ada->id));
    expect(count($withDetails) === 2, 'two transactions for Ada');
    expect(count($withDetails[0]->details) === 2 && $withDetails[0]->details[1]->description === 'candle', 'nested details hydrate');
    expect($withDetails[1]->details === [], 'a parent without children gets an empty list, never null');
    $usage = $db->query(Queries::roleUsage(), EmptyArgs::value());
    expect($usage[0]->roleName === 'admin' && $usage[0]->userCount === 1 && $usage[1]->userCount === 0, 'role usage aggregate');
    $streamed = 0;
    foreach ($db->stream(Queries::roleUsage(), EmptyArgs::value()) as $row) {
        $streamed++;
    }
    expect($streamed === 2, 'stream() yields every row');
    ok('registry queries', 'json_group_array nesting -> DetailLine objects; aggregate rows; stream()');

    // --- statement entity (ADR-0008 add.2) and the view (add.3) ---------------------
    $daily = $transactions->getDailySales(utc('2026-08-01T00:00:00'));
    expect(count($daily) === 3 && $daily[0]->salesDate->format('Y-m-d') === '2026-09-02', 'daily sales by day, newest first');
    expect($daily[0]->totalAmount->equals(Decimal::of('40')), 'sum(amount) maps to Decimal');
    $totals = $db->from(UserTransactionTotal::class)->orderBy('userId')->toList();
    $adaTotal = $db->get(UserTransactionTotal::class, $ada->id);
    expect(count($totals) === 2 && $adaTotal->transactionCount === 2 && $adaTotal->totalAmount->equals(Decimal::of('55.25')), 'view totals');
    expect($adaTotal->lastTransactionAtUtc?->format('Y-m-d') === '2026-09-02', 'view max(created_at) maps to a UTC DateTimeImmutable');
    ok('statement + view', 'DailySales by type; user_transaction_totals by criteria and by key');

    // --- optimistic concurrency (§7.16) --------------------------------------------
    $stale = $transactions->get($first->id);
    $transactions->setStatus($first->id, TransactionStatus::Completed, $now);
    $fresh = $transactions->get($first->id);
    expect($fresh->version === 1 && $fresh->status === TransactionStatus::Completed && $fresh->updatedAtUtc !== null, 'update bumps version');
    try {
        $stale->note = 'late edit';
        $transactions->update($stale);
        expect(false, 'a stale version must not update');
    } catch (ConcurrencyException $conflict) {
        expect($conflict->errorCode === 'CRUD-010', 'CRUD-010 on a stale version');
    }
    ok('concurrency', 'version 0 -> 1 on update; the stale copy is refused with CRUD-010');

    // --- commands inside an explicit transaction (§7.17) -----------------------------
    $scope = $db->begin();
    $affected = $db->execute(Commands::cancelStaleTransactions(), new CancelStaleArgs(utc('2026-09-01T00:00:00'), $now));
    expect($affected === 1, "one stale pending transaction, got {$affected}");
    $scope->rollback();
    expect($transactions->get($old->id)->status === TransactionStatus::Pending, 'rollback restores the row');

    $scope = $db->begin();
    $db->execute(Commands::cancelStaleTransactions(), new CancelStaleArgs(utc('2026-09-01T00:00:00'), $now));
    $scope->commit();
    expect($transactions->get($old->id)->status === TransactionStatus::Cancelled, 'commit keeps the change');
    ok('command + transaction', 'partial update via hand SQL; rolled back once, then committed');

    // --- deletes (§7.16) ------------------------------------------------------------
    foreach ($db->from(TransactionDetail::class)->where(Criteria::eq('transactionId', $first->id))->toList() as $detail) {
        $db->delete(TransactionDetail::class, $detail->id);
    }
    $transactions->delete($transactions->get($first->id));   // the entity form is version-checked
    expect($transactions->getOrDefault($first->id) === null, 'deleted');
    try {
        $transactions->delete($first->id);
        expect(false, 'deleting a missing key must throw');
    } catch (SimpleOrmException $missing) {
        expect($missing->errorCode === 'CRUD-001', 'CRUD-001 on a missing key');
    }
    ok('delete', 'by key and version-checked by entity; a second delete is CRUD-001');

    // --- derived rollbacks (ADR-0018): no Down() anywhere in the sample -------------
    $reverted = $runner->migrateDown(3);
    expect($reverted === 6, "6 versions reverted, got {$reverted}");
    $columns = $db->connection()->query("select name from pragma_table_info('roles') order by name")->fetchAll(PDO::FETCH_COLUMN);
    expect(in_array('name', $columns, true) && !in_array('role_name', $columns, true), 'V0004 rename inverted data-preservingly');
    $reapplied = $runner->migrate();
    expect($reapplied === 6, "6 versions re-applied, got {$reapplied}");
    $roleNames = array_map(static fn (Role $role): string => $role->name, $db->queryAll(Role::class));
    expect($roleNames === ['admin', 'user'], 'seeds survive the round trip');
    ok('migrate down --to 3, then up', 'rename inverted, columns/indexes/view restored, seeds intact');

    // --- metadata export (§7.3): the conformance artifact ----------------------------
    $json = EntityMapJson::export($db->maps()->load(User::class), $db->maps());
    $decoded = json_decode($json, true, flags: JSON_THROW_ON_ERROR);
    expect($decoded['entity'] === 'User' && str_contains($json, '"users"'), 'export names the entity and its table');
    ok('export-metadata', 'User -> ' . strlen($json) . ' bytes of EntityMap JSON');

    echo PHP_EOL . 'SimpleOrm PHP sample: every step passed.' . PHP_EOL;
    $exit = 0;
} catch (SchemaValidationException $report) {
    fwrite(STDERR, '[FAIL] SchemaGuard report:' . PHP_EOL . $report->getMessage() . PHP_EOL);
    $exit = 1;
} catch (SimpleOrmException $failure) {
    fwrite(STDERR, "[FAIL] {$failure->errorCode} {$failure->getMessage()}" . PHP_EOL);
    $exit = 1;
} catch (Throwable $failure) {
    fwrite(STDERR, '[FAIL] ' . $failure::class . ': ' . $failure->getMessage() . PHP_EOL . $failure->getTraceAsString() . PHP_EOL);
    $exit = 1;
} finally {
    $db->close();
    if (!$keepDatabase) {
        // Best-effort: Windows can hold a delete lock briefly after SQLite closes its handle.
        set_error_handler(static fn (): bool => true);
        try {
            foreach ([$databasePath, $databasePath . '-journal', $databasePath . '-wal', $databasePath . '-shm'] as $file) {
                if (is_file($file)) {
                    unlink($file);
                }
            }
        } finally {
            restore_error_handler();
        }
    }
}

exit($exit);
