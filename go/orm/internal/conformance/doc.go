// Package conformance holds one data-driven runner per conformance/* folder
// (CLAUDE.md §9) — the executable definition of the library: entities, cases,
// crud-cases, ast, diff-cases, snapshot-cases, migrations-cases, amend-cases.
// The runners read the JSON from ../../../../conformance and are the
// definition of done: a feature without a passing conformance case is not
// done. load-cases is Level 2 and excluded (ADR-0027). The package has no
// non-test code.
package conformance
