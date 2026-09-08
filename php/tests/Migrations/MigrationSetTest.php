<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Migrations;

use PHPUnit\Framework\Attributes\Test;
use PHPUnit\Framework\TestCase;
use SimpleOrm\Dialect\Dialect;
use SimpleOrm\Dialect\SqliteDialect;
use SimpleOrm\Errors\SimpleOrmException;
use SimpleOrm\Metadata\EntityMapLoader;
use SimpleOrm\Migrations\DownPlan;
use SimpleOrm\Migrations\MigrationSet;
use SimpleOrm\Migrations\MigrationStep;
use SimpleOrm\Migrations\MigrationVersion;
use SimpleOrm\Migrations\TableActions;
use SimpleOrm\Migrations\TableMigration;
use SimpleOrm\Migrations\VersionBuilder;
use SimpleOrm\Tests\Migrations\Fixtures\WellFormed\Table\Widget\V0001_Create;
use SimpleOrm\Tests\Migrations\Fixtures\WellFormed\Table\Widget\V0002_AddNote;
use SimpleOrm\Tests\Migrations\Support\Widget;

/**
 * Mirrors the discovery/validation half of `MigrationRunner.cs` (`MIG-001`
 * through `MIG-004`) — the part of the C# reference this port's `MigrationSet`
 * owns; applying SQL is a later, runner-phase area. `MIG-002`'s
 * same-object-composed-twice half needs entity resolution, so it is exercised
 * through `render()` with the real `EntityMapLoader`/`SqliteDialect`; every
 * other structural check needs no loader and runs at construction time.
 */
final class MigrationSetTest extends TestCase
{
    #[Test]
    public function of_orders_versions_by_number(): void
    {
        $v2 = $this->rootVersion(2);
        $v1 = $this->rootVersion(1);

        $set = MigrationSet::of($v2, $v1);

        self::assertSame([1, 2], array_map(static fn (MigrationVersion $v) => $v->version(), $set->versions()));
    }

    #[Test]
    public function malformed_root_class_name_is_mig_001(): void
    {
        $bad = new class extends MigrationVersion {
            public function compose(VersionBuilder $version): void
            {
            }
        };

        try {
            MigrationSet::of($bad);
            self::fail('expected MIG-001');
        } catch (SimpleOrmException $exception) {
            self::assertSame('MIG-001', $exception->errorCode);
        }
    }

    #[Test]
    public function malformed_step_class_name_is_mig_001(): void
    {
        $badStep = new class extends MigrationStep {
            public function objectName(EntityMapLoader $maps): string
            {
                return 'x';
            }

            public function renderUp(EntityMapLoader $maps, Dialect $dialect): array
            {
                return [];
            }

            public function renderDown(EntityMapLoader $maps, Dialect $dialect): DownPlan
            {
                return new DownPlan([], [], []);
            }
        };
        $root = $this->rootVersion(1, $badStep);

        try {
            MigrationSet::of($root);
            self::fail('expected MIG-001');
        } catch (SimpleOrmException $exception) {
            self::assertSame('MIG-001', $exception->errorCode);
        }
    }

    #[Test]
    public function two_roots_declaring_the_same_version_is_mig_002(): void
    {
        try {
            MigrationSet::of($this->rootVersion(1), $this->rootVersion(1));
            self::fail('expected MIG-002');
        } catch (SimpleOrmException $exception) {
            self::assertSame('MIG-002', $exception->errorCode);
        }
    }

    #[Test]
    public function a_step_declaring_a_different_version_than_its_root_is_mig_003(): void
    {
        $step = $this->namedStep(2, 'Mismatch');
        $root = $this->rootVersion(1, $step);

        try {
            MigrationSet::of($root);
            self::fail('expected MIG-003');
        } catch (SimpleOrmException $exception) {
            self::assertSame('MIG-003', $exception->errorCode);
        }
    }

    #[Test]
    public function of_never_reports_an_orphan_step_there_is_no_universe_to_compare_against(): void
    {
        // Explicit-list construction (`of()`) has no directory to discover
        // "stray" candidates from, so MIG-004 cannot fire — mirrors the C#
        // reference's `MigrationRunner(Db, IEnumerable<MigrationVersion>)`
        // constructor, which passes an empty stray-candidate list too.
        $set = MigrationSet::of($this->rootVersion(1));

        self::assertCount(1, $set->versions());
    }

