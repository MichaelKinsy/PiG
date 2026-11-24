package workers

import (
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/host"
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

// TestRolloutDoesNotReloadAStartingWorkerWaitingInClearStaleLockWithSameScripts holds a non-retried worker's start
// inside options.Lock (after startChild has read scripts from Plugins.Scripts, before ClearStaleLock and ToWorkerStart)
// and runs an unrelated rollout with the same scripts. Like Pi (workers.ts:480-488, which sets child.shipped before
// clearStaleLock), Rollout must skip the starting child because its shipped key already matches, sending no
// ToWorkerReload and returning no host entry for it.
func TestRolloutDoesNotReloadAStartingWorkerWaitingInClearStaleLockWithSameScripts(t *testing.T) {
	f := newFleet(t, fleetOptions{})
	one := f.get(fixtureNotes, "S1")
	defer one.Release()

	// Pre-fetch the current worker scripts (v1) so the rollout ships the exact same script set S2 just read in startChild.
	scripts, err := f.plugins.Scripts("worker", background)
	if err != nil {
		t.Fatal(err)
	}

	inLock := make(chan struct{})
	releaseLock := make(chan struct{})
	var lockOnce, releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseLock) }) }
	defer release()

	var s2Child *child
	var s2ReloadingAfterFork chan struct{}
	origLock := f.workers.options.Lock
	f.workers.options.Lock = func(address contracts.Address) (string, error) {
		if address.Key == session("S2").Key {
			lockOnce.Do(func() {
				f.workers.loop.Run(func() {
					if record := f.workers.addresses[address.Key]; record != nil {
						s2Child = record.child
						if s2Child != nil {
							s2ReloadingAfterFork = s2Child.reloading
						}
					}
				})
				close(inLock)
			})
			<-releaseLock
		}
		return origLock(address)
	}

	// Start S2 (not a retried failed worker); its startChild reads v1 scripts then pauses in options.Lock.
	s2 := f.hold(fixtureNotes, "S2")
	defer s2.Release()
	<-inLock

	type result struct {
		hosts []string
		err   error
	}
	finished := make(chan result, 1)
	go func() {
		hosts, err := f.workers.Rollout(scripts, background)
		if err != nil {
			finished <- result{err: err}
			return
		}
		finished <- result{hosts: listed(hosts)}
	}()

	// Before releasing S2's lock, wait until either Rollout returned (expected: S1 and S2 both skipped) or a reload was
	// queued on S2 (the bug).
	until(t, func() bool {
		if len(finished) > 0 {
			return true
		}
		var queued bool
		f.workers.loop.Run(func() {
			queued = s2Child != nil && s2Child.reloading != s2ReloadingAfterFork
		})
		return queued
	})

	var reloaded bool
	f.workers.loop.Run(func() {
		reloaded = s2Child != nil && s2Child.reloading != s2ReloadingAfterFork
	})
	release()
	got := <-finished
	if got.err != nil {
		t.Fatal(got.err)
	}
	if len(got.hosts) != 0 {
		t.Fatalf("hosts = %q, want [] (S2 already has the same shipped scripts and must be skipped)", got.hosts)
	}
	if reloaded {
		t.Fatal("Rollout queued a ToWorkerReload on a starting worker waiting in ClearStaleLock with the same scripts")
	}
	until(t, func() bool { return refKind(s2) == host.RefStateLive && labelOf(t, s2) == "S2 v1" })
}
