<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Session;

use DateTimeImmutable;
use DateTimeZone;
use PDOException;
use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use ReflectionProperty;
use SimpleOrm\Dialect\SqliteDialect;
use SimpleOrm\Errors\MappingException;
use SimpleOrm\Errors\SimpleOrmException;
use SimpleOrm\Metadata\Attributes\Column;
use SimpleOrm\Metadata\Attributes\CreatedAt;
use SimpleOrm\Metadata\Attributes\Generated;
use SimpleOrm\Metadata\Attributes\Key;
use SimpleOrm\Metadata\Attributes\SoftDelete;
use SimpleOrm\Metadata\Attributes\Table;
use SimpleOrm\Metadata\Attributes\UpdatedAt;
use SimpleOrm\Metadata\Attributes\Version;
use SimpleOrm\Metadata\Attributes\View;
use SimpleOrm\Metadata\AuditRole;
use SimpleOrm\Metadata\EntityMapBuilder;
use SimpleOrm\Metadata\EntityMapJson;
use SimpleOrm\Metadata\EntityMapLoader;
use SimpleOrm\Session\Db;
use SimpleOrm\Session\DbOptions;
use SimpleOrm\Session\TrackingMode;
use SimpleOrm\Tests\Sample\Models\Role;
use SimpleOrm\Tests\Sample\Models\User;
use SimpleOrm\Tests\Support\ConformanceClock;
use SimpleOrm\Tests\Support\ConformanceSeed;
use SimpleOrm\Tests\Support\RecordingUpdateDialect;
use SimpleOrm\Tests\Support\SampleDatabase;
use SimpleOrm\Tests\Support\TempDatabase;

/**
 * The declared hooks (Level 3 M3, ADR-0034 §7c, spec/hooks.md) on a real temp-file SQLite database: the
 * `MAP-026` declaration shapes, a versioned soft-delete entity (`CRUD-012`, `CRUD-010`, the version bump),
 * the clock's UTC rule (`VAL-020`), the column-list update's stamp, and a failed flush putting the flag,
 * stamp, and created-at back. The shared behaviors are pinned data-driven by `Conformance\HookCasesTest`.
 */
final class HooksTest extends TestCase
{
    private TempDatabase $fixture;

    private RecordingUpdateDialect $recorder;

    private Db $db;

    private Db $observer;

    protected function setUp(): void
    {
        $this->fixture = TempDatabase::create();
        $this->recorder = new RecordingUpdateDialect(new SqliteDialect());
        $this->db = SampleDatabase::open(
            $this->fixture,
            new DbOptions($this->recorder, tracking: TrackingMode::Tracked, clock: ConformanceClock::fixed()),
        );
        $this->db->createTable(HookDocument::class);
        ConformanceSeed::insert($this->fixture);
        $this->observer = Db::open($this->fixture->connectionString(), new DbOptions(new SqliteDialect()));
    }

    protected function tearDown(): void
    {
        $this->observer->close();
        $this->db->close();
        $this->fixture->delete();
    }

    private static function clock(): DateTimeImmutable
    {
        return (ConformanceClock::fixed())();
    }

    private function code(callable $action): string
    {
        try {
            $action();
        } catch (SimpleOrmException $e) {
            return $e->errorCode;
        }

        self::fail('expected a SimpleOrmException, none was thrown');
    }

    private function newDocument(string $title): HookDocument
    {
        $document = new HookDocument();
        $document->title = $title;
        $this->db->insert($document);

        return $document;
    }

    // --- declaration rules: MAP-026 --------------------------------------------------------------------

    /** @return list<string> */
    private function codesOf(string $entityType): array
    {
        try {
            (new EntityMapLoader())->load($entityType);
            self::fail("{$entityType} was expected to fail loading");
        } catch (MappingException $e) {
            return array_map(static fn ($error) => $error->code, $e->errors);
        }
    }

    #[Test]
    public function an_audit_column_that_is_not_temporal_is_map026(): void
    {
        self::assertSame(['MAP-026'], $this->codesOf(HookBadCreatedAtType::class));
    }

    #[Test]
    public function a_nullable_soft_delete_flag_is_map026(): void
    {
        self::assertSame(['MAP-026'], $this->codesOf(HookNullableFlag::class));
    }

    #[Test]
    public function a_hook_on_a_key_is_map026(): void
    {
        self::assertSame(['MAP-026'], $this->codesOf(HookOnKey::class));
    }

