package workers

import (
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins"
)

// TestRolloutDoesNotReloadARetriedWorkerThatForkedBeforeTheReloadLoop forces the interleaving where a retried failed
// worker (S3) forks its replacement child (setting record.child with empty shipped) while Rollout is outside the
// component loop in route.Retry and holds startChild inside options.Lock (before startChild sets child.shipped) until
// Rollout has run its post-Retry section. Rollout must list S3 once (with its start load report) and must not send a
// ToWorkerReload to the child it just restarted. Both the normal Retry()==true path and the already-resolving
// Retry()==false path are covered.
func TestRolloutDoesNotReloadARetriedWorkerThatForkedBeforeTheReloadLoop(t *testing.T) {
	run := func(t *testing.T, alreadyResolving bool) {
		f := newFleet(t, fleetOptions{})
		one, two := f.get(fixtureNotes, "S1"), f.get(fixtureNotes, "S2")
		control := f.get(fixtureControl, "S3")
		failed := f.get(fixtureNotes, "S3")
		defer func() {
			for _, reference := range []interface{ Release() }{one, two, failed, control} {
				reference.Release()
			}
		}()
		if _, err := control.Call("crash", float64(1), background); err != nil {
			t.Fatal(err)
		}
		until(t, func() bool { return f.stateOf("S3") != nil && f.stateOf("S3").Status == WorkerFailed })
		f.label("v2")
		f.untilDirty()
		built := f.build(f.dirty())

		inLock := make(chan struct{})
		releaseLock := make(chan struct{})
		var lockOnce, releaseOnce sync.Once
		release := func() { releaseOnce.Do(func() { close(releaseLock) }) }
		defer release()

		var s3Child *child
		var s3ReloadingAfterFork chan struct{}
		origLock := f.workers.options.Lock
		f.workers.options.Lock = func(address contracts.Address) (string, error) {
			if address.Key == session("S3").Key {
				lockOnce.Do(func() {
					f.workers.loop.Run(func() {
						if record := f.workers.addresses[address.Key]; record != nil {
							s3Child = record.child
							if s3Child != nil {
								s3ReloadingAfterFork = s3Child.reloading
							}
						}
					})
					close(inLock)
				})
				<-releaseLock
			}
			return origLock(address)
		}
		origRetry := f.workers.route.Retry
		f.workers.route.Retry = func(address contracts.Address) bool {
			if address.Key != session("S3").Key {
				return origRetry(address)
			}
			var ok bool
			if alreadyResolving {
				if !origRetry(address) {
					t.Error("first Retry of the failed address started no resolve")
				}
				ok = origRetry(address)
				if ok {
					t.Error("second Retry of the address started another resolve while the first runs")
				}
			} else {
				ok = origRetry(address)
			}
			<-inLock
			return ok
		}

		type result struct {
			hosts []string
			err   error
		}
		finished := make(chan result, 1)
		go func() {
			out, err := f.plugins.Rollout([]plugins.RolloutStep{f.workers.Step("workers")}, built, background)
			if err != nil {
				finished <- result{err: err}
				return
			}
			finished <- result{hosts: listed(out.Steps[0].Hosts)}
		}()
		// S1 reloads in the same Rollout post-Retry section that inspects S3; once S1 has v2, that section has finished.
		until(t, func() bool { return labelOf(t, one) == "S1 v2" })

		var reloaded bool
		f.workers.loop.Run(func() {
			reloaded = s3Child != nil && s3Child.reloading != s3ReloadingAfterFork
		})
		release()
		got := <-finished
		if got.err != nil {
			t.Fatal(got.err)
		}
		requireListed(t, got.hosts,
			"session|S1 "+changedNotes,
			"session|S2 "+changedNotes,
			"session|S3 added=[@test/notes] changed=[] removed=[]")
		if reloaded {
			t.Fatal("Rollout queued a ToWorkerReload on the starting child of the retried worker S3")
		}
		until(t, func() bool {
			return labelOf(t, one) == "S1 v2" && labelOf(t, two) == "S2 v2" && labelOf(t, failed) == "S3 v2"
		})
	}
	t.Run("retry_starts_resolve", func(t *testing.T) { run(t, false) })
	t.Run("resolve_already_running", func(t *testing.T) { run(t, true) })
}
