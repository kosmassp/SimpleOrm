<?php

declare(strict_types=1);

namespace SimpleOrm\Session;

use SimpleOrm\Dialect\Dialect;
use SimpleOrm\Metadata\ColumnType;
use SimpleOrm\Metadata\EntityMap;
use SimpleOrm\Metadata\PropertyMap;
use WeakMap;

/**
 * The generated CRUD text of one entity map on one dialect, rendered once (ADR-0048 item 1): the insert, the full-row
 * update, both delete forms, the key read, the select-all, and each column-list update by its ordered property list (at
 * most {@see MAX_COLUMN_LISTS} lists per map; beyond that a list is rendered uncached, as every list was before).
 *
 * Keyed by **identity** — a `WeakMap` on the {@see EntityMap} instance, then a `WeakMap` on the {@see Dialect} instance —
 * never by a name or a string that could collide across models, and an entry lives exactly as long as both of its keys
 * (CODING-STANDARD §10). The entry holds neither key, so it never keeps a map or a dialect alive. The dialect's write
 * renderings are taken as pure functions of their arguments — the seam's contract — so the first rendering is the one
 * every later call would have produced. The session still tells its render observer about every rendering it takes,
 * cached or not ({@see Db::observeRenderings()}).
 *
 * @internal the session's cache, not public API
 */
final class GeneratedSql
{
    /** How many distinct column lists one map caches (a dynamic caller cannot grow the cache without bound). */
    public const int MAX_COLUMN_LISTS = 256;

    /** @var WeakMap<EntityMap, WeakMap<Dialect, self>>|null */
    private static ?WeakMap $cache = null;

    private ?string $insert = null;

    private ?string $update = null;

    /** @var array<int, string> the two delete forms, by `$checkVersion` (0 or 1) */
    private array $delete = [];

    private ?string $selectByKey = null;

    private ?string $selectAll = null;

    /** @var array<string, string> column-list updates by their property names, joined with NUL in list order */
    private array $updateOnly = [];

    /** The storage types {@see keyOrderIsValueOrder()} accepts: an integer or exact numeric type, sized or not. */
    private const string EXACT_NUMERIC_STORAGE = '/^(integer|bigint|int|smallint|tinyint|numeric|decimal)(\s*\(\s*\d+\s*(,\s*\d+\s*)?\))?$/i';

    private ?bool $keyOrderIsValueOrder = null;

    private function __construct()
    {
    }

    /** The one entry for this map on this dialect. */
    public static function for(EntityMap $map, Dialect $dialect): self
    {
        self::$cache ??= new WeakMap();
        $byDialect = self::$cache[$map] ??= new WeakMap();

        return $byDialect[$dialect] ??= new self();
    }

    public function insert(EntityMap $map, Dialect $dialect): string
    {
        return $this->insert ??= $dialect->insertSql($map);
    }

    public function update(EntityMap $map, Dialect $dialect): string
    {
        return $this->update ??= $dialect->updateSql($map);
    }

    public function delete(EntityMap $map, Dialect $dialect, bool $checkVersion): string
    {
        return $this->delete[(int) $checkVersion] ??= $dialect->deleteSql($map, $checkVersion);
    }

    /**
     * The column-list update of `$properties`, in their order — the list is the key (names are unique within one map, so
     * the joined names identify the list exactly).
     *
     * @param list<PropertyMap> $properties
     */
    public function updateOnly(EntityMap $map, Dialect $dialect, array $properties): string
    {
        $key = implode("\0", array_map(static fn (PropertyMap $p): string => $p->propertyName(), $properties));
        if (isset($this->updateOnly[$key])) {
            return $this->updateOnly[$key];
        }

        $sql = $dialect->updateOnlySql($map, $properties);
        if (count($this->updateOnly) < self::MAX_COLUMN_LISTS) {
            $this->updateOnly[$key] = $sql;
        }

        return $sql;
    }

    /** The generated key select (ADR-0006): explicit column list, every key column ANDed against `@k0…`. */
    public function selectByKey(EntityMap $map, Dialect $dialect): string
    {
        if ($this->selectByKey !== null) {
            return $this->selectByKey;
        }

        $predicates = [];
        foreach ($map->keyProperties as $i => $key) {
            $predicates[] = $dialect->quoteIdentifier($key->columnName) . " = @k{$i}";
        }

        return $this->selectByKey = self::selectList($map, $dialect) . ' where ' . implode(' and ', $predicates);
    }

    /** The generated select-all: explicit column list, ordered by the key when one exists. */
    public function selectAll(EntityMap $map, Dialect $dialect): string
    {
        if ($this->selectAll !== null) {
            return $this->selectAll;
        }

        $sql = self::selectList($map, $dialect);
        if ($map->keyProperties !== []) {
            $sql .= ' order by ' . implode(', ', array_map(
                static fn (PropertyMap $k): string => $dialect->quoteIdentifier($k->columnName),
                $map->keyProperties,
            ));
        }

        return $this->selectAll = $sql;
    }

    /**
     * Whether the dialect's `ORDER BY` on the key already is spec/loading.md's value order (ADR-0048 item 4): **every key
     * part is an integer or decimal type and the dialect stores it as an integer or exact numeric type** (`integer`,
     * `bigint`, `int`, `smallint`, `tinyint`, `numeric`, `decimal`, sized or not). Then `ORDER BY` compares numbers as the
     * value comparison does, and a sort after fetching cannot move anything. SQLite's `TEXT` decimal (`'10'` before `'2'`)
     * fails the rule; strings (collations are not ordinal), GUIDs, temporals, booleans, enums, and bytes always fail it.
     */
    public function keyOrderIsValueOrder(EntityMap $map, Dialect $dialect): bool
    {
        if ($this->keyOrderIsValueOrder !== null) {
            return $this->keyOrderIsValueOrder;
        }

        $holds = $map->keyProperties !== [];
        foreach ($map->keyProperties as $key) {
            $numeric = in_array($key->type, [ColumnType::Int16, ColumnType::Int32, ColumnType::Int64, ColumnType::Decimal], true);
            if (!$numeric || preg_match(self::EXACT_NUMERIC_STORAGE, $dialect->storageType($map, $key)) !== 1) {
                $holds = false;
                break;
            }
        }

        return $this->keyOrderIsValueOrder = $holds;
    }

    private static function selectList(EntityMap $map, Dialect $dialect): string
    {
        return 'select ' . implode(', ', array_map(
            static fn (PropertyMap $p): string => $dialect->quoteIdentifier($p->columnName),
            $map->properties,
        )) . ' from ' . $dialect->quoteIdentifier((string) $map->relationName);
    }
}
