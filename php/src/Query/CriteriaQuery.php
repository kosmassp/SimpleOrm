<?php

declare(strict_types=1);

namespace SimpleOrm\Query;

use PDO;
use SimpleOrm\Errors\SimpleOrmException;
use SimpleOrm\Metadata\EntityMap;
use SimpleOrm\Metadata\PropertyMap;
use SimpleOrm\Parameters\PdoBinder;
use SimpleOrm\Parameters\SqlPlaceholders;
use SimpleOrm\Session\Db;

/**
 * The session-first criteria chain (ADR-0012): `$db->from(User::class)
 * ->where(...)->orderBy(...)->limit(...)->toList()`. `where()` arguments (and
 * repeated calls) are implicitly ANDed. The rendered SELECT lists explicit
 * columns (never `*`), resolves property names through the metadata
 * (`QRY-006` when unknown), and binds every value as a parameter. Instances
 * come from {@see Db::from()}, which has already checked the source carries a
 * named relation (`QRY-005`).
 */
final class CriteriaQuery
{
    /** @var list<Criteria> */
    private array $where = [];

    /** @var list<Ordering> */
    private array $orderings = [];

    private ?int $limit = null;

    private ?int $offset = null;

    public function __construct(
        private readonly Db $db,
        private readonly string $entityType,
    ) {
    }

    /** Adds criteria; multiple arguments and multiple calls are ANDed. */
    public function where(Criteria ...$criteria): self
    {
        $this->where = [...$this->where, ...$criteria];

        return $this;
    }

    public function orderBy(string $property, SortOrder $order = SortOrder::Asc): self
    {
        $this->orderings[] = new Ordering($property, $order);

        return $this;
    }

    public function limit(int $limit): self
    {
        $this->limit = $limit;

        return $this;
    }

    public function offset(int $offset): self
    {
        $this->offset = $offset;

        return $this;
    }

    /** @return list<object> */
    public function toList(): array
    {
        $map = $this->db->maps()->load($this->entityType);

        return $this->execute($this->toAst($map), $map);
    }

    /** Exactly one row: zero throws `QRY-001`, more than one throws `QRY-002`. */
    public function single(): object
    {
        $rows = $this->toList();

        return match (count($rows)) {
            1 => $rows[0],
            0 => throw new SimpleOrmException('QRY-001', self::queryName($this->entityType), 'expected exactly one row, found none'),
            default => throw new SimpleOrmException(
                'QRY-002',
                self::queryName($this->entityType),
                sprintf('expected exactly one row, found %d', count($rows)),
            ),
        };
    }

    public function singleOrDefault(): ?object
    {
        $rows = $this->toList();

        return match (count($rows)) {
            0 => null,
            1 => $rows[0],
            default => throw new SimpleOrmException(
                'QRY-002',
                self::queryName($this->entityType),
                sprintf('expected at most one row, found %d', count($rows)),
            ),
        };
    }

    /** The query as data (§10.4, ADR-0012/0020); the dialect renders it. */
    public function toAst(EntityMap $map): SelectAst
    {
        return new SelectAst($map, $this->where, $this->orderings, $this->limit, $this->offset);
    }

    private function execute(SelectAst $ast, EntityMap $map): array
    {
        $connection = $this->db->connection();
        $converter = $this->db->converter();
        $dialect = $this->db->options()->dialect;
        $queryName = self::queryName($this->entityType);

        // Binds criteria parameter values in render order (@c0…); the compared
        // property's conversion rules apply — an enum against an [EnumAsInt]
        // column binds as its number, not its name.
        $parameters = [];
        $bindParameter = static function (mixed $value, ?PropertyMap $property) use (&$parameters, $converter, $queryName): string {
            $name = 'c' . count($parameters);
            $parameters[$name] = $converter->toDatabase($value, "{$queryName} @{$name}", $property?->enumAsInt() ?? false);

            return '@' . $name;
        };

        $sql = SqlPlaceholders::toPdo($dialect->selectSql($ast, $bindParameter));
        $statement = $connection->prepare($sql);
        // PdoBinder::bindAndExecute, not a bare execute($parameters): the latter binds
        // everything as PDO::PARAM_STR, which a column SQLite gives no
        // declared affinity (a view's aggregate expression) never coerces back.
        PdoBinder::bindAndExecute($statement, $parameters);

        $columns = [];
        for ($i = 0; $i < $statement->columnCount(); $i++) {
            $meta = $statement->getColumnMeta($i);
            $columns[] = $meta !== false ? $meta['name'] : (string) $i;
        }

        $plan = $this->db->mapper()->createPlan($this->entityType, $columns, $queryName);

        $results = [];
        while (($row = $statement->fetch(PDO::FETCH_ASSOC)) !== false) {
            $results[] = $plan($row);
        }

        return $results;
    }

    private static function queryName(string $entityType): string
    {
        $slash = strrpos($entityType, '\\');
        $short = $slash === false ? $entityType : substr($entityType, $slash + 1);

        return $short . ' criteria';
    }
}
