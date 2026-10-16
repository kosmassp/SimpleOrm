package conformance

import "testing"

// TestMergeCases_BehaveAsSpecified is the conformance/merge-cases/ runner (Level 3 M3,
// spec/unit-of-work.md "Detach and merge"): the shared step engine plus the `detach` and `merge` verbs
// (`merge` names the instance it returns with `as`).
func TestMergeCases_BehaveAsSpecified(t *testing.T) {
	runStepCases(t, "merge-cases")
}
