<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Support;

use PDO;
use SimpleOrm\Dialect\Dialect;
use SimpleOrm\Metadata\ColumnType;
use SimpleOrm\Metadata\EntityMap;
use SimpleOrm\Metadata\PropertyMap;
use SimpleOrm\Query\SelectAst;

/**
 * The one test-local {@see Dialect} decorator base (CODING-STANDARD §8, §7): every member
 * delegates to a real dialect, a subclass overrides only the member it observes or forces
 * (`CountingSelectDialect` counts `selectSql`, `RowValueInDisabledDialect` forces
 * `supportsRowValueIn`, `RecordingUpdateDialect` records what an update was asked to
 * render). It decorates the dialect seam — a real object — never the database itself, so
 * the "no mocks" rule of §7 stands. Not `final` by design: extending it is its purpose.
 */
abstract class DelegatingDialect implements Dialect
{
    public function __construct(protected readonly Dialect $inner)
    {
    }

    public function createConnection(string $connectionString): PDO
    {
        return $this->inner->createConnection($connectionString);
    }

    public function quoteIdentifier(string $identifier): string
    {
        return $this->inner->quoteIdentifier($identifier);
    }

    public function createTableSql(EntityMap $map): string
    {
        return $this->inner->createTableSql($map);
    }

    public function createIndexSql(EntityMap $map): array
    {
        return $this->inner->createIndexSql($map);
    }

    public function createViewSql(EntityMap $map): string
    {
        return $this->inner->createViewSql($map);
    }

    public function insertSql(EntityMap $map): string
    {
        return $this->inner->insertSql($map);
    }

    public function updateSql(EntityMap $map): string
    {
        return $this->inner->updateSql($map);
    }

    public function updateOnlySql(EntityMap $map, array $properties): string
    {
        return $this->inner->updateOnlySql($map, $properties);
    }

    public function deleteSql(EntityMap $map, bool $checkVersion): string
    {
        return $this->inner->deleteSql($map, $checkVersion);
    }

    public function reportsAffectedRowsBySelect(): bool
    {
        return $this->inner->reportsAffectedRowsBySelect();
    }

    public function limitOffsetClause(?string $limitParameter, ?string $offsetParameter): string
    {
        return $this->inner->limitOffsetClause($limitParameter, $offsetParameter);
    }

    public function selectSql(SelectAst $select, callable $bindParameter): string
    {
        return $this->inner->selectSql($select, $bindParameter);
    }

    public function pagingRequiresOrderBy(): bool
    {
        return $this->inner->pagingRequiresOrderBy();
    }

    public function supportsRowValueIn(): bool
    {
        return $this->inner->supportsRowValueIn();
    }

    public function supportsArrayParameters(): bool
    {
        return $this->inner->supportsArrayParameters();
    }

    public function bindsTemporalsNatively(): bool
    {
        return $this->inner->bindsTemporalsNatively();
    }

    public function supportsMaterializedViews(): bool
    {
        return $this->inner->supportsMaterializedViews();
    }

    public function supportsProcedures(): bool
    {
        return $this->inner->supportsProcedures();
    }

    public function supportsTransactionalDdl(): bool
    {
        return $this->inner->supportsTransactionalDdl();
    }

    public function columnsInfoSql(): string
    {
        return $this->inner->columnsInfoSql();
    }

    public function viewDefinitionSql(): string
    {
        return $this->inner->viewDefinitionSql();
    }

    public function indexesInfoSql(): string
    {
        return $this->inner->indexesInfoSql();
    }

    public function isDeclaredTypeCompatible(string $declaredType, ColumnType $type): bool
    {
        return $this->inner->isDeclaredTypeCompatible($declaredType, $type);
    }

    public function storageType(PropertyMap $property): string
    {
        return $this->inner->storageType($property);
    }

    public function beginMigrationRunLock(PDO $connection): void
    {
        $this->inner->beginMigrationRunLock($connection);
    }

    public function versionTableSql(): string
    {
        return $this->inner->versionTableSql();
    }

    public function renameTableSql(string $fromName, string $toName): string
    {
        return $this->inner->renameTableSql($fromName, $toName);
    }

    public function renameColumnSql(string $table, string $fromName, string $toName): string
    {
        return $this->inner->renameColumnSql($table, $fromName, $toName);
    }

    public function addColumnSql(string $table, string $column, string $storageType, bool $nullable, ?string $defaultSql): string
    {
        return $this->inner->addColumnSql($table, $column, $storageType, $nullable, $defaultSql);
    }

    public function dropColumnSql(string $table, string $column): string
    {
        return $this->inner->dropColumnSql($table, $column);
    }

    public function dropTableSql(string $table): string
    {
        return $this->inner->dropTableSql($table);
    }

    public function dropIndexSql(string $table, string $index): string
    {
        return $this->inner->dropIndexSql($table, $index);
    }
}
