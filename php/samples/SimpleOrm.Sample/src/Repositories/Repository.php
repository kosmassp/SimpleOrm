<?php

declare(strict_types=1);

namespace SimpleOrm\Sample\Repositories;

use SimpleOrm\Query\Criteria;
use SimpleOrm\Query\CriteriaQuery;
use SimpleOrm\Session\Db;

/**
 * The per-entity accessor (ADR-0016): generic CRUD, key reads, and criteria over
 * one injected session, the base every app-side data layer was rewriting by
 * hand. Instance-based, session-first (§7.17): no statics, no ambient state;
 * one repository instance per unit of work, subclass to add entity-specific
 * methods. The C# reference ships this as `SimpleOrm.Repository<TEntity>`; the
 * PHP port has no library counterpart yet, so the sample carries it (PHP has no
 * generics, so the entity class is a constructor argument).
 *
 * @template TEntity of object
 */
abstract class Repository
{
    /** @param class-string<TEntity> $entityClass */
    public function __construct(
        protected readonly Db $db,
        private readonly string $entityClass,
    ) {
    }

    /** @param TEntity $entity */
    public function insert(object $entity): void
    {
        $this->db->insert($entity);
    }

    /** @param TEntity $entity */
    public function update(object $entity): void
    {
        $this->db->update($entity);
    }

    /**
     * Update by column list (ADR-0028): writes only the named properties; version rules unchanged.
     *
     * @param TEntity $entity
     * @param list<string> $properties
     */
    public function updateOnly(object $entity, array $properties): void
    {
        $this->db->updateOnly($entity, $properties);
    }

    /** A key (or key-part list) deletes by key; passing the entity gives the version-checked delete (§7.16). */
    public function delete(mixed $keyOrEntity): void
    {
        $this->db->delete($this->entityClass, $keyOrEntity);
    }

    /** @return TEntity */
    public function get(mixed $key): object
    {
        /** @var TEntity $entity */
        $entity = $this->db->get($this->entityClass, $key);

        return $entity;
    }

    /** @return ?TEntity */
    public function getOrDefault(mixed $key): ?object
    {
        /** @var ?TEntity $entity */
        $entity = $this->db->getOrDefault($this->entityClass, $key);

        return $entity;
    }

    /** @return list<TEntity> */
    public function getAll(): array
    {
        /** @var list<TEntity> $entities */
        $entities = $this->db->queryAll($this->entityClass);

        return $entities;
    }

    /**
     * Criteria find (ADR-0012); compose with `Criteria::and()`/`or()` for more.
     *
     * @return list<TEntity>
     */
    public function find(Criteria $criteria): array
    {
        /** @var list<TEntity> $entities */
        $entities = $this->query()->where($criteria)->toList();

        return $entities;
    }

    /** The full criteria chain for ordering and paging. */
    public function query(): CriteriaQuery
    {
        return $this->db->from($this->entityClass);
    }
}
