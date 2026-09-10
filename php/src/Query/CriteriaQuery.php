<?php

declare(strict_types=1);

namespace SimpleOrm\Query;

use SimpleOrm\Errors\SimpleOrmException;
use SimpleOrm\Metadata\EntityMap;
use SimpleOrm\Session\Db;

/**
 * The session-first criteria chain (ADR-0012): `$db->from(User::class)
 * ->where(...)->orderBy(...)->limit(...)->toList()`. `where()` arguments (and
 * repeated calls) are implicitly ANDed. The rendered SELECT lists explicit
 * columns (never `*`), resolves property names through the metadata
 * (`QRY-006` when unknown), and binds every value as a parameter. Instances
 * come from {@see Db::from()}, which has already checked the source carries a
 * named relation (`QRY-005`). Eager loading (ADR-0022): `include()` names
 * navigations, `fetch()` picks the {@see FetchMode}; the graphs are identical
 * in every mode.
 */
final class CriteriaQuery
{
    /** @var list<Criteria> */
    private array $where = [];

    /** @var list<Ordering> */
    private array $orderings = [];

    /** @var list<string> */
    private array $includes = [];

    private FetchMode $fetch = FetchMode::MultiQuery;

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

    /**
     * Eager loading (ADR-0022): the named navigations load automatically with
     * the query — the root query plus one batch load per navigation (M3
     * machinery: visible round trips, correct paging, shared instances). An
     * unknown name is `REL-001`, even when the query matches no rows.
     * Repeatable; duplicates load once.
     */
    public function include(string ...$navigations): self
    {
        foreach ($navigations as $navigation) {
            if (!in_array($navigation, $this->includes, true)) {
                $this->includes[] = $navigation;
            }
        }

        return $this;
    }

    /** Chooses how the includes fetch (ADR-0022 add.1); no includes, no effect. */
    public function fetch(FetchMode $mode): self
    {
        $this->fetch = $mode;

        return $this;
    }

    /** @return list<object> */
    public function toList(): array
    {
        $map = $this->db->maps()->load($this->entityType);

        // Includes validate before any SQL runs (REL-001 in every mode).
        foreach ($this->includes as $navigation) {
            $this->db->resolveNavigation($map, $navigation);
        }

        if ($this->includes !== [] && $this->fetch === FetchMode::Join) {
            return $this->db->eagerJoinLoad($this->toAst($map), $this->entityType, $this->includes);
        }

        $ast = $this->toAst($map);
        if ($this->fetch === FetchMode::SubSelect && $this->includes !== []
            && ($ast->limit !== null || $ast->offset !== null)) {
            // A paged subselect re-evaluates the root, so the page must be
            // deterministic: the key columns break ordering ties. The tiebroken
            // AST drives BOTH the root query and every subquery, so the two
            // evaluations pick the same rows.
            $orderings = $ast->orderings;
            foreach ($map->keyProperties as $key) {
                $ordered = false;
                foreach ($orderings as $ordering) {
                    if (strcasecmp($ordering->property, $key->propertyName()) === 0) {
                        $ordered = true;
                        break;
                    }
                }

                if (!$ordered) {
                    $orderings[] = new Ordering($key->propertyName());
                }
            }

            $ast = new SelectAst($map, $ast->where, $orderings, $ast->limit, $ast->offset);
        }

        $rows = $this->db->executeAst($ast, $this->entityType);
        if ($this->includes !== []) {
            $ownerSubquery = $this->fetch === FetchMode::SubSelect ? $ast : null;
            foreach ($this->includes as $navigation) {
                $this->db->loadEachFrom($rows, $navigation, $ownerSubquery);
            }
        }

        return $rows;
    }

    /** Exactly one row: zero throws `QRY-001`, more than one throws `QRY-002`. */
    public function single(): object
    {
        $rows = $this->toList();

        return match (count($rows)) {
            1 => $rows[0],
            0 => throw new SimpleOrmException('QRY-001', $this->queryName(), 'expected exactly one row, found none'),
            default => throw new SimpleOrmException(
                'QRY-002',
                $this->queryName(),
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
                $this->queryName(),
                sprintf('expected at most one row, found %d', count($rows)),
            ),
        };
    }

    /** The query as data (§10.4, ADR-0012/0020); the dialect renders it. */
    public function toAst(EntityMap $map): SelectAst
    {
        return new SelectAst($map, $this->where, $this->orderings, $this->limit, $this->offset);
    }

    /** {@see Db::criteriaQueryName()} — the one implementation (CODING-STANDARD §8). */
    private function queryName(): string
    {
        return Db::criteriaQueryName($this->db->maps()->load($this->entityType));
    }
}
