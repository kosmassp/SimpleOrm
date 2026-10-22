<?php

declare(strict_types=1);

namespace SimpleOrm\Migrations;

/**
 * A rendered statement paired with the action it came from, for error reporting
 * (§7.22; mirrors `dotnet/src/SimpleOrm/Migration.cs`'s `MigrationStatement`). A
 * statement carrying `$guardView` is a **precondition**, not SQL to execute: the
 * runner phase compares the view's live definition against `$sql`
 * (whitespace-normalized) before continuing (`MIG-012` on mismatch — views get
 * patched outside code in urgencies, and a migration must not silently overwrite
 * that). Guards still count into the step checksum.
 */
final readonly class MigrationStatement
{
    /**
     * @param string|null $guardView set on a view guard: the view whose live definition must equal `$sql`
     * @param string|null $guardTrigger set on a trigger guard (ADR-0042): the trigger name whose live definitions
     *        (`Dialect::triggerDefinitionSql()` rows) must be exactly the `$guardExpected` set
     * @param list<string> $guardExpected the expected create statements of a trigger guard, normalized
     */
    public function __construct(
        public string $sql,
        public string $origin,
        public ?string $guardView = null,
        public ?string $guardTrigger = null,
        public array $guardExpected = [],
    ) {
    }

    /**
     * The `MIG-012` trigger guard (ADR-0042) for the create statements a declaration renders to: each normalized
     * ({@see SchemaSnapshot::normalizeTriggerDdl()}); `$sql` joins them, so the expectation counts into the checksum.
     *
     * @param list<string> $createStatements
     */
    public static function expectTrigger(string $origin, string $triggerName, array $createStatements): self
    {
        $expected = array_map(SchemaSnapshot::normalizeTriggerDdl(...), $createStatements);
        sort($expected, SORT_STRING);   // the guard compares sets; the checksum joins them in this one order

        return new self(implode("\n;\n", $expected), $origin, null, $triggerName, $expected);
    }
}
