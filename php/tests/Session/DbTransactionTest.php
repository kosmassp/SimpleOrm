<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Session;

use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use SimpleOrm\Errors\SimpleOrmException;
use SimpleOrm\Session\Db;
use SimpleOrm\Session\EmptyArgs;
use SimpleOrm\Session\Query;
use SimpleOrm\Tests\Support\SampleDatabase;
use SimpleOrm\Tests\Support\TempDatabase;

/** One transaction per session (§7.17): automatic enlistment, explicit commit, rollback on the alternative paths. Mirrors dotnet's `DbTransactionTests`. */
final class DbTransactionTest extends TestCase
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

    private function userCount(): int
    {
        $countUsers = Query::inline(EmptyArgs::class, 'int', 'select count(id) from users');

        return $this->db->querySingle($countUsers, EmptyArgs::value());
    }

    #[Test]
    public function committed_transaction_persists(): void
    {
        $tx = $this->db->begin();
        SampleDatabase::insertUser($this->db, 'Ada', 'ada@example.com');
        $tx->commit();

        self::assertSame(1, $this->userCount());
    }

    #[Test]
    public function rolled_back_transaction_discards_its_writes(): void
    {
        $tx = $this->db->begin();
        SampleDatabase::insertUser($this->db, 'Ada', 'ada@example.com');
        $tx->rollback();

        self::assertSame(0, $this->userCount());
    }

    #[Test]
    public function an_uncommitted_scope_rolls_back_when_garbage_collected(): void
    {
        (function (): void {
            $tx = $this->db->begin();
            SampleDatabase::insertUser($this->db, 'Ada', 'ada@example.com');
            unset($tx);   // never committed — the scope's destructor rolls back
        })();

        self::assertSame(0, $this->userCount());
    }

    #[Test]
    public function closing_the_session_rolls_back_an_active_transaction(): void
    {
        $tx = $this->db->begin();
        SampleDatabase::insertUser($this->db, 'Ada', 'ada@example.com');
        $this->db->close();
        unset($tx);   // the scope never committed; the session's own close() already rolled back

        $this->db = SampleDatabase::open($this->fixture);
        self::assertSame(0, $this->userCount());
    }

    #[Test]
    public function second_begin_on_the_same_session_is_tx001(): void
    {
        $tx = $this->db->begin();

        try {
            $this->db->begin();
            self::fail('expected TX-001');
        } catch (SimpleOrmException $e) {
            self::assertSame('TX-001', $e->errorCode);
        }

        $tx->rollback();
    }
}
