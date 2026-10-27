package core

import (
	"context"
	"testing"
	"time"
)

// The command timeout's seconds (ADR-0047, spec/session.md "Resilience"): whole seconds rounded up, at least 1; zero is
// unset.
func TestCommandTimeoutSecondsRoundsUpToWholeSecondsAtLeastOne(t *testing.T) {
	for _, c := range []struct{ in, want time.Duration }{
		{0, 0},
		{time.Nanosecond, time.Second},
		{200 * time.Millisecond, time.Second},
		{time.Second, time.Second},
		{1500 * time.Millisecond, 2 * time.Second},
		{5 * time.Minute, 5 * time.Minute},
	} {
		if got := CommandTimeoutSeconds(c.in); got != c.want {
			t.Errorf("CommandTimeoutSeconds(%s) = %s, want %s", c.in, got, c.want)
		}
	}
}

func TestCommandContextDerivesADeadlineOnlyWhenSet(t *testing.T) {
	parent := context.Background()
	same, cancel := CommandContext(parent, 0)
	cancel()
	if same != parent {
		t.Fatal("an unset timeout derives a context")
	}
	timed, cancel := CommandContext(parent, 200*time.Millisecond)
	defer cancel()
	deadline, ok := timed.Deadline()
	if !ok || time.Until(deadline) <= 500*time.Millisecond {
		t.Fatalf("a 200 ms timeout is a one-second deadline: %v %v", deadline, ok)
	}
	if CommandTimeoutFrom(WithCommandTimeout(parent, time.Second)) != time.Second || CommandTimeoutFrom(parent) != 0 {
		t.Fatal("the tooling's timeout travels on the context")
	}
}
