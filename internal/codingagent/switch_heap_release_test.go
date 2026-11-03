// SPDX-License-Identifier: MIT

package codingagent

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
)

// pig additive (D172): a Session replacement and a tree navigation hand the replaced Session's entries and the load's buffers back to the
// OS once the new transcript is painted, as the start does. The test asserts the call path, not a footprint: each path that repaints the
// transcript for a new Session or a new leaf requests the release exactly once.
//
// /resume, /fork and a session import end in renderCurrentSessionState (interactive_rebind.go, after the runtime replaced the Session), and
// /tree ends in navigateTree's repaint of the navigated branch.
func TestSessionSwitchesRequestAHeapRelease(t *testing.T) {
	t.Run("a Session replacement (/resume, /fork, import)", func(t *testing.T) {
		m := resumeThinkingMode(t, false, userMsg("kept prompt"))
		m.renderCurrentSessionState()
		m.backgroundTasks.Wait()
		if got := m.switchHeapReleases.Load(); got != 1 {
			t.Fatalf("renderCurrentSessionState requested %d heap releases, want 1", got)
		}
		if m.switchHeapRelease.Load() {
			t.Fatal("the release did not finish: its pending flag is still set")
		}
	})
	t.Run("a tree navigation (/tree)", func(t *testing.T) {
		m := statusBorderMode(t, true)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		m.runCtx = ctx
		h := &lifecycleNavigationHandle{events: make(chan agent.AgentEvent, 1)}
		m.opts.SessionHandle, m.eventCh = h, h.events
		h.navigate = func(context.Context) (NavigateTreeResult, error) { return NavigateTreeResult{}, nil }
		if _, err := m.navigateTree(ctx, "target", false, ""); err != nil {
			t.Fatal(err)
		}
		m.backgroundTasks.Wait()
		if got := m.switchHeapReleases.Load(); got != 1 {
			t.Fatalf("a completed tree navigation requested %d heap releases, want 1", got)
		}
	})
	t.Run("a cancelled tree navigation changes nothing", func(t *testing.T) {
		m := statusBorderMode(t, true)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		m.runCtx = ctx
		h := &lifecycleNavigationHandle{events: make(chan agent.AgentEvent, 1)}
		m.opts.SessionHandle, m.eventCh = h, h.events
		h.navigate = func(context.Context) (NavigateTreeResult, error) { return NavigateTreeResult{Cancelled: true}, nil }
		if _, err := m.navigateTree(ctx, "target", false, ""); err != nil {
			t.Fatal(err)
		}
		m.backgroundTasks.Wait()
		if got := m.switchHeapReleases.Load(); got != 0 {
			t.Fatalf("a cancelled tree navigation requested %d heap releases, want 0", got)
		}
	})
}
