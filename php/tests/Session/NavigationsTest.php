<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Session;

use Error;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use SimpleOrm\Errors\SimpleOrmException;
use SimpleOrm\Metadata\Attributes\Column;
use SimpleOrm\Metadata\Attributes\Generated;
use SimpleOrm\Metadata\Attributes\Key;
use SimpleOrm\Metadata\Attributes\OneToMany;
use SimpleOrm\Metadata\Attributes\Table;
use SimpleOrm\Query\Criteria;
use SimpleOrm\Session\Db;
use SimpleOrm\Tests\Sample\Models\Transaction;
use SimpleOrm\Tests\Sample\Models\User;
use SimpleOrm\Tests\Support\SampleDatabase;
use SimpleOrm\Tests\Support\TempDatabase;
use SimpleOrm\Types\Decimal;

/**
 * The unloaded-collection guard (ADR-0021 add.2, ADR-0032 ruling 2,
 * spec/loading.md "The guard across languages"): a database-read entity's
 * collection navigations are unset by the mapper, so a read refuses — `REL-004`
 * through the opt-in `Navigations` trait, PHP's own `Error` without it — and
 * never returns a silent `[]`. User-constructed entities keep their
 * initializers; singular navigations stay null (no proxies).
 */
final class NavigationsTest extends TestCase
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
    public function a_database_read_entity_with_the_trait_throws_rel_004_on_an_unloaded_collection(): void
    {
        SampleDatabase::insertUser($this->db, 'Ada', 'ada@example.com');
        $user = $this->db->from(User::class)->single();
        self::assertInstanceOf(User::class, $user);

        try {
            $user->transactions;
            self::fail('an unloaded collection must not read as empty');
        } catch (SimpleOrmException $e) {
            self::assertSame('REL-004', $e->errorCode);
            self::assertStringContainsString('transactions', $e->getMessage());
        }

        try {
            $user->roles;
            self::fail('an unloaded many-to-many must not read as empty');
        } catch (SimpleOrmException $e) {
            self::assertSame('REL-004', $e->errorCode);
        }
    }

    #[Test]
    public function a_database_read_entity_without_the_trait_refuses_with_php_own_error(): void
    {
        SampleDatabase::insertUser($this->db, 'Ada', 'ada@example.com');
        $user = $this->db->from(PlainUser::class)->single();
        self::assertInstanceOf(PlainUser::class, $user);

        $this->expectException(Error::class);
        $this->expectExceptionMessage('must not be accessed before initialization');
        $user->transactions;
    }

    #[Test]
    public function a_key_read_marks_collections_unloaded_too(): void
    {
        $inserted = SampleDatabase::insertUser($this->db, 'Ada', 'ada@example.com');
        $user = $this->db->get(User::class, $inserted->id);
        self::assertInstanceOf(User::class, $user);

        $this->expectException(SimpleOrmException::class);
        $user->transactions;
    }

    #[Test]
    public function a_user_constructed_entity_keeps_its_initializer(): void
    {
        $user = new User();

        self::assertSame([], $user->transactions);
        self::assertSame([], $user->roles);
    }

    #[Test]
    public function singular_navigations_stay_null_until_loaded(): void
    {
        $user = SampleDatabase::insertUser($this->db, 'Ada', 'ada@example.com');
        $transaction = new Transaction();
        $transaction->userId = $user->id;
        $transaction->amount = Decimal::of('19.99');
        $transaction->createdAtUtc = SampleDatabase::seedTime();
        $this->db->insert($transaction);
        $transaction = $this->db->from(Transaction::class)->single();
        self::assertInstanceOf(Transaction::class, $transaction);

        self::assertNull($transaction->user);
        self::assertSame($user->id, $transaction->userId);
    }

    #[Test]
    public function the_trait_keeps_reporting_an_undefined_property_as_php_does(): void
    {
        $user = new User();
        $warned = null;
        set_error_handler(static function (int $severity, string $message) use (&$warned): bool {
            $warned = $message;

            return true;
        });
        try {
            /** @phpstan-ignore-next-line */
            $value = $user->noSuchProperty;
        } finally {
            restore_error_handler();
        }

        self::assertNull($value);
        self::assertStringContainsString('Undefined property', (string) $warned);
    }

    #[Test]
    public function loading_an_unknown_navigation_is_rel_001_even_for_one_entity(): void
    {
        $user = SampleDatabase::insertUser($this->db, 'Ada', 'ada@example.com');

        try {
            $this->db->load($user, 'NoSuchNavigation');
            self::fail('REL-001 expected');
        } catch (SimpleOrmException $e) {
            self::assertSame('REL-001', $e->errorCode);
            self::assertStringContainsString('transactions', $e->getMessage());
        }
    }

    #[Test]
    public function an_empty_batch_with_a_bad_navigation_is_a_silent_no_op_only_when_no_class_is_known(): void
    {
        $this->db->loadEach([], 'NoSuchNavigation');
        self::assertTrue(true);
    }

    #[Test]
    public function include_validates_the_navigation_before_any_sql_runs(): void
    {
        try {
            $this->db->from(User::class)->where(Criteria::eq('id', -1))->include('NoSuchNavigation')->toList();
            self::fail('REL-001 expected');
        } catch (SimpleOrmException $e) {
            self::assertSame('REL-001', $e->errorCode);
        }
    }
}

/** The same `users` table without the trait: the guard falls back to PHP's own uninitialized-property error. */
#[Table('users')]
final class PlainUser
{
    #[Key]
    #[Generated]
    #[Column]
    public int $id;

    #[Column]
    public string $name;

    #[Column]
    public string $email;

    #[Column]
    public ?string $displayName = null;

    #[Column('created_at')]
    public \DateTimeImmutable $createdAtUtc;

    #[Column('updated_at')]
    public ?\DateTimeImmutable $updatedAtUtc = null;

    #[OneToMany(Transaction::class, 'userId')]
    public private(set) array $transactions = [];
}
