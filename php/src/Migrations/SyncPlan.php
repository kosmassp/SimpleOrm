<?php

declare(strict_types=1);

namespace SimpleOrm\Migrations;

/**
 * The result of `SchemaSync::plan()` (ADR-0013 add.3 / ADR-0017): safe additive
 * statements, gated destructive ones, and changes sync cannot express safely.
 * Mirrors `SchemaSync.cs`'s nested `Plan` class; mutable lists so the planner
 * can build it up incrementally, exactly like the C# reference.
 */
final class SyncPlan
{
    /** @var list<string> safe, additive statements */
    public array $additive = [];

    /** @var list<string> destructive statements — apply only with an explicit allow-delete */
    public array $deletions = [];

    /** @var list<string> differences sync cannot express safely (`DDL-004`); write a migration */
    public array $unsupported = [];

    public function isEmpty(): bool
    {
        return $this->additive === [] && $this->deletions === [] && $this->unsupported === [];
    }
}
