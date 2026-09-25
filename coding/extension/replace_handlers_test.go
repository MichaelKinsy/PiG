package extension

import "testing"

// A runner holds a copy of an Extension. After a subprocess restart the host
// replaces the handlers in place, so the copy dispatches exactly the new
// registration.
func TestReplaceEventHandlersReachesSharedCopies(t *testing.T) {
	ext := &Extension{Name: "restarting"}
	ext.InitializeEventHandlers()
	ext.AddEventHandler("before_restart", 1, func(...any) (any, error) { return nil, nil })
	runnerCopy := *ext

	fresh := &Extension{Name: "restarting"}
	fresh.InitializeEventHandlers()
	fresh.AddEventHandler("after_restart", 2, func(...any) (any, error) { return "new", nil })
	ext.ReplaceEventHandlers(fresh)

	if got := runnerCopy.EventHandlers("before_restart"); len(got) != 0 {
		t.Fatalf("runner still dispatches %d handlers the old process registered", len(got))
	}
	got := runnerCopy.EventHandlers("after_restart")
	if len(got) != 1 {
		t.Fatalf("runner dispatches %d handlers for the new registration, want 1", len(got))
	}
	if result, _ := got[0](); result != "new" {
		t.Fatalf("runner dispatched %v, want the new handler", result)
	}
	// A subscription made after the restart reaches the runner's copy.
	ext.AddEventHandler("later", 3, func(...any) (any, error) { return nil, nil })
	if len(runnerCopy.EventHandlers("later")) != 1 {
		t.Fatal("a subscription after the restart did not reach the runner's copy")
	}
}
