<?php

declare(strict_types=1);

namespace SimpleOrm\Session;

use LogicException;
use SimpleOrm\Metadata\EntityMap;
use SimpleOrm\Metadata\RelationshipMap;
use SimpleOrm\Query\SelectAst;

/**
 * Explicit and batch relationship loading (Level 2 milestone 3, ADR-0021) —
 * the PHP counterpart of the C# `Db` partial in `DbLoading.cs` (PHP has no
 * partial classes, so the engine is a collaborator the session delegates to;
 * CODING-STANDARD §10). Nothing loads implicitly (§2, ADR-0019 add.1): a
 * navigation stays unloaded until one of these calls populates it, and
 * touching an unloaded navigation never fires SQL. Every call is a visible,
 * bounded set of round trips — one criteria query per navigation (two for
 * many-to-many: link rows, then targets), chunked only when a batch exceeds
 * the parameter budget.
 *
 * Key/FK tuples match by **structural equality of their values** — the §7.4
 * entity-identity rule — never by string tokens (ADR-0021 add.1: lossy
 * stringification silently loaded the wrong entity for DateTime and byte[]
 * keys). Collection results order by the target key, compared value-wise.
 * With an owner subquery (ADR-0022 add.1, SubSelect mode) the owner set is
 * `in (select …)` over the root query instead of a client-side key list — one
 * query per navigation, no chunking.
 */
final class DbLoading
{
    /** How many owners bind into one query before chunking (SQLite's parameter budget). */
    public const int LOAD_CHUNK_SIZE = 500;

    public function __construct(private readonly Db $db)
    {
    }

    /**
     * Loads one declared navigation for every entity in the list with one query
     * per chunk — never one per entity. Within one call, owners sharing a
     * many-to-one target share the same loaded instance. The session has
     * already resolved the navigation (`REL-001`) — a wrong name is a bug
     * regardless of how many entities happened to be in the list — and
     * returned early for an empty batch.
     *
     * @param list<object> $entities
     */
    public function loadEach(EntityMap $map, RelationshipMap $relationship, array $entities, ?SelectAst $ownerSubquery): void
    {
        throw new LogicException('DbLoading::loadEach is the Level 2 loading engine — not implemented yet (ADR-0032 step 2b)');
    }
}
