<?php

declare(strict_types=1);

namespace SimpleOrm\Migrations;

use Attribute;

/**
 * Marks a root migration version as a draft (Level 3 M5, spec/migrations.md "Draft migrations"): `simpleorm diff
 * --draft` writes it on the generated root, and `migrate` and `baseline` refuse a pending version carrying it
 * (`MIG-013`) until the author has reviewed the generated code and removed the attribute. A draft still composes,
 * renders, reports in `status`, and replays in `shadow` — it is a version in every respect but applicability.
 */
#[Attribute(Attribute::TARGET_CLASS)]
final readonly class Draft
{
}
