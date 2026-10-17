<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Support;

use PHPUnit\Framework\Assert;
use SimpleOrm\Query\Criteria;
use SimpleOrm\Session\Db;
use SimpleOrm\Session\Query;
use SimpleOrm\Tests\Sample\Models\UserProfile;

/**
 * The `bytes` round trip every dialect runs (ADR-0040 add.1): `UserProfile.avatar` written by
 * insert, a column-list update and a full update, and matched by a criteria query and a
 * registry query whose args declare the token. The values are the ones text binding breaks —
 * a NUL, a backslash, 0xff, an invalid UTF-8 sequence, then bytes spelling a bytea escape
 * literal (`\x41\\` + NUL, which escaped-text binding reads back as `A`). The last write is a
 * NULL into the bytes column.
 */
final class BytesRoundTrip
{
    public const FIRST = "\x00\x5c\xff\x27\x41\xc3\x28\x0a";

    public const SECOND = "\x5c\x78\x34\x31\x5c\x5c\x00";

    private function __construct()
    {
    }

    /** Runs the round trip on a session holding the sample tables. */
    public static function run(Db $db): void
    {
        $user = SampleDatabase::insertUser($db, 'Ada', 'ada@example.com');
        $profile = new UserProfile();
        $profile->userId = $user->id;
        $profile->avatar = self::FIRST;
        $profile->createdAtUtc = SampleDatabase::seedTime();
        $db->insert($profile);

        Assert::assertSame(bin2hex(self::FIRST), bin2hex((string) self::read($db, $profile->id)->avatar));

        $byCriteria = $db->from(UserProfile::class)->where(Criteria::eq('avatar', self::FIRST))->toList();
        Assert::assertSame([$profile->id], array_map(static fn (UserProfile $p): int => $p->id, $byCriteria));

        $byQuery = $db->query(self::profilesByAvatar(), new AvatarArgs(self::FIRST));
        Assert::assertSame([$profile->id], array_map(static fn (UserProfile $p): int => $p->id, $byQuery));

        $profile->avatar = self::SECOND;
        $db->updateOnly($profile, ['avatar']);
        Assert::assertSame(bin2hex(self::SECOND), bin2hex((string) self::read($db, $profile->id)->avatar));

        $profile->avatar = null;
        $db->update($profile);
        Assert::assertNull(self::read($db, $profile->id)->avatar);
    }

    public static function profilesByAvatar(): Query
    {
        return Query::inline(
            AvatarArgs::class,
            UserProfile::class,
            'select id, user_id, bio, avatar_url, avatar, address_street, address_city, address_postal_code, created_at, updated_at'
                . ' from user_profiles where avatar = @avatar',
        );
    }

    private static function read(Db $db, int $id): UserProfile
    {
        $read = $db->get(UserProfile::class, $id);
        Assert::assertInstanceOf(UserProfile::class, $read);

        return $read;
    }
}

