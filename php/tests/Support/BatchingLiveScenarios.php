<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Support;

use PDOException;
use PHPUnit\Framework\Assert;
use ReflectionProperty;
use SimpleOrm\Errors\SimpleOrmException;
use SimpleOrm\Session\Db;
use SimpleOrm\Tests\Sample\Models\Role;
use SimpleOrm\Tests\Sample\Models\Transaction;
use SimpleOrm\Tests\Sample\Models\TransactionDetail;
use SimpleOrm\Tests\Sample\Models\User;
use SimpleOrm\Types\Decimal;

/**
 * The statement-batching scenarios of the live testing contract (Level 4 item 3, ADR-0044, spec/dialects.md "Testing
 * contract"), written once for the PostgreSQL and SQL Server legs (CODING-STANDARD §8): a batched flush whose generated
 * keys flow across messages, an explicit batch carrying every operation, a stale version in the middle of a message
 * (`CRUD-010`, nothing applied, every instance as it was), and a failing statement in the middle of a message (the
 * provider's error, nothing applied). Each takes the session under test — opened with `BatchingMode::Auto` on the sample
 * schema — and a plain second session on the same database, which sees only what was committed.
 *
 * PHP runs these as the sequential realization on PostgreSQL (`batchingInEffect()` false: `pdo_pgsql` cannot carry
 * several statements with per-statement results) and as one multi-statement text on SQL Server (true); the **plan** — and
 * so the messages — is the same for the same facts of the dialect, which is why the expected grouping is a parameter.
 */
final class BatchingLiveScenarios
{
    private function __construct()
    {
    }

    /**
     * A tracked session flushes two users, two transactions that point at them, and two cascaded details: three levels of
     * generated keys, so three messages where one-statement-per-round-trip takes six round trips' worth of waiting.
     *
     * @param list<int> $expectedMessages
     */
    public static function aBatchedFlushFlowsGeneratedKeysAcrossMessages(Db $db, Db $observer, bool $inEffect, array $expectedMessages): void
    {
        $sizes = self::observe($db);
        Assert::assertSame($inEffect, $db->batchingInEffect());

        $ada = self::user('Ada', 'ada@example.com');
        $grace = self::user('Grace', 'grace@example.com');
        $first = self::transaction($ada, '10.00');
        $second = self::transaction($grace, '20.00');
        self::navigate($first, 'details', [self::detail('Widget'), self::detail('Gadget')]);

        foreach ([$first, $second, $ada, $grace] as $entity) {
            $db->persist($entity);
        }

        $summary = $db->flush();

        Assert::assertSame(6, $summary->inserted);
        Assert::assertSame($expectedMessages, $sizes->all(), 'the plan: each level waits for the keys of the one before it');
        Assert::assertGreaterThan(0, $ada->id);
        Assert::assertSame($ada->id, $first->userId, 'the generated key flowed into the dependent');
        Assert::assertSame($grace->id, $second->userId);
        foreach ($first->details as $detail) {
            Assert::assertSame($first->id, $detail->transactionId);
        }

        Assert::assertCount(2, $observer->queryAll(User::class));
        Assert::assertCount(2, $observer->queryAll(Transaction::class));
        Assert::assertCount(2, $observer->queryAll(TransactionDetail::class));
        Assert::assertSame($ada->id, $observer->get(Transaction::class, $first->id)->userId);
    }

    /**
     * An explicit batch carrying every operation in an untracked session: an insert, a column-list update, a full-row
     * update of a versioned row, a registry command, and a delete by key (the role's soft delete, a flag-setting update).
     *
     * @param list<int> $expectedMessages
     */
    public static function anExplicitBatchCarriesEveryOperation(Db $db, Db $observer, bool $inEffect, array $expectedMessages): void
    {
        Assert::assertSame($inEffect, $db->batchingInEffect());
        $ada = self::inserted($db, self::user('Ada', 'ada@example.com'));
        $grace = self::inserted($db, self::user('Grace', 'grace@example.com'));
        $order = self::inserted($db, self::transaction($ada, '10.00'));
        $seeded = self::inserted($db, self::role('seeded'));
        $sizes = self::observe($db);

        $fresh = self::role('fresh');
        $ada->displayName = 'Countess Ada';
        $order->amount = Decimal::of('11.00');

        $batch = $db->batch();
        $batch->insert($fresh);
        $batch->updateOnly($ada, ['displayName']);
        $batch->update($order);
        $batch->execute(SetDisplayNameArgs::command(), new SetDisplayNameArgs($grace->id, 'Rear Admiral Grace'));
        $batch->delete(Role::class, $seeded->id);
        $result = $batch->send();

        Assert::assertSame([1, 1, 1, 1, 1], $result->affected);
        Assert::assertSame($expectedMessages, $sizes->all());
        Assert::assertSame(count($expectedMessages), $result->messages);
        Assert::assertGreaterThan(0, $fresh->id, 'the generated key was written back');
        Assert::assertSame(1, $order->version, 'the version bumped in memory as the immediate update does');

        Assert::assertSame('Countess Ada', $observer->get(User::class, $ada->id)->displayName);
        Assert::assertSame('Rear Admiral Grace', $observer->get(User::class, $grace->id)->displayName);
        Assert::assertSame(1, $observer->get(Transaction::class, $order->id)->version);
        Assert::assertTrue($observer->get(Role::class, $seeded->id)->isDeleted);
        Assert::assertSame('fresh', $observer->get(Role::class, $fresh->id)->name);
    }

