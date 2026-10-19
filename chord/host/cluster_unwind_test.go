package host

// cluster.ts ChildReloads.#run: a prepare that fails or is aborted is unwound by `await rollback()`, then `endReload`,
// then `this.#operation = undefined` and the vote, in one synchronous continuation. Nothing runs between them, so the
// next frame the child handles finds the reload slot and the operation free together. The Go unwind runs off the loop
// and must make the same one section of it.

import (
	"testing"
	"time"
)

func TestCrossHostReloadAnAbortedChildFreesItsReloadSlotAndItsOperationInOneSection(t *testing.T) {
	s := setupCluster(t)
	waitLinked(t, s.server, s.worker)
	greet(t, getGreeter(t, s.server, clusterGreeter, "S1"), "a", nil)
	s.modules.set("greeter", greeterFacet("v2", false))
	// Whatever runs right after the section that frees the worker's reload slot (the next frame it handles) is queued
	// from the waiter the slot's end calls.
	pending := make(chan bool, 1)
	inHook, proceed := make(chan struct{}), make(chan struct{})
	reloadBetweenPreparePasses = func(host *HostImpl) {
		if host == s.server {
			// The vote deadline's callback runs on the server's loop: it comes after the worker is held, whatever the
			// scheduling, as a Node timer callback comes after the worker's microtasks.
			host.run(func() {
				select {
				case <-inHook:
				case <-time.After(5 * time.Second):
					t.Error("the worker did not reach its hold")
				}
			})
			return
		}
		host.run(func() {
			host.reloadWaiters = append(host.reloadWaiters, func() {
				host.post(func() { pending <- host.childReloads.operation != nil })
			})
		})
		close(inHook)
		<-proceed
	}
	t.Cleanup(func() { reloadBetweenPreparePasses = nil })
	// The worker is held between its prepare passes until the server's vote deadline passed and the worker handled
	// the abort that follows it.
	if _, err := s.server.Reload(nil, ReloadOptions{Remote: sessionRemote("greeter"), VoteDeadline: new(50 * time.Millisecond)}); !IsChordError(err, CodeAborted) {
		t.Fatalf("err = %v", err)
	}
	<-inHook
	waitFor(t, "the worker to cancel its operation", func() bool {
		return reviewOnLoop(s.worker, func() bool {
			operation := s.worker.childReloads.operation
			return operation != nil && operation.cancelled
		})
	})
	close(proceed)
	select {
	case registered := <-pending:
		if registered {
			t.Fatal("the reload slot was freed while the aborted operation was still registered")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the worker's aborted reload did not unwind")
	}
}