    #[Test]
    public function from_directory_discovers_and_orders_versions(): void
    {
        $set = MigrationSet::fromDirectory(
            __DIR__ . '/Fixtures/WellFormed',
            'SimpleOrm\\Tests\\Migrations\\Fixtures\\WellFormed',
        );

        self::assertSame([1, 2], array_map(static fn (MigrationVersion $v) => $v->version(), $set->versions()));

        $builder = new VersionBuilder();
        $set->versions()[0]->compose($builder);
        self::assertInstanceOf(V0001_Create::class, $builder->steps()[0]);

        $builder2 = new VersionBuilder();
        $set->versions()[1]->compose($builder2);
        self::assertInstanceOf(V0002_AddNote::class, $builder2->steps()[0]);
    }

    #[Test]
    public function orphan_step_is_mig_004(): void
    {
        try {
            MigrationSet::fromDirectory(__DIR__ . '/Fixtures/Orphan', 'SimpleOrm\\Tests\\Migrations\\Fixtures\\Orphan');
            self::fail('expected MIG-004');
        } catch (SimpleOrmException $exception) {
            self::assertSame('MIG-004', $exception->errorCode);
        }
    }

    #[Test]
    public function render_resolves_object_names_and_computes_checksums(): void
    {
        $root = $this->rootVersion(1, $this->tableStep(1, 'CreateWidget'));
        $set = MigrationSet::of($root);

        $rendered = $set->render(new EntityMapLoader(), new SqliteDialect());

        self::assertCount(1, $rendered);
        self::assertSame(1, $rendered[0]->version);
        $step = $rendered[0]->steps[0];
        self::assertSame('widget', $step->objectName);
        self::assertStringContainsString('create table if not exists widget', $step->up[0]->sql);
        self::assertSame(64, strlen($step->checksum));
    }

    #[Test]
    public function same_object_composed_twice_is_mig_002(): void
    {
        // The only MIG-002 sub-case that needs entity resolution: two
        // different step instances both targeting `Widget` (same relation
        // name via the convention loader) composed by one root.
        $root = $this->rootVersion(1, $this->tableStep(1, 'First'), $this->tableStep(1, 'Second'));
        $set = MigrationSet::of($root);

        try {
            $set->render(new EntityMapLoader(), new SqliteDialect());
            self::fail('expected MIG-002');
        } catch (SimpleOrmException $exception) {
            self::assertSame('MIG-002', $exception->errorCode);
        }
    }

    private function tableStep(int $version, string $description): TableMigration
    {
        return new class($version, $description) extends TableMigration {
            public function __construct(private readonly int $number, private readonly string $label)
            {
            }

            public function version(): int
            {
                return $this->number;
            }

            public function description(): string
            {
                return $this->label;
            }

            public function entityClass(): string
            {
                return Widget::class;
            }

            public function action(TableActions $actions): void
            {
                $actions->createTable();
            }
        };
    }

    private function rootVersion(int $version, MigrationStep ...$steps): MigrationVersion
    {
        return new class($version, $steps) extends MigrationVersion {
            /** @param list<MigrationStep> $steps */
            public function __construct(private readonly int $number, private readonly array $steps)
            {
            }

            public function version(): int
            {
                return $this->number;
            }

            public function compose(VersionBuilder $version): void
            {
                foreach ($this->steps as $step) {
                    $version->apply($step);
                }
            }
        };
    }

    private function namedStep(int $version, string $description): MigrationStep
    {
        return new class($version, $description) extends MigrationStep {
            public function __construct(private readonly int $number, private readonly string $label)
            {
            }

            public function version(): int
            {
                return $this->number;
            }

            public function description(): string
            {
                return $this->label;
            }

            public function objectName(EntityMapLoader $maps): string
            {
                return 'x';
            }

            public function renderUp(EntityMapLoader $maps, Dialect $dialect): array
            {
                return [];
            }

            public function renderDown(EntityMapLoader $maps, Dialect $dialect): DownPlan
            {
                return new DownPlan([], [], []);
            }
        };
    }
}