    /**
     * A stale version in the middle of a message: `CRUD-010`, the insert before it rolled back with the send's own
     * transaction, and every instance as it was — the role without a key, the stale transaction's version and stamp
     * unchanged.
     */
    public static function aStaleVersionInTheMiddleOfAMessageFailsTheSendAndLeavesNothingApplied(Db $db, Db $observer): void
    {
        $ada = self::inserted($db, self::user('Ada', 'ada@example.com'));
        $order = self::inserted($db, self::transaction($ada, '10.00'));
        $stale = $db->get(Transaction::class, $order->id);
        $order->note = 'first';
        $db->updateOnly($order, ['note']);   // the row is at version 1; $stale still holds 0
        $stale->note = 'second';
        $rolesBefore = count($observer->queryAll(Role::class));

        $role = self::role('reader');
        $batch = $db->batch();
        $batch->insert($role);
        $batch->updateOnly($stale, ['note']);

        Assert::assertSame('CRUD-010', self::codeOf(static fn () => $batch->send()));
        Assert::assertFalse(isset($role->id), 'the generated key was undone in memory');
        Assert::assertSame(0, $stale->version);
        Assert::assertNull($stale->updatedAtUtc);
        Assert::assertCount($rolesBefore, $observer->queryAll(Role::class), 'the send\'s own transaction rolled back');
        Assert::assertSame('first', $observer->get(Transaction::class, $order->id)->note);
        Assert::assertFalse($db->connection()->inTransaction());
    }

    /**
     * A failing statement in the middle of a message — a unique index refuses the second role — is the provider's own
     * error, unwrapped, with nothing applied: the roles before and after it, in the same send, were rolled back too.
     */
    public static function aFailingStatementInTheMiddleOfAMessageFailsTheSendWithTheProvidersError(Db $db, Db $observer): void
    {
        self::inserted($db, self::role('admin'));
        $rolesBefore = count($observer->queryAll(Role::class));

        $before = self::role('before');
        $duplicate = self::role('admin');
        $after = self::role('after');
        $batch = $db->batch();
        $batch->insert($before);
        $batch->insert($duplicate);
        $batch->insert($after);

        $failure = null;
        try {
            $batch->send();
        } catch (PDOException $e) {
            $failure = $e;
        }

        Assert::assertNotNull($failure, 'the provider\'s error propagates unchanged');
        foreach ([$before, $duplicate, $after] as $role) {
            Assert::assertFalse(isset($role->id), 'every generated key was undone in memory');
        }

        Assert::assertCount($rolesBefore, $observer->queryAll(Role::class), 'nothing applied');
        Assert::assertFalse($db->connection()->inTransaction());
    }

    // --- builders ---------------------------------------------------------------------------------

    private static function user(string $name, string $email): User
    {
        $user = new User();
        $user->name = $name;
        $user->email = $email;

        return $user;
    }

    private static function role(string $name): Role
    {
        $role = new Role();
        $role->name = $name;

        return $role;
    }

    /** A transaction owned by `$user`: the navigation carries the relationship, the FK follows at flush (or is set by the caller). */
    private static function transaction(User $user, string $amount): Transaction
    {
        $transaction = new Transaction();
        $transaction->amount = Decimal::of($amount);
        self::navigate($transaction, 'user', $user);
        if (isset($user->id)) {
            $transaction->userId = $user->id;
        }

        return $transaction;
    }

    private static function detail(string $description): TransactionDetail
    {
        $detail = new TransactionDetail();
        $detail->description = $description;
        $detail->quantity = 1;
        $detail->unitPrice = Decimal::of('2.50');

        return $detail;
    }

    /** The entity inserted immediately, for the set-up the batch then works on. */
    private static function inserted(Db $db, object $entity): object
    {
        $db->insert($entity);

        return $entity;
    }

    private static function navigate(object $owner, string $navigation, mixed $value): void
    {
        (new ReflectionProperty($owner::class, $navigation))->setValue($owner, $value);
    }

    /** Hooks the session's test hook: the sizes of the messages formed from now on. */
    private static function observe(Db $db): MessageSizes
    {
        $sizes = new MessageSizes();
        $db->observeMessages(static function (int $size) use ($sizes): void {
            $sizes->add($size);
        });

        return $sizes;
    }

    private static function codeOf(callable $action): string
    {
        try {
            $action();
        } catch (SimpleOrmException $e) {
            return $e->errorCode;
        }

        Assert::fail('expected a SimpleOrmException, none was thrown');
    }
}