    #[Test]
    public function two_updated_at_columns_are_map026(): void
    {
        self::assertSame(['MAP-026'], $this->codesOf(HookTwoUpdatedAt::class));
    }

    #[Test]
    public function two_hooks_on_one_property_are_map026(): void
    {
        self::assertSame(['MAP-026'], $this->codesOf(HookTwoOnOneProperty::class));
    }

    #[Test]
    public function a_hook_without_column_is_map026(): void
    {
        self::assertContains('MAP-026', $this->codesOf(HookWithoutColumn::class));
    }

    #[Test]
    public function a_hook_on_a_view_is_map026(): void
    {
        self::assertSame(['MAP-026'], $this->codesOf(HookOnView::class));
    }

    #[Test]
    public function the_declared_hooks_are_on_the_map_and_undeclared_entities_have_none(): void
    {
        $maps = new EntityMapLoader();
        $role = $maps->load(Role::class);

        self::assertSame('isDeleted', $role->softDeleteProperty()?->propertyName());
        self::assertSame('createdAtUtc', $role->createdAtProperty()?->propertyName());
        self::assertSame(AuditRole::UpdatedAt, $role->updatedAtProperty()?->audit);

        $document = $maps->load(HookDocument::class);
        self::assertSame('openedAt', $document->createdAtProperty()?->propertyName());
        self::assertNull($maps->load(User::class)->softDeleteProperty());
    }

    #[Test]
    public function the_manual_builder_declares_hooks_and_validates_them_like_the_attributes(): void
    {
        $map = EntityMapBuilder::for(HookDocument::class)->table('hook_documents')
            ->column('id', key: true, generated: true)
            ->column('title')
            ->column('archived', softDelete: true)
            ->column('openedAt', name: 'opened_at', audit: AuditRole::CreatedAt)
            ->build();
        self::assertSame('archived', $map->softDeleteProperty()?->propertyName());
        self::assertSame('openedAt', $map->createdAtProperty()?->propertyName());
        self::assertNull($map->updatedAtProperty());
        self::assertStringContainsString('"softDelete": true', EntityMapJson::export($map, new EntityMapLoader()));

        try {
            EntityMapBuilder::for(HookDocument::class)->column('id', key: true, generated: true)
                ->column('title', audit: AuditRole::UpdatedAt)->build();
            self::fail('a string audit column was expected to fail loading');
        } catch (MappingException $e) {
            self::assertSame('MAP-026', $e->errorCode);
        }
    }

    // --- the clock -------------------------------------------------------------------------------------

    #[Test]
    public function a_clock_that_returns_a_non_utc_instant_is_val020(): void
    {
        $session = Db::open($this->fixture->connectionString(), new DbOptions(
            new SqliteDialect(),
            clock: static fn (): DateTimeImmutable => new DateTimeImmutable('2026-01-01T00:00:00', new DateTimeZone('Europe/Paris')),
        ));
        $user = new User();
        $user->name = 'Linus';
        $user->email = 'linus@example.com';

        try {
            self::assertSame('VAL-020', $this->code(static fn () => $session->insert($user)));
        } finally {
            $session->close();
        }
    }

    #[Test]
    public function the_default_clock_is_the_utc_wall_clock(): void
    {
        $before = new DateTimeImmutable('now', new DateTimeZone('UTC'));
        $now = (new DbOptions(new SqliteDialect()))->now();
        $after = new DateTimeImmutable('now', new DateTimeZone('UTC'));

        self::assertGreaterThanOrEqual($before->getTimestamp(), $now->getTimestamp());
        self::assertLessThanOrEqual($after->getTimestamp(), $now->getTimestamp());
        self::assertSame('UTC', $now->getTimezone()->getName());
    }

    // --- [CreatedAt] / [UpdatedAt] ---------------------------------------------------------------------

    #[Test]
    public function an_uninitialized_created_at_takes_the_clock_and_a_set_one_is_kept(): void
    {
        $filled = $this->newDocument('filled');
        $kept = new HookDocument();
        $kept->title = 'kept';
        $kept->openedAt = new DateTimeImmutable('2020-05-05T05:05:05Z');
        $this->db->insert($kept);

        self::assertEquals(self::clock(), $filled->openedAt);
        self::assertEquals(new DateTimeImmutable('2020-05-05T05:05:05Z'), $kept->openedAt);
        self::assertNull($filled->touchedAt, 'the update stamp is not written on insert');
    }

