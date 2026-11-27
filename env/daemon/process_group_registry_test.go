package daemon

// pi: packages/env/daemon/src/exec.rs (a_registration_never_touches_a_later_one_with_the_same_pid)

import (
	"slices"
	"testing"
)

func TestRegistrationNeverTouchesALaterOneWithTheSamePid(t *testing.T) {
	// A pid no process has (above Linux's largest), so a wrong kill signals nothing.
	const pid = 99_999_999
	groups := newGroupRegistry()
	first := groups.register(pid)
	// The first command was reaped and its pid reused: a later command registers it.
	second := groups.register(pid)
	first.forget(groups)

	var signalled []int
	if first.killWith(groups, func(p int) { signalled = append(signalled, p) }) {
		t.Fatal("the first registration should not stand after a later registration with the same pid")
	}
	if len(signalled) != 0 {
		t.Fatalf("the first command signalled %v, want none", signalled)
	}
	groups.mu.Lock()
	gotToken := groups.entries[pid]
	groups.mu.Unlock()
	if gotToken != second.token {
		t.Fatalf("registry token = %d, want second token %d", gotToken, second.token)
	}
	if !second.killWith(groups, func(p int) { signalled = append(signalled, p) }) {
		t.Fatal("the later registration should stand")
	}
	if !slices.Equal(signalled, []int{pid}) {
		t.Fatalf("signalled = %v, want [%d]", signalled, pid)
	}
	second.forget(groups)
	groups.mu.Lock()
	remaining := len(groups.entries)
	groups.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("registry has %d entries after second.forget, want 0", remaining)
	}
}
