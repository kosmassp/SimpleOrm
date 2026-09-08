<?php

declare(strict_types=1);

namespace SimpleOrm\Migrations;

use SimpleOrm\Dialect\Dialect;
use SimpleOrm\Metadata\EntityMapLoader;

/** @internal used only by SqlVersion::compose() — a raw-SQL step built from data, not from a class name. */
final class SqlVersionRawStep extends MigrationStep
{
    public function __construct(
        private readonly int $rootVersion,
        private readonly SqlVersionStep $step,
    ) {
    }

    public function version(): int
    {
        return $this->rootVersion;
    }

    public function description(): string
    {
        return $this->step->description;
    }

    public function objectName(EntityMapLoader $maps): string
    {
        return $this->step->objectName;
    }

    public function renderUp(EntityMapLoader $maps, Dialect $dialect): array
    {
        $statements = [];
        if ($this->step->expectDefinition !== null) {
            $statements[] = new MigrationStatement(
                SchemaSnapshot::normalizeDdl($this->step->expectDefinition),
                'expect ' . $this->step->objectName,
                $this->step->objectName,
            );
        }

        foreach ($this->step->up as $sql) {
            $statements[] = new MigrationStatement($sql, 'sql ' . $this->step->objectName);
        }

        return $statements;
    }

    public function renderDown(EntityMapLoader $maps, Dialect $dialect): DownPlan
    {
        $core = array_map(
            fn (string $sql): MigrationStatement => new MigrationStatement($sql, 'sql ' . $this->step->objectName),
            $this->step->down,
        );

        return new DownPlan([], $core, []);
    }

    public function upRenames(EntityMapLoader $maps, Dialect $dialect): array
    {
        return $this->step->renames;
    }
}