    #[Test]
    public function update_only_adds_the_stamp_after_the_listed_columns_and_never_twice(): void
    {
        $document = $this->newDocument('first');
        $document->title = 'second';
        $this->recorder->updates = [];
        $this->db->updateOnly($document, ['title']);
        $document->title = 'third';
        $this->db->updateOnly($document, ['title', 'touchedAt']);

        self::assertSame([['title', 'touchedAt'], ['title', 'touchedAt']], $this->recorder->updates);
        self::assertEquals(self::clock(), $this->observer->get(HookDocument::class, $document->id)->touchedAt);
    }

    #[Test]
    public function a_refused_update_leaves_the_previous_stamp_on_the_instance(): void
    {
        $document = $this->newDocument('first');
        $elsewhere = $this->observer->get(HookDocument::class, $document->id);
        $elsewhere->title = 'elsewhere';
        $this->observer->update($elsewhere);

        $document->title = 'stale';
        self::assertSame('CRUD-010', $this->code(fn () => $this->db->update($document)));
        self::assertNull($document->touchedAt);
    }

    // --- [SoftDelete] ----------------------------------------------------------------------------------

    #[Test]
    public function the_version_checked_entity_form_flags_the_row_stamps_and_bumps_the_version(): void
    {
        $document = $this->newDocument('keep me');
        $this->recorder->statements = [];

        $this->db->delete(HookDocument::class, $document);

        self::assertSame([['op' => 'update', 'entity' => 'HookDocument', 'columns' => ['archived', 'touchedAt']]], $this->recorder->statements);
        self::assertTrue($document->archived);
        self::assertEquals(self::clock(), $document->touchedAt);
        self::assertSame(1, $document->version);
        self::assertFalse($this->db->isTracked($document));

        $stored = $this->observer->get(HookDocument::class, $document->id);
        self::assertTrue($stored->archived, 'reads never filter on the flag');
        self::assertSame(1, $stored->version);
    }

    #[Test]
    public function the_by_key_form_of_a_versioned_soft_delete_is_crud012(): void
    {
        $document = $this->newDocument('versioned');

        self::assertSame('CRUD-012', $this->code(fn () => $this->db->delete(HookDocument::class, $document->id)));
        self::assertFalse($this->observer->get(HookDocument::class, $document->id)->archived);
    }

    #[Test]
    public function a_stale_entity_form_soft_delete_is_crud010_and_leaves_the_flag_unset(): void
    {
        $document = $this->newDocument('stale soon');
        $elsewhere = $this->observer->get(HookDocument::class, $document->id);
        $elsewhere->title = 'elsewhere';
        $this->observer->update($elsewhere);

        self::assertSame('CRUD-010', $this->code(fn () => $this->db->delete(HookDocument::class, $document)));

        self::assertFalse($document->archived, 'the refused statement puts the flag back');
        self::assertNull($document->touchedAt);
        self::assertSame(0, $document->version);
        self::assertTrue($this->db->isTracked($document), 'a refused delete leaves the instance tracked');
    }

    #[Test]
    public function a_soft_delete_of_a_missing_key_is_crud001_and_by_key_forgets_the_held_instance(): void
    {
        self::assertSame('CRUD-001', $this->code(fn () => $this->db->delete(Role::class, 99)));

        $role = $this->db->get(Role::class, 2);
        $this->db->delete(Role::class, 2);

        self::assertFalse($this->db->isTracked($role));
        self::assertTrue($this->observer->get(Role::class, 2)->isDeleted);
    }

    // --- a failed flush puts the hooks' effects back ---------------------------------------------------

    #[Test]
    public function a_failed_flush_puts_back_an_undone_soft_deletes_flag_stamp_and_version_and_an_updates_stamp(): void
    {
        $first = $this->newDocument('first');
        $second = $this->newDocument('second');
        $ada = $this->db->get(User::class, 1);
        $ada->name = 'Ada Lovelace';

        // Another session bumps the second document's version: its soft delete will be refused.
        $elsewhere = $this->observer->get(HookDocument::class, $second->id);
        $elsewhere->title = 'elsewhere';
        $this->observer->update($elsewhere);

        $this->db->remove($first);
        $this->db->remove($second);

        self::assertSame('CRUD-010', $this->code(fn () => $this->db->flush()));

        self::assertFalse($first->archived, 'the flag of the soft delete that ran is put back');
        self::assertNull($first->touchedAt);
        self::assertSame(0, $first->version);
        self::assertTrue($this->db->isTracked($first));
        self::assertNull($ada->updatedAtUtc, 'the stamp of the update that ran is put back');
        self::assertSame(['name'], $this->db->changesOf($ada));
        self::assertFalse($this->observer->get(HookDocument::class, $first->id)->archived, 'the flush owned its transaction');
        self::assertTrue($this->db->hasPendingWork());
    }

