package conformance

import "testing"

// TestHookCases_BehaveAsSpecified is the conformance/hook-cases/ runner (Level 3 M3, spec/hooks.md): the
// tracking-case step engine — one engine for every step-script folder — with the session clock fixed at
// 2026-08-29T00:00:00Z. Expectations on the audit columns are `value` expectations against that clock; a
// soft delete shows as an `update` statement over the flag and the stamp.
func TestHookCases_BehaveAsSpecified(t *testing.T) {
	runStepCases(t, "hook-cases")
}
