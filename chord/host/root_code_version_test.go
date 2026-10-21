package host

import "testing"

// host.ts root: `this.joinRoot(rootId, options.codeVersion ?? this.current.id, fn, options)` is synchronous up to its first
// await, so a root takes the current code version and is counted in it before any callback queued behind can run. Here
// a callback queued before the root starts (as the reaction of a parent's retire frame or of a reload's commit is) moves
// the host to the next code version and retires this one the moment the section that started the root ends: the root
// must already be counted in its code version then, which keeps it, instead of being superseded by a retirement it
// could not have seen.
func TestRootIsCountedInItsCodeVersionBeforeAQueuedCallbackCanRetireIt(t *testing.T) {
	host := requestsHost(t, HostOptions{Name: "h"})
	// Queue the callback as if a drain were pending: nothing runs it until the next section that ends.
	host.mu.Lock()
	host.scheduled = true
	host.mu.Unlock()
	host.post(func() {
		host.codeVersionSeq++
		next := newCodeVersionState(host.codeVersionSeq, newOrderedMap[string, *Service]())
		host.codeVersions.Set(next.id, next)
		host.previous, host.current = host.current, next
		host.maybeRetire()
	})
	value, err := host.Root(func(ctx Ctx) (any, error) { return float64(ctx.CodeVersion()), nil }, RootOptions{})
	if err != nil || value != float64(1) {
		t.Fatalf("root = %v, %v; want the code version it started in (1)", value, err)
	}
	host.run(func() {
		if host.previous != nil || host.codeVersions.Has(1) {
			t.Fatalf("code version 1 was not retired once its root ended")
		}
	})
}
