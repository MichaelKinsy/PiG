package host

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/store"
)

// The helpers of packages/chord/test/next/support.ts, ported in host_next_support_test.go. Pi's support module is test
// scaffolding the slice tests share and has no test of its own: these tests assert what its helpers document, through the host and the store they drive (the
// `restartable` runner's fail, retry and restart; `loaded`'s collected errors and failed plugins; `failures`; the
// `faultyStore`'s injected statement failure and held commit; `memoryStore`), so that a test built on them can trust
// what they do.

// support.ts `restartable`: "`fail()` fails the live attempt as a runtime failure of its owner does; `retry()` asks for
// the next attempt and resolves once it is live; `restart()` does both."
func TestSupportRestartableFailsRetriesAndRestartsTheLiveAttempt(t *testing.T) {
	run := restartable(t, func(int, Self) {}, loadedOptions{})
	if got := run.Attempts.len(); got != 1 {
		t.Fatalf("attempts after the get = %d, want 1 (object 1 is live)", got)
	}
	if got := exStateNow(t, run.Reference); got != "live" {
		t.Fatalf("state after the get = %q, want live", got)
	}
	run.Fail()
	if state := run.Reference.State().Value(); state.Kind != RefStateFailed || state.Cause.Kind != data.FailureBroken || state.Cause.Error.Message != "boom" {
		t.Fatalf("state after fail = %+v, want failed with the owner's cause boom", state)
	}
	run.Retry()
	if got := run.Attempts.len(); got != 2 {
		t.Fatalf("attempts after retry = %d, want 2", got)
	}
	if got := exStateNow(t, run.Reference); got != "live" {
		t.Fatalf("state after retry = %q, want live", got)
	}
	run.Restart()
	if got := run.Attempts.len(); got != 3 {
		t.Fatalf("attempts after restart = %d, want 3", got)
	}
	if got := exStateNow(t, run.Reference); got != "live" {
		t.Fatalf("state after restart = %q, want live", got)
	}
	run.Reference.Release()
	exCloseHost(t, run.Host)
}

// support.ts `loaded`: "a host with background errors collected, loaded with one facet (`@test/app`) whose setup is
// `setup`"; `failed` is what the load left out; `failures` lists the reported activation failures.
func TestSupportLoadedCollectsErrorsAndTheFailedPlugins(t *testing.T) {
	_, errs := loaded(t, func(FacetHost, FacetSelf) error { return errors.New("setup threw") }, loadedOptions{})
	if len(errs.Failed) != 1 || errs.Failed[0].Plugin != "@test/app" || errs.Failed[0].Error == nil || errs.Failed[0].Error.Error() != `plugin "@test/app": setup threw` {
		t.Fatalf("failed = %+v, want @test/app with its setup's error", errs.Failed)
	}
	host, errs := loaded(t, func(h FacetHost, _ FacetSelf) error {
		return h.Serve(Restartable, func(*delta.JsonObject, Self) (contracts.Implementation, error) {
			return contracts.Implementation{}, errors.New("no way")
		}, ServeOptions{})
	}, loadedOptions{})
	if len(errs.Failed) != 0 {
		t.Fatalf("failed = %+v, want none", errs.Failed)
	}
	if _, err := host.Get(background, Restartable, jsonObject("id", float64(1)), GetOptions{}); err == nil {
		t.Fatal("get of a type whose factory throws: resolved")
	}
	scopeEventually(t, "the activation failure is reported", func() bool { return len(failures(errs.All())) == 1 })
	if got := failures(errs.All()); got[0] != "app.restartable(1): broken" {
		t.Fatalf("failures = %v, want the object and the kind of its cause", got)
	}
	exCloseHost(t, host)
}

// support.ts `faultyStore`: "fail(statement)" makes the next transaction's statement throw once (a failed `COMMIT` is an
// unknown outcome, `storage_failed`), `holdNextCommit(store)` holds the next commit inside its backend transaction until
// it is released; `memoryStore` is a working store on `:memory:`.
func TestSupportFaultyStoreFailsAStatementOnceAndHoldsTheNextCommit(t *testing.T) {
	plain := memoryStore(t)
	if _, err := plain.Commit([]store.StagedWrite{noWrite{}}, openFence(), background); err != nil {
		t.Fatalf("commit on memoryStore: %v", err)
	}
	st, fail, _ := faultyStore(t)
	fail("COMMIT")
	_, err := st.Commit([]store.StagedWrite{noWrite{}}, openFence(), background)
	if chordErr, ok := data.IsChordError(err); !ok || chordErr.Reason != data.ReasonStorageFailed {
		t.Fatalf("commit with COMMIT failing = %v, want storage_failed", err)
	}
	held, _, holdNextCommit := faultyStore(t)
	gate := holdNextCommit()
	done := make(chan error, 1)
	go func() {
		_, err := held.Commit([]store.StagedWrite{noWrite{}}, openFence(), context.Background())
		done <- err
	}()
	select {
	case <-gate.Entered():
	case <-time.After(10 * time.Second):
		t.Fatal("the held commit never reached its backend transaction")
	}
	select {
	case err := <-done:
		t.Fatalf("the held commit finished before release: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	gate.Release()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("the released commit: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the released commit never finished")
	}
}
