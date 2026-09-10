<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Session;

use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use SimpleOrm\Errors\SimpleOrmException;
use SimpleOrm\Metadata\Attributes\Column;
use SimpleOrm\Metadata\Attributes\Key;
use SimpleOrm\Metadata\Attributes\ManyToOne;
use SimpleOrm\Metadata\Attributes\View;
use SimpleOrm\Query\Criteria;
use SimpleOrm\Query\FetchMode;
use SimpleOrm\Session\Db;
use SimpleOrm\Tests\Sample\Models\Role;
use SimpleOrm\Tests\Sample\Models\Transaction;
use SimpleOrm\Tests\Sample\Models\User;
use SimpleOrm\Tests\Support\SampleDatabase;
use SimpleOrm\Tests\Support\TempDatabase;

/**
 * Join-mode eager loading (ADR-0022 add.1, spec/loading.md "Join"): one
 * SELECT with LEFT JOINs, segmented per alias and mapped through the one
 * pipeline (§7.11). Every graph pinned here is checked against explicitly
 * built expectations rather than against `FetchMode::MultiQuery` — at the
 * time this file was written {@see \SimpleOrm\Session\DbLoading::loadEach}
 * (agent (b)'s engine) still throws `LogicException`, so MultiQuery is not
 * yet a working oracle to compare against; re-run once it lands.
 */
