package sessions_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
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

// captureStderr replaces os.Stderr for the test and returns what was written to it so far.
func captureStderr(t *testing.T) func() string {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var written strings.Builder
	done := make(chan struct{})
	go func() {
		defer close(done)
		buffer := make([]byte, 4096)
		for {
			n, err := reader.Read(buffer)
			mu.Lock()
			written.Write(buffer[:n])
			mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	original := os.Stderr
	os.Stderr = writer
	t.Cleanup(func() {
		os.Stderr = original
		_ = writer.Close()
		<-done
		_ = reader.Close()
	})
	return func() string {
		mu.Lock()
		defer mu.Unlock()
		return written.String()
	}
}

// Pi's lock mirror ends each commit in `.catch(report)`: the failure goes to stderr and the value is dropped until the next
// change; the routine does not fail, so nothing retries it after a backoff and no host error is raised.
func TestAFailedLockMirrorIsReportedToStderrAndDropped(t *testing.T) {
	stderr := captureStderr(t)
	server := p2test.NewTestHost(t, p2test.TestHostOptions{Facets: []host.Facet{sessions.Server}, Store: openStore(t)})
	opened := p2test.OpenHarness(t)
	var failing atomic.Bool
	failing.Store(true)
	parts := &p2runtime.HarnessParts{Durable: func() (harness.Harness, error) {
		if failing.Load() {
			return nil, errors.New("the Durable state is gone")
		}
		return opened, nil
	}}
	server.WorkerHost("s1", 1, p2test.WorkerFacet(parts, nil, nil), sessions.Worker)

	ownerView, err := p2test.HoldIn(server.Client(owner), contract.SessionLock, delta.NewJsonObject(0), "owner")
	if err != nil {
		t.Fatal(err)
	}
	defer ownerView.Release()
	set := func(lock any) {
		t.Helper()
		if _, err := ownerView.Call("set", delta.JsonObjectOf("session", "s1", "lock", lock), p2test.Background); err != nil {
			t.Fatal(err)
		}
	}
	set(lockOf("lost"))
	if err := p2test.Poll(func() (bool, error) {
		return strings.Contains(stderr(), "p2 sessions: mirroring the session's lock failed: the Durable state is gone"), nil
	}, 0, "the failed mirror reported to stderr"); err != nil {
		t.Fatalf("%v; stderr: %q", err, stderr())
	}
	if errs := server.Errors(); len(errs) != 0 {
		t.Fatalf("a failed mirror failed the routine: %v", errs)
	}

	// The next change is mirrored.
	failing.Store(false)
	set(lockOf("github"))
	if err := p2test.Poll(func() (bool, error) {
		lock, err := p2runtime.LockOf(p2test.Background, opened)
		return err == nil && lock != nil && len(lock.MCP) == 1 && lock.MCP[0] == "github", err
	}, 0, "the next lock in Durable"); err != nil {
		t.Fatal(err)
	}
	if errs := server.Errors(); len(errs) != 0 {
		t.Fatalf("host errors: %v", errs)
	}
}
