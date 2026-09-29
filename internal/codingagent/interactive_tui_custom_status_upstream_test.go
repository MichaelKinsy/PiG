package codingagent

import (
	"fmt"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// .upstream/v0.87.1/packages/coding-agent/test/interactive-tui.test.ts:445
func TestInteractiveTuiCustomEditorStandaloneStatusUpstream(t *testing.T) {
	for _, tc := range []struct {
		mode     string
		children int
	}{{"regular", 1}, {"fullscreen", 0}} {
		t.Run(tc.mode, func(t *testing.T) {
			mode, _ := newCustomEditorDispatchMode(t)
			mode.opts.TuiMode = tc.mode
			mode.opts.Settings.ClearOnShrink = new(true)
			mode.tuiInst.SetClearOnShrink(true)
			mode.editor.EmbedWorkingStatus = true
			indicator := &tui.StatusIndicator{Kind: "working", Loader: tui.NewLoader("DEFAULT_STALE_STATUS")}
			mode.editor.SetWorkingStatusIndicator(indicator)
			custom := &fakeRemoteEditor{}
			mode.setRemoteEditor(custom)
			before := len(custom.calls)
			mode.statusContainer = tui.NewContainer()
			mode.activeStatusIndicator = indicator
			mode.activeWorkingIndicatorEmbedded = false
			mode.clearStatusIndicator("")
			if got := mode.statusContainer.ChildCount(); got != tc.children {
				t.Fatalf("status rows=%d want %d", got, tc.children)
			}
			if len(custom.calls) != before {
				t.Fatalf("unopted custom editor received a status operation: %v", custom.calls[before:])
			}
			mode.setRemoteEditor(nil)
			if strings.Contains(strings.Join(mode.editor.Render(120), "\n"), "DEFAULT_STALE_STATUS") {
				t.Fatal("default editor status was not cleared")
			}
		})
	}
}

// Pi interactive-mode.ts:3528-3529,3553,3615-3651 sends every operation through showStatusIndicator, which checks the active editor at :2208-2225.
func TestCustomEditorStatusEventPlacement(t *testing.T) {
	for _, mode := range []string{"regular", "fullscreen"} {
		for _, tc := range []struct {
			kind  string
			event agent.AgentEvent
			label string
		}{
			{"working", agent.AgentStartEvent{}, "Working"},
			{"compaction", agent.CompactionStartEvent{Reason: "manual"}, "Compacting context... (escape to cancel)"},
			{"compaction", agent.CompactionStartEvent{Reason: "threshold"}, "Auto-compacting... (escape to cancel)"},
			{"compaction", agent.CompactionStartEvent{Reason: "overflow"}, "Context overflow detected, Auto-compacting... (escape to cancel)"},
			{"branchSummary", agent.SummarizationRetryAttemptStartEvent{Source: "branchSummary"}, "Summarizing branch... (escape to cancel)"},
			{"retry", agent.AutoRetryStartEvent{Attempt: 1, MaxAttempts: 3, DelayMs: 1000}, "Retrying (1/3) in 1s... (escape to cancel)"},
		} {
			t.Run(mode+"/"+tc.label, func(t *testing.T) {
				m := statusBorderMode(t, true)
				m.opts.TuiMode = mode
				m.tuiInst.SetClearOnShrink(true)
				m.setRemoteEditor(&fakeRemoteEditor{})
				m.editor.SetRemoteFrame([]string{"CUSTOM_EDITOR"}, 120, false)
				t.Cleanup(func() { m.clearStatusIndicator("") })
				m.handleAgentEvent(tc.event)
				if m.activeStatusIndicator == nil || m.activeStatusIndicator.Kind != tc.kind || m.activeWorkingIndicatorEmbedded {
					t.Fatalf("status=%#v embedded=%v", m.activeStatusIndicator, m.activeWorkingIndicatorEmbedded)
				}
				if got := widthx.StripAnsi(strings.Join(m.statusContainer.Render(120), "\n")); !strings.Contains(got, tc.label) {
					t.Fatalf("standalone status=%q, want %q", got, tc.label)
				}
				if got := strings.Join(m.editor.Render(120), "\n"); got != "CUSTOM_EDITOR" {
					t.Fatalf("status changed custom editor frame to %q", got)
				}
				m.clearStatusIndicator("")
				wantRows := 0
				if mode == "regular" {
					wantRows = 2 // Pi IdleStatus.render reserves two standalone rows.
				}
				if got := len(m.statusContainer.Render(120)); got != wantRows {
					t.Fatalf("cleared standalone rows=%d, want %d", got, wantRows)
				}
				m.setRemoteEditor(nil)
				m.startWorkingLoader()
				if !m.activeWorkingIndicatorEmbedded || !m.statusContainer.IsEmpty() {
					t.Fatal("restored default editor lost its own status opt-in")
				}
			})
		}
	}
}

// The same opt-in predicate must govern showing status through a real installed extension editor, not the dormant default editor's flag.
func TestCustomEditorDoesNotInheritDefaultStatusOptIn(t *testing.T) {
	mode, _ := newCustomEditorDispatchMode(t)
	mode.editor.EmbedWorkingStatus = true
	mode.statusContainer = tui.NewContainer()
	mode.setRemoteEditor(&fakeRemoteEditor{})
	for _, kind := range []string{"working", "compaction", "branchSummary", "retry"} {
		t.Run(kind, func(t *testing.T) {
			indicator := &tui.StatusIndicator{Kind: kind, Loader: tui.NewLoader(fmt.Sprint("STATUS_", kind))}
			mode.showStatusIndicator(indicator)
			_, last := mode.statusContainer.LastTwoChildren()
			if mode.activeWorkingIndicatorEmbedded || mode.statusContainer.ChildCount() != 1 || last != indicator {
				t.Fatalf("custom editor inherited default opt-in: embedded=%v children=%d", mode.activeWorkingIndicatorEmbedded, mode.statusContainer.ChildCount())
			}
			mode.clearStatusIndicator("")
		})
	}
}
