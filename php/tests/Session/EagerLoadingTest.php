<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Session;

use LogicException;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use SimpleOrm\Query\FetchMode;
use SimpleOrm\Session\Db;
use SimpleOrm\Tests\Sample\Models\Role;
use SimpleOrm\Tests\Sample\Models\Transaction;
use SimpleOrm\Tests\Sample\Models\User;
use SimpleOrm\Tests\Sample\Models\UserRole;
use SimpleOrm\Tests\Support\SampleDatabase;
use SimpleOrm\Tests\Support\TempDatabase;
use SimpleOrm\Types\Decimal;

/**
 * Eager loading (Level 2 milestone 4, ADR-0022 + add.1, spec/loading.md
 * "Eager loading"): `Db::from()->include(...)->fetch(FetchMode)->toList()`.
 * `MultiQuery` and `SubSelect` load identical graphs to explicit `loadEach`
 * (tested in {@see LoadingTest}); this file exercises them through the
 * criteria chain, including a paged `SubSelect` root (the key-tiebroken
 * ordering `CriteriaQuery` applies so both evaluations pick the same rows).
 * `Join` mode is agent (c)'s engine ({@see \SimpleOrm\Session\DbEagerJoin}) —
 * still a stub as of this file; its test is skipped on that stub's own
 * refusal only.
 */
final class EagerLoadingTest extends TestCase
{
    private TempDatabase $fixture;

    private Db $db;

    protected function setUp(): void
    {
        $this->fixture = TempDatabase::create();
        $this->db = SampleDatabase::open($this->fixture);
    }

    protected function tearDown(): void
    {
        $this->db->close();
        $this->fixture->delete();
    }

    #[Test]
    public function include_loads_a_many_to_one_navigation_in_multi_query_mode(): void
    {
        $user = SampleDatabase::insertUser($this->db, 'Ada', 'ada@example.com');
        $this->insertTransaction($user->id, '19.99');

        $rows = $this->db->from(Transaction::class)->include('user')->fetch(FetchMode::MultiQuery)->toList();

        self::assertCount(1, $rows);
        self::assertNotNull($rows[0]->user);
        self::assertSame($user->id, $rows[0]->user->id);
    }

    #[Test]
    public function include_loads_a_one_to_many_navigation_in_subselect_mode(): void
    {
        $user = SampleDatabase::insertUser($this->db, 'Ada', 'ada@example.com');
        $other = SampleDatabase::insertUser($this->db, 'Grace', 'grace@example.com');
        $this->insertTransaction($user->id, '1.00');
        $this->insertTransaction($user->id, '2.00');

        $rows = $this->db->from(User::class)
            ->orderBy('id')
            ->include('transactions')
            ->fetch(FetchMode::SubSelect)
            ->toList();

        self::assertCount(2, $rows);
        self::assertCount(2, $rows[0]->transactions);
        self::assertSame([], $rows[1]->transactions);
        self::assertSame($user->id, $rows[0]->id);
        self::assertSame($other->id, $rows[1]->id);
    }

    #[Test]
    public function include_loads_a_many_to_many_navigation_in_multi_query_mode(): void
    {
        $user = SampleDatabase::insertUser($this->db, 'Ada', 'ada@example.com');
        $role = $this->insertRole('admin');
        $this->insertUserRole($user->id, $role->id);

        $rows = $this->db->from(User::class)->include('roles')->fetch(FetchMode::MultiQuery)->toList();

        self::assertCount(1, $rows);
        self::assertCount(1, $rows[0]->roles);
        self::assertSame($role->id, $rows[0]->roles[0]->id);
    }

    #[Test]
    public function multi_query_and_subselect_load_identical_graphs(): void
    {
        $user = SampleDatabase::insertUser($this->db, 'Ada', 'ada@example.com');
        $this->insertTransaction($user->id, '1.00');
        $this->insertTransaction($user->id, '2.00');

        $viaMultiQuery = $this->db->from(User::class)->orderBy('id')
            ->include('transactions')->fetch(FetchMode::MultiQuery)->toList();
        $viaSubSelect = $this->db->from(User::class)->orderBy('id')
            ->include('transactions')->fetch(FetchMode::SubSelect)->toList();

        self::assertSame(
            array_map(static fn (Transaction $t): int => $t->id, $viaMultiQuery[0]->transactions),
            array_map(static fn (Transaction $t): int => $t->id, $viaSubSelect[0]->transactions),
        );
    }

    #[Test]
    public function a_paged_subselect_root_gains_key_tiebroken_ordering_so_both_evaluations_pick_the_same_rows(): void
    {
        // Every user ties on `name`: an ORDER BY name alone leaves the page's
        // exact rows database-arbitrary. CriteriaQuery breaks the tie with the
        // key (`id`) on both the root and the subquery (ADR-0022 add.1
        // Clarifications), so the same two rows load correctly every time.
        $users = [];
        for ($i = 0; $i < 5; $i++) {
            $users[] = SampleDatabase::insertUser($this->db, 'Ada', "ada{$i}@example.com");
        }

        $this->insertTransaction($users[2]->id, '2.00');
        $this->insertTransaction($users[3]->id, '3.00');

        $rows = $this->db->from(User::class)
            ->orderBy('name')
            ->limit(2)
            ->offset(2)
            ->include('transactions')
            ->fetch(FetchMode::SubSelect)
            ->toList();

        self::assertSame([$users[2]->id, $users[3]->id], array_map(static fn (User $u): int => $u->id, $rows));
        self::assertCount(1, $rows[0]->transactions);
        self::assertSame('2.00', (string) $rows[0]->transactions[0]->amount);
        self::assertCount(1, $rows[1]->transactions);
        self::assertSame('3.00', (string) $rows[1]->transactions[0]->amount);
    }

    #[Test]
    public function include_in_join_mode_loads_the_same_graph_or_is_skipped_pending_the_join_engine(): void
    {
        $user = SampleDatabase::insertUser($this->db, 'Ada', 'ada@example.com');
        $this->insertTransaction($user->id, '1.00');

        try {
            $rows = $this->db->from(User::class)->include('transactions')->fetch(FetchMode::Join)->toList();
            self::assertCount(1, $rows);
            self::assertCount(1, $rows[0]->transactions);
        } catch (LogicException $e) {
            if (!str_contains($e->getMessage(), 'ADR-0032 step 2c')) {
                throw $e;
            }

            self::markTestSkipped('join mode pending (c) — ' . $e->getMessage());
        }
    }

    // --- fixture helpers ---------------------------------------------------------

    private function insertTransaction(int $userId, string $amount): Transaction
    {
        $transaction = new Transaction();
        $transaction->userId = $userId;
        $transaction->amount = Decimal::of($amount);
        $transaction->createdAtUtc = SampleDatabase::seedTime();
        $this->db->insert($transaction);

        return $transaction;
    }

    private function insertRole(string $name): Role
    {
        $role = new Role();
        $role->name = $name;
        $role->createdAtUtc = SampleDatabase::seedTime();
        $this->db->insert($role);

        return $role;
    }

    private function insertUserRole(int $userId, int $roleId): void
    {
        $link = new UserRole();
        $link->userId = $userId;
        $link->roleId = $roleId;
        $link->createdAtUtc = SampleDatabase::seedTime();
        $this->db->insert($link);
    }
}
