package uiview

import "testing"

// Every function registered with OnAbilities is called, in registration order, for each change of the abilities, and a
// removed one is not called again.
func TestOnAbilitiesCallsEveryWatcherUntilRemoved(t *testing.T) {
	r := New(Options{})
	var calls []string
	removeFirst := r.OnAbilities(func() { calls = append(calls, "first") })
	r.OnAbilities(func() {
		// The abilities are in force when a watcher runs: a mount made here is admitted.
		if r.Abilities() != nil {
			if _, err := r.Mount("host", mountArgs(t, "arrived", "x")); err != nil {
				t.Errorf("a mount made by a watcher: %v", err)
			}
		}
		calls = append(calls, "second")
	})
	att, _, err := r.Attach(*abilities(), nil)
	wantOK(t, "attach", err)
	if len(calls) != 2 || calls[0] != "first" || calls[1] != "second" {
		t.Fatalf("after an attach the watchers ran %v, want first, second", calls)
	}
	if st, serr := r.State("host", StateArgs{Key: "arrived"}); serr != nil || st.State != StateLive {
		t.Fatalf("the watcher's mount = %+v (%v)", st, serr)
	}
	removeFirst()
	removeFirst()
	calls = nil
	att.Detach()
	if len(calls) != 1 || calls[0] != "second" {
		t.Fatalf("after the removal the watchers ran %v, want second", calls)
	}
}