    #[Test]
    public function a_failed_flush_puts_a_filled_created_at_back_to_unset(): void
    {
        $fine = new User();
        $fine->name = 'Linus';
        $fine->email = 'linus@example.com';
        $duplicate = new User();
        $duplicate->name = 'Impostor';
        $duplicate->email = 'ada@example.com';   // users.email is unique
        $this->db->persist($fine);
        $this->db->persist($duplicate);

        try {
            $this->db->flush();
            self::fail('the unique index on users.email should refuse the second insert');
        } catch (PDOException) {
            // the provider's own exception propagates (CODING-STANDARD §4)
        }

        self::assertFalse((new ReflectionProperty(User::class, 'createdAtUtc'))->isInitialized($fine));
    }

    #[Test]
    public function a_flushed_soft_delete_remove_forgets_the_instance_and_a_clean_instance_is_not_stamped(): void
    {
        $role = $this->db->get(Role::class, 1);
        $clean = $this->db->get(User::class, 1);
        $this->db->remove($role);

        $summary = $this->db->flush();

        self::assertSame([0, 0, 1], [$summary->inserted, $summary->updated, $summary->deleted]);
        self::assertTrue($role->isDeleted);
        self::assertNull($clean->updatedAtUtc);
        self::assertFalse($this->db->isTracked($role));
    }
}

/** An audit column that is not a date: `MAP-026`. */
#[Table('hook_bad_created_at_type')]
final class HookBadCreatedAtType
{
    #[Key]
    #[Generated]
    #[Column]
    public int $id;

    #[Column]
    #[CreatedAt]
    public string $stamp = '';
}

/** A nullable soft-delete flag: `MAP-026`. */
#[Table('hook_nullable_flag')]
final class HookNullableFlag
{
    #[Key]
    #[Generated]
    #[Column]
    public int $id;

    #[Column]
    #[SoftDelete]
    public ?bool $gone = null;
}

/** A hook on a key column: `MAP-026`. */
#[Table('hook_on_key')]
final class HookOnKey
{
    #[Key]
    #[Column]
    #[CreatedAt]
    public DateTimeImmutable $at;
}

/** Two `[UpdatedAt]` columns on one entity: `MAP-026`. */
#[Table('hook_two_updated_at')]
final class HookTwoUpdatedAt
{
    #[Key]
    #[Generated]
    #[Column]
    public int $id;

    #[Column]
    #[UpdatedAt]
    public ?DateTimeImmutable $first = null;

    #[Column]
    #[UpdatedAt]
    public ?DateTimeImmutable $second = null;
}

/** Two hooks on one property: `MAP-026`. */
#[Table('hook_two_on_one')]
final class HookTwoOnOneProperty
{
    #[Key]
    #[Generated]
    #[Column]
    public int $id;

    #[Column]
    #[CreatedAt]
    #[UpdatedAt]
    public DateTimeImmutable $at;
}

/** A hook with no `[Column]`: `MAP-026`. */
#[Table('hook_without_column')]
final class HookWithoutColumn
{
    #[Key]
    #[Generated]
    #[Column]
    public int $id;

    #[CreatedAt]
    public ?DateTimeImmutable $at = null;
}

/** A hook on a view-backed entity — it could never act, so it is refused: `MAP-026`. */
#[View('hook_on_view', 'select 1 as id, \'2026-01-01T00:00:00Z\' as at')]
final class HookOnView
{
    #[Key]
    #[Column]
    public int $id;

    #[Column]
    #[CreatedAt]
    public DateTimeImmutable $at;
}

/** A versioned soft-delete entity with both audit hooks: the shape the by-key `CRUD-012` rule exists for. */
#[Table('hook_documents')]
final class HookDocument
{
    #[Key]
    #[Generated]
    #[Column]
    public int $id;

    #[Column]
    public string $title;

    #[Version]
    #[Column]
    public int $version = 0;

    #[Column]
    #[SoftDelete]
    public bool $archived = false;

    #[Column]
    #[CreatedAt]
    public DateTimeImmutable $openedAt;

    #[Column]
    #[UpdatedAt]
    public ?DateTimeImmutable $touchedAt = null;
}
