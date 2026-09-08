<?php

declare(strict_types=1);

namespace SimpleOrm\Sample\Repositories;

use SimpleOrm\Query\Criteria;
use SimpleOrm\Sample\Models\Tables\User;
use SimpleOrm\Session\Db;

/**
 * Per-entity repository: the generic surface comes from {@see Repository}
 * (ADR-0016); only entity-specific reads live here, as one-line criteria, no
 * per-table SQL.
 *
 * @extends Repository<User>
 */
final class UserRepository extends Repository
{
    public function __construct(Db $db)
    {
        parent::__construct($db, User::class);
    }

    public function getByEmail(string $email): User
    {
        /** @var User $user */
        $user = $this->query()->where(Criteria::eq('email', $email))->single();

        return $user;
    }

    /**
     * @param list<int> $ids
     * @return list<User>
     */
    public function getByIds(array $ids): array
    {
        /** @var list<User> $users */
        $users = $this->query()->where(Criteria::in('id', $ids))->orderBy('id')->toList();

        return $users;
    }
}