final class EagerJoinTest extends TestCase
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
    public function many_to_one_join_attaches_the_shared_owner_instance(): void
    {
        $ada = SampleDatabase::insertUser($this->db, 'Ada', 'ada@example.com');
        $bob = SampleDatabase::insertUser($this->db, 'Bob', 'bob@example.com');
        SampleDatabase::insertTransaction($this->db, $ada, '1.00');
        SampleDatabase::insertTransaction($this->db, $ada, '2.00');
        SampleDatabase::insertTransaction($this->db, $bob, '3.00');

        $rows = $this->db->from(Transaction::class)
            ->orderBy('id')
            ->include('user')
            ->fetch(FetchMode::Join)
            ->toList();

        self::assertCount(3, $rows);
        self::assertSame('Ada', $rows[0]->user?->name);
        self::assertSame('Ada', $rows[1]->user?->name);
        self::assertSame('Bob', $rows[2]->user?->name);
        // Owners sharing a target within one call share the same instance
        // (spec/loading.md, per-kind table: many-to-one).
        self::assertSame($rows[0]->user, $rows[1]->user);
        self::assertNotSame($rows[0]->user, $rows[2]->user);
    }

    #[Test]
    public function a_null_foreign_key_owner_loads_the_navigation_as_null(): void
    {
        $ada = SampleDatabase::insertUser($this->db, 'Ada', 'ada@example.com');
        $detail = SampleDatabase::insertTransaction($this->db, $ada, '1.00');
        $orphan = SampleDatabase::insertTransaction($this->db, $ada, '2.00');

        // A dead link: point the FK at a user id that does not exist, bypassing
        // the entity's own navigation-consistency check (ADR-0005 add.1) via a
        // raw update, exactly like the C# reference's "dead link" fixtures.
        $this->db->connection()->exec('update transactions set user_id = 999999 where id = ' . $orphan->id);

        $rows = $this->db->from(Transaction::class)->orderBy('id')->include('user')->fetch(FetchMode::Join)->toList();

        self::assertCount(2, $rows);
        self::assertSame('Ada', $rows[0]->user?->name);
        self::assertNull($rows[1]->user, 'a dead link reads as null, never as an error');
    }

    #[Test]
    public function one_to_many_join_fills_a_fresh_ordered_list_and_deduplicates_the_root(): void
    {
        $ada = SampleDatabase::insertUser($this->db, 'Ada', 'ada@example.com');
        $bob = SampleDatabase::insertUser($this->db, 'Bob', 'bob@example.com');
        $t2 = SampleDatabase::insertTransaction($this->db, $ada, '2.00');
        $t1 = SampleDatabase::insertTransaction($this->db, $ada, '1.00');

        $rows = $this->db->from(User::class)->orderBy('id')->include('transactions')->fetch(FetchMode::Join)->toList();

        self::assertCount(2, $rows, 'the root deduplicates: Ada appears once despite two joined rows');
        self::assertSame('Ada', $rows[0]->name);
        self::assertCount(2, $rows[0]->transactions);
        // Collections order by target key, compared value-wise (spec/loading.md),
        // regardless of insertion order (t2 was inserted before t1).
        self::assertSame($t2->id, $rows[0]->transactions[0]->id);
        self::assertSame($t1->id, $rows[0]->transactions[1]->id);
        self::assertSame([], $rows[1]->transactions, 'Bob has none: loaded-but-empty, never null');
    }

    #[Test]
    public function many_to_many_join_resolves_through_the_link_and_shares_the_target_instance(): void
    {
        $ada = SampleDatabase::insertUser($this->db, 'Ada', 'ada@example.com');
        $bob = SampleDatabase::insertUser($this->db, 'Bob', 'bob@example.com');
        $admin = SampleDatabase::insertRole($this->db, 'admin');
        $user = SampleDatabase::insertRole($this->db, 'user');
        SampleDatabase::insertUserRole($this->db, $ada, $admin);
        SampleDatabase::insertUserRole($this->db, $ada, $user);
        SampleDatabase::insertUserRole($this->db, $bob, $admin);

        $rows = $this->db->from(User::class)->orderBy('id')->include('roles')->fetch(FetchMode::Join)->toList();

        self::assertCount(2, $rows);
        self::assertCount(2, $rows[0]->roles);
        self::assertSame('admin', $rows[0]->roles[0]->name);
        self::assertSame('user', $rows[0]->roles[1]->name);
        self::assertCount(1, $rows[1]->roles);
        self::assertSame('admin', $rows[1]->roles[0]->name);
        // The same role row shared across both owners is the same instance.
        self::assertSame($rows[0]->roles[0], $rows[1]->roles[0]);
    }

    #[Test]
    public function a_user_with_no_roles_loads_an_empty_collection_not_null(): void
    {
        SampleDatabase::insertUser($this->db, 'Ada', 'ada@example.com');

        $rows = $this->db->from(User::class)->include('roles')->fetch(FetchMode::Join)->toList();

        self::assertCount(1, $rows);
        self::assertSame([], $rows[0]->roles);
    }

    #[Test]
    public function one_to_one_join_attaches_the_single_matching_profile_or_null(): void
    {
        $ada = SampleDatabase::insertUser($this->db, 'Ada', 'ada@example.com');
        $bob = SampleDatabase::insertUser($this->db, 'Bob', 'bob@example.com');
        SampleDatabase::insertProfile($this->db, $ada, 'astronaut');

        $rows = $this->db->from(User::class)->orderBy('id')->include('profile')->fetch(FetchMode::Join)->toList();

        self::assertCount(2, $rows);
        self::assertSame('astronaut', $rows[0]->profile?->bio);
        self::assertNull($rows[1]->profile);
    }

    #[Test]
    public function a_to_one_include_pages_fine_under_join_mode(): void
    {
        $ada = SampleDatabase::insertUser($this->db, 'Ada', 'ada@example.com');
        $bob = SampleDatabase::insertUser($this->db, 'Bob', 'bob@example.com');
        SampleDatabase::insertUser($this->db, 'Cid', 'cid@example.com');
        SampleDatabase::insertProfile($this->db, $ada, 'astronaut');
        SampleDatabase::insertProfile($this->db, $bob, 'diver');

        $rows = $this->db->from(User::class)->orderBy('id')->include('profile')->fetch(FetchMode::Join)
            ->limit(2)->toList();

        self::assertCount(2, $rows);
        self::assertSame('astronaut', $rows[0]->profile?->bio);
        self::assertSame('diver', $rows[1]->profile?->bio);
    }

    #[Test]
    public function a_where_on_the_root_still_filters_the_joined_query(): void
    {
        $ada = SampleDatabase::insertUser($this->db, 'Ada', 'ada@example.com');
        SampleDatabase::insertUser($this->db, 'Bob', 'bob@example.com');
        SampleDatabase::insertTransaction($this->db, $ada, '1.00');

        $rows = $this->db->from(User::class)->where(Criteria::eq('name', 'Ada'))->include('transactions')
            ->fetch(FetchMode::Join)->toList();

        self::assertCount(1, $rows);
        self::assertSame('Ada', $rows[0]->name);
        self::assertCount(1, $rows[0]->transactions);
    }

    #[Test]
    public function a_non_included_collection_still_throws_rel_004_while_the_included_one_reads_empty(): void
    {
        SampleDatabase::insertUser($this->db, 'Ada', 'ada@example.com');

        $rows = $this->db->from(User::class)->include('transactions')->fetch(FetchMode::Join)->toList();

        self::assertSame([], $rows[0]->transactions);
        try {
            $rows[0]->roles;
            self::fail('an unloaded, non-included collection must not read as empty');
        } catch (SimpleOrmException $e) {
            self::assertSame('REL-004', $e->errorCode);
        }
    }

    #[Test]
    public function join_mode_refuses_a_paged_collection_include_with_rel_005(): void
    {
        SampleDatabase::insertUser($this->db, 'Ada', 'ada@example.com');

        try {
            $this->db->from(User::class)->include('transactions')->fetch(FetchMode::Join)->limit(5)->toList();
            self::fail('REL-005 expected');
        } catch (SimpleOrmException $e) {
            self::assertSame('REL-005', $e->errorCode);
        }
    }

    #[Test]
    public function join_mode_refuses_an_offset_collection_include_with_rel_005_too(): void
    {
        SampleDatabase::insertUser($this->db, 'Ada', 'ada@example.com');

        try {
            $this->db->from(User::class)->include('roles')->fetch(FetchMode::Join)->offset(1)->toList();
            self::fail('REL-005 expected');
        } catch (SimpleOrmException $e) {
            self::assertSame('REL-005', $e->errorCode);
        }
    }

    #[Test]
    public function join_mode_refuses_more_than_one_collection_include_with_rel_006(): void
    {
        SampleDatabase::insertUser($this->db, 'Ada', 'ada@example.com');

        try {
            $this->db->from(User::class)->include('transactions', 'roles')->fetch(FetchMode::Join)->toList();
            self::fail('REL-006 expected');
        } catch (SimpleOrmException $e) {
            self::assertSame('REL-006', $e->errorCode);
        }
    }

    #[Test]
    public function join_mode_refuses_a_keyless_root_with_rel_003(): void
    {
        // A view declares no #[Key] (metadata-model.md: key is optional, not
        // required, for a view) and the many-to-one arity check is skipped for
        // a keyless owner (MapAssembler::resolveRelationships) — so this
        // fixture is otherwise perfectly valid metadata; only join mode's own
        // dedup-needs-identity rule refuses it.
        try {
            $this->db->from(KeylessRoot::class)->include('ref')->fetch(FetchMode::Join)->toList();
            self::fail('REL-003 expected');
        } catch (SimpleOrmException $e) {
            self::assertSame('REL-003', $e->errorCode);
        }
    }

    #[Test]
    public function join_mode_refuses_a_keyless_target_with_rel_003(): void
    {
        // The owner is keyed; only the many-to-one target is a keyless view —
        // ManyToOne's arity check is skipped against a keyless target too
        // (MapAssembler::keyArity returns null), so this is metadata-valid and
        // only join mode's dedup rule refuses it.
        try {
            $this->db->from(KeylessTargetOwner::class)->include('target')->fetch(FetchMode::Join)->toList();
            self::fail('REL-003 expected');
        } catch (SimpleOrmException $e) {
            self::assertSame('REL-003', $e->errorCode);
        }
    }

    #[Test]
    public function an_unknown_navigation_is_rel_001_before_any_sql_runs(): void
    {
        SampleDatabase::insertUser($this->db, 'Ada', 'ada@example.com');

        try {
            $this->db->from(User::class)->include('noSuchNavigation')->fetch(FetchMode::Join)->toList();
            self::fail('REL-001 expected');
        } catch (SimpleOrmException $e) {
            self::assertSame('REL-001', $e->errorCode);
        }
    }

    #[Test]
    public function join_mode_and_multi_query_load_identical_graphs_for_a_many_to_one_include(): void
    {
        $ada = SampleDatabase::insertUser($this->db, 'Ada', 'ada@example.com');
        SampleDatabase::insertTransaction($this->db, $ada, '1.00');
        SampleDatabase::insertTransaction($this->db, $ada, '2.00');

        $join = $this->db->from(Transaction::class)->orderBy('id')->include('user')->fetch(FetchMode::Join)->toList();

        try {
            $multi = $this->db->from(Transaction::class)->orderBy('id')->include('user')
                ->fetch(FetchMode::MultiQuery)->toList();
        } catch (\LogicException) {
            self::markTestIncomplete(
                'DbLoading::loadEach (agent (b), FetchMode::MultiQuery) is not implemented yet — '
                . 'comparing Join mode against explicitly built expectations only, see the other tests in this class.',
            );
        }

        self::assertCount(count($join), $multi);
        foreach ($join as $i => $entity) {
            self::assertSame($entity->id, $multi[$i]->id);
            self::assertSame($entity->user?->name, $multi[$i]->user?->name);
        }
    }
}

