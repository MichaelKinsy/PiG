package sessions_test

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/durable"
)

// lockInDurable is the lock the session's Durable state holds now.
func lockInDurable(t *testing.T, opened durable.DocumentReader) *p2runtime.SessionLock {
	t.Helper()
	lock, err := p2runtime.LockOf(p2test.Background, opened)
	if err != nil {
		t.Fatal(err)
	}
	return lock
}

// commitCounter counts the commits stored.
type commitCounter struct{ n atomic.Int64 }

func (c *commitCounter) Advance(context.Context, durable.CommitPublication) { c.n.Add(1) }
func (c *commitCounter) End(durable.WatchEnd)                               {}

func TestMirrorLockCommitsTheLockUnlessItIsThatAlready(t *testing.T) {
	opened := p2test.OpenHarness(t)
	if got := lockInDurable(t, opened); got != nil {
		t.Fatalf("a fresh session is locked: %+v", got)
	}
	if err := sessions.MirrorLock(p2test.Background, opened, lockOf("github")); err != nil {
		t.Fatal(err)
	}
	got := lockInDurable(t, opened)
	if got == nil || len(got.MCP) != 1 || got.MCP[0] != "github" || !got.Instructions || got.ProjectMCP || len(got.Capabilities) != 1 || got.Capabilities[0] != p2runtime.CapabilityPorts {
		t.Fatalf("lock = %+v", got)
	}
	// The same lock again commits nothing: the head of the session does not move.
	commits := &commitCounter{}
	observation := opened.ObserveCommits(commits)
	defer observation.Detach()
	if err := sessions.MirrorLock(p2test.Background, opened, lockOf("github")); err != nil {
		t.Fatal(err)
	}
	if commits.n.Load() != 0 {
		t.Fatal("the same lock was committed again")
	}
	if err := sessions.MirrorLock(p2test.Background, opened, nil); err != nil {
		t.Fatal(err)
	}
	if got := lockInDurable(t, opened); got != nil {
		t.Fatalf("lock after removal = %+v", got)
	}
}

func TestWorkersMirrorTheServersLockIntoTheirDurableState(t *testing.T) {
	p2test.CheckLeaks(t, 0)
	server := p2test.NewTestHost(t, p2test.TestHostOptions{Facets: []host.Facet{sessions.Server}, Store: openStore(t)})
	opened := p2test.OpenHarness(t)
	server.WorkerHost("s1", 1, p2test.WorkerFacet(p2test.HarnessParts(opened), nil, nil), sessions.Worker)

	ownerView, err := p2test.HoldIn(server.Client(owner), contract.SessionLock, delta.NewJsonObject(0), "owner")
	if err != nil {
		t.Fatal(err)
	}
	defer ownerView.Release()
	set := func(session string, lock any) {
		t.Helper()
		if _, err := ownerView.Call("set", delta.JsonObjectOf("session", session, "lock", lock), p2test.Background); err != nil {
			t.Fatal(err)
		}
	}
	// Another session's lock is not this worker's.
	set("s2", lockOf("other"))
	set("s1", lockOf("github"))
	if err := p2test.Poll(func() (bool, error) {
		lock, err := p2runtime.LockOf(p2test.Background, opened)
		return err == nil && lock != nil && len(lock.MCP) == 1 && lock.MCP[0] == "github", err
	}, 0, "the worker's lock in Durable"); err != nil {
		t.Fatal(err)
	}
	set("s1", nil)
	if err := p2test.Poll(func() (bool, error) {
		lock, err := p2runtime.LockOf(p2test.Background, opened)
		return err == nil && lock == nil, err
	}, 0, "the lock removed from Durable"); err != nil {
		t.Fatal(err)
	}
}
