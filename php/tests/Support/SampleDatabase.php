<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Support;

use DateTimeImmutable;
use SimpleOrm\Dialect\SqliteDialect;
use SimpleOrm\Session\Db;
use SimpleOrm\Session\DbOptions;
use SimpleOrm\Tests\Sample\Models\Role;
use SimpleOrm\Tests\Sample\Models\Transaction;
use SimpleOrm\Tests\Sample\Models\TransactionDetail;
use SimpleOrm\Tests\Sample\Models\User;
use SimpleOrm\Tests\Sample\Models\UserProfile;
use SimpleOrm\Tests\Sample\Models\UserRole;
use SimpleOrm\Tests\Sample\Models\UserTransactionTotal;
use SimpleOrm\Types\Decimal;

/**
 * Session setup for Session/Query/Mapping integration tests (mirrors dotnet's
 * `TestDb`): opens a {@see Db} on a temp file and creates the whole sample
 * schema — tables and the view — from entity metadata (ADR-0011 + ADR-0008
 * add.3). No hand-written DDL, and no dependency on the migrations tree (a
 * separate, in-flux area of this port).
 */
final class SampleDatabase
{
    /** The fixed instant every sample fixture inserts with, matching conformance/fixtures/seed.json. */
    public static function seedTime(): DateTimeImmutable
    {
        return new DateTimeImmutable('2026-08-28T09:30:00.0000000Z');
    }

    public static function options(): DbOptions
    {
        return new DbOptions(new SqliteDialect());
    }

    public static function open(TempDatabase $fixture): Db
    {
        $db = Db::open($fixture->connectionString(), self::options());

        $db->createTable(User::class);
        $db->createTable(Role::class);
        $db->createTable(UserRole::class);
        $db->createTable(UserProfile::class);
        $db->createTable(Transaction::class);
        $db->createTable(TransactionDetail::class);
        $db->createView(UserTransactionTotal::class);

        return $db;
    }

    public static function insertUser(Db $db, string $name, string $email): User
    {
        $user = new User();
        $user->name = $name;
        $user->email = $email;
        $user->createdAtUtc = self::seedTime();
        $db->insert($user);

        return $user;
    }

    /** A transaction owned by `$user` (Level 2 eager-loading fixtures: EagerJoinTest). */
    public static function insertTransaction(Db $db, User $user, string $amount): Transaction
    {
        $transaction = new Transaction();
        $transaction->userId = $user->id;
        $transaction->amount = Decimal::of($amount);
        $transaction->createdAtUtc = self::seedTime();
        $db->insert($transaction);

        return $transaction;
    }

    /** Level 2 eager-loading fixtures (EagerJoinTest): a role, not yet granted to anyone. */
    public static function insertRole(Db $db, string $name): Role
    {
        $role = new Role();
        $role->name = $name;
        $role->createdAtUtc = self::seedTime();
        $db->insert($role);

        return $role;
    }

    /** Level 2 eager-loading fixtures (EagerJoinTest): grants `$role` to `$user` via the `UserRole` link. */
    public static function insertUserRole(Db $db, User $user, Role $role): UserRole
    {
        $userRole = new UserRole();
        $userRole->userId = $user->id;
        $userRole->roleId = $role->id;
        $userRole->createdAtUtc = self::seedTime();
        $db->insert($userRole);

        return $userRole;
    }

    /** Level 2 eager-loading fixtures (EagerJoinTest): the 1:1 profile side, FK on `UserProfile`. */
    public static function insertProfile(Db $db, User $user, ?string $bio = null): UserProfile
    {
        $profile = new UserProfile();
        $profile->userId = $user->id;
        $profile->bio = $bio;
        $profile->createdAtUtc = self::seedTime();
        $db->insert($profile);

        return $profile;
    }
}
