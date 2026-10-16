<?php

declare(strict_types=1);

namespace SimpleOrm\Session;

use SimpleOrm\Metadata\EntityMapLoader;
use SimpleOrm\Metadata\RelationshipKind;

use function array_keys;
use function array_pop;
use function array_shift;
use function array_values;
use function max;
use function min;

/**
 * The foreign-key rank of entity types for the unit of work's write order (Level 3 M2,
 * spec/unit-of-work.md "Order of writes" and Clarifications, ADR-0036): a type ranks after every
 * type its relationship metadata says it depends on — its many-to-one targets, and the owners whose
 * one-to-many/one-to-one navigations point at it (a many-to-many link is its own entity, ranked
 * through its own many-to-one navigations). **Types in a dependency cycle (a strongly connected
 * component, a self-reference included) share one rank**, so the result never depends on the order
 * the types arrive in; the schedule order decides inside a rank.
 *
 * Ranks are computed over the given types and every type reachable from them through relationships
 * (a rank is only meaningful against the types it can depend on); components are found with
 * Tarjan's algorithm, which emits a component only after every component it depends on.
 *
 * @internal owned by {@see DbUnitOfWork}
 */
final class TypeRanks
{
    private int $next = 0;

    /** @var array<class-string, int> */
    private array $index = [];

    /** @var array<class-string, int> */
    private array $lowLink = [];

    /** @var array<class-string, true> */
    private array $onStack = [];

    /** @var list<class-string> */
    private array $stack = [];

    /** @var array<class-string, int> type → the rank of its component */
    private array $ranks = [];

    /** @var array<class-string, int> type → its component number */
    private array $component = [];

    private int $components = 0;

    /** @var array<int, int> component number → rank */
    private array $componentRank = [];

    /** @param array<class-string, array<class-string, true>> $dependsOn */
    private function __construct(private readonly array $dependsOn)
    {
    }

    /**
     * @param list<class-string> $types the types the flush involves
     * @return array<class-string, int>
     */
    public static function of(EntityMapLoader $maps, array $types): array
    {
        [$dependsOn, $discovered] = self::dependencies($maps, $types);
        $ranker = new self($dependsOn);
        foreach ($discovered as $type) {
            if (!isset($ranker->index[$type])) {
                $ranker->connect($type);
            }
        }

        return $ranker->ranks;
    }

    /**
     * @param list<class-string> $seeds
     * @return array{0: array<class-string, array<class-string, true>>, 1: list<class-string>} type → the types it depends on; every type reached
     */
    private static function dependencies(EntityMapLoader $maps, array $seeds): array
    {
        $dependsOn = [];
        $discovered = [];
        $queue = $seeds;
        while ($queue !== []) {
            $type = array_shift($queue);
            if (isset($discovered[$type])) {
                continue;
            }

            $discovered[$type] = $type;
            $dependsOn[$type] ??= [];
            foreach ($maps->load($type)->relationships as $relationship) {
                $target = $relationship->targetType;
                if ($relationship->kind === RelationshipKind::ManyToOne) {
                    $dependsOn[$type][$target] = true;
                } elseif ($relationship->kind === RelationshipKind::OneToMany || $relationship->kind === RelationshipKind::OneToOne) {
                    $dependsOn[$target][$type] = true;
                } else {
                    continue;
                }

                $queue[] = $target;
            }
        }

        return [$dependsOn, array_values($discovered)];
    }

    /** @param class-string $type */
    private function connect(string $type): void
    {
        $this->index[$type] = $this->lowLink[$type] = $this->next++;
        $this->stack[] = $type;
        $this->onStack[$type] = true;

        foreach (array_keys($this->dependsOn[$type] ?? []) as $dependency) {
            if (!isset($this->index[$dependency])) {
                $this->connect($dependency);
                $this->lowLink[$type] = min($this->lowLink[$type], $this->lowLink[$dependency]);
            } elseif (isset($this->onStack[$dependency])) {
                $this->lowLink[$type] = min($this->lowLink[$type], $this->index[$dependency]);
            }
        }

        if ($this->lowLink[$type] === $this->index[$type]) {
            $this->emitComponent($type);
        }
    }

    /** @param class-string $root the type the component was found from */
    private function emitComponent(string $root): void
    {
        $members = [];
        do {
            $top = array_pop($this->stack);
            unset($this->onStack[$top]);
            $this->component[$top] = $this->components;
            $members[] = $top;
        } while ($top !== $root);

        $rank = 0;
        foreach ($members as $member) {
            foreach (array_keys($this->dependsOn[$member] ?? []) as $dependency) {
                if ($this->component[$dependency] !== $this->components) {
                    $rank = max($rank, $this->componentRank[$this->component[$dependency]] + 1);
                }
            }
        }

        $this->componentRank[$this->components] = $rank;
        foreach ($members as $member) {
            $this->ranks[$member] = $rank;
        }

        $this->components++;
    }
}
