package coding

import (
	"sync"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/ai"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// cleanupRecorder records provider Session-resource cleanup calls by ID.
type cleanupRecorder struct {
	mu    sync.Mutex
	calls []string
}

func recordSessionCleanup(t *testing.T, onCleanup func(id string)) *cleanupRecorder {
	r := &cleanupRecorder{}
	unregister := ai.RegisterSessionResourceCleanup(func(id string) {
		r.mu.Lock()
		r.calls = append(r.calls, id)
		r.mu.Unlock()
		if onCleanup != nil {
			onCleanup(id)
		}
	})
	t.Cleanup(unregister)
	return r
}

func (r *cleanupRecorder) count(id string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, c := range r.calls {
		if c == id {
			n++
		}
	}
	return n
}

// agent-session-runtime.ts teardownCurrent disposes the outgoing session, and
// agent-session.ts dispose cancels its warmer and cleans the outgoing Session
// ID. Go joins the cancelled refresh first, then cleans that retired ID, so a
// late provider completion cannot leave a resource behind.
func TestReplacementCleansRetiredSessionResourcesAfterDrain(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sess, provider, warmCtx := newPendingWarmSession(t)
		oldID := sess.ID()
		rec := recordSessionCleanup(t, func(id string) {
			if id == oldID {
				provider.resource.Store(false)
			}
		})
		sess.ReplaceInner(icodingagent.NewSession("replacement", sess.CWD()))
		if warmCtx.Err() == nil {
			t.Error("replacement did not cancel the pending refresh")
		}
		synctest.Wait()
		if n := rec.count(oldID); n != 0 {
			t.Errorf("retired ID cleaned %d times before its refresh drained", n)
		}
		close(provider.release)
		synctest.Wait()
		if provider.resource.Load() || rec.count(oldID) != 1 {
			t.Errorf("retired refresh drained without cleaning its Session resources (cleanups=%d)", rec.count(oldID))
		}
		if n := rec.count("replacement"); n != 0 {
			t.Errorf("retirement cleaned the successor's resources %d times", n)
		}
		_ = sess.Close()
		if provider.resource.Load() {
			t.Error("retired provider resource remains live after Close")
		}
		if n := rec.count("replacement"); n != 1 {
			t.Errorf("Close cleaned the current ID %d times, want 1", n)
		}
	})
}

// A replacement that keeps the same Session ID (reopening the same session)
// shares provider resources with the successor. The retired refresh must not
// clean them when it drains; Close owns that ID's cleanup.
func TestSameIDReplacementKeepsSuccessorSessionResources(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sess, provider, warmCtx := newPendingWarmSession(t)
		id := sess.ID()
		rec := recordSessionCleanup(t, func(cleaned string) {
			if cleaned == id {
				provider.resource.Store(false)
			}
		})
		sess.ReplaceInner(icodingagent.NewSession(id, sess.CWD()))
		if warmCtx.Err() == nil {
			t.Error("replacement did not cancel the pending refresh")
		}
		close(provider.release)
		synctest.Wait()
		if n := rec.count(id); n != 0 {
			t.Errorf("retired refresh cleaned the live successor's Session resources %d times", n)
		}
		_ = sess.Close()
		if n := rec.count(id); n != 1 || provider.resource.Load() {
			t.Errorf("Close cleaned the shared ID %d times (resource live=%v), want once and released", n, provider.resource.Load())
		}
	})
}