/**
 * `REL-003` fixture (a keyless root): a view declares no `#[Key]` — allowed,
 * not required, for a view (spec/metadata-model.md's capability table) — and
 * a many-to-one navigation's arity check against the *owner's* key is skipped
 * entirely for a keyless owner (`MapAssembler::resolveRelationships`), so this
 * loads as perfectly valid metadata; only join mode's own "identity drives
 * the reshaping" rule (spec/loading.md "Join") refuses it. Never queried —
 * the refusal happens before any SQL renders, so the `sql` text is a stand-in.
 */
#[View('kt_keyless_root', 'select 1 as x')]
final class KeylessRoot
{
    #[Column]
    public int $x;

    #[ManyToOne('x')]
    public private(set) ?Role $ref = null;
}

/** `REL-003` fixture (a keyless target): never queried, see {@see KeylessRoot}. */
#[View('kt_keyless_target', 'select 1 as dummy_id')]
final class KeylessTarget
{
    #[Column]
    public int $dummyId;
}

/**
 * `REL-003` fixture: a keyed owner whose many-to-one target is
 * {@see KeylessTarget} — `MapAssembler::keyArity` returns null for a keyless
 * type, so the arity check against the *target's* key is skipped too, and
 * this loads as valid metadata; only join mode refuses it.
 */
#[View('kt_keyless_target_owner', 'select 1 as id, 1 as target_id')]
final class KeylessTargetOwner
{
    #[Key]
    #[Column]
    public int $id;

    #[Column]
    public int $targetId;

    #[ManyToOne('targetId')]
    public private(set) ?KeylessTarget $target = null;
}
