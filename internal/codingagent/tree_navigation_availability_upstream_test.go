package codingagent

import (
	"context"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

type availabilityNavigationHandle struct {
	recordingCompactHandle
	navigate func(context.Context, string, bool, string) (NavigateTreeResult, error)
}

func (h *availabilityNavigationHandle) NavigateTreeHandle(ctx context.Context, id string, summarize bool, instructions string) (NavigateTreeResult, error) {
	return h.navigate(ctx, id, summarize, instructions)
}

func TestTreeNavigationAvailabilityUpstream(t *testing.T) {
	const busy = "Wait for the current compaction or tree navigation to finish before navigating the session tree."
	for _, tc := range []struct {
		name, choice                                         string
		initialBusy, busyInDialog, streaming, busyAfterAbort bool
		wantCalls, wantAborts                                int
	}{
		// .upstream/v0.87.1/packages/coding-agent/test/interactive-mode-tree-navigation.test.ts:73 (both table rows).
		{"preserves operation UI when choosing Summarize while busy", "Summarize", false, true, false, false, 0, 0},
		{"preserves operation UI when choosing No summary while busy", "No summary", false, true, false, false, 0, 0},
		// .upstream/v0.87.1/packages/coding-agent/test/interactive-mode-tree-navigation.test.ts:93
		{"allows navigation when compaction finishes while the dialog is open", "No summary", true, false, false, false, 1, 0},
		// .upstream/v0.87.1/packages/coding-agent/test/interactive-mode-tree-navigation.test.ts:110
		{"still aborts an active response before navigating", "No summary", false, false, true, false, 1, 1},
		// .upstream/v0.87.1/packages/coding-agent/test/interactive-mode-tree-navigation.test.ts:126
		{"rechecks availability after the response abort settles", "Summarize", false, false, true, true, 0, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := statusBorderMode(t, true)
			m.runCtx = t.Context()
			session := NewSession("availability", t.TempDir())
			target, err := session.AppendMessage(userMsg("first"))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := session.AppendMessage(assistantMsg("", ai.TextContent{Text: "reply"})); err != nil {
				t.Fatal(err)
			}
			originalLeaf := *session.GetLeafID()
			calls, aborts := 0, 0
			h := &availabilityNavigationHandle{recordingCompactHandle: recordingCompactHandle{inner: session, agent: m.agent}}
			h.navigate = func(_ context.Context, id string, summarize bool, instructions string) (NavigateTreeResult, error) {
				calls++
				if id != target || summarize || instructions != "" {
					t.Errorf("navigation args=%q/%t/%q", id, summarize, instructions)
				}
				if m.runStreaming() {
					t.Error("navigation started before abort settled")
				}
				if tc.streaming && m.editor.Text() != "queued" {
					t.Errorf("queued text was not restored: %q", m.editor.Text())
				}
				return NavigateTreeResult{}, nil
			}
			m.opts.SessionHandle = h
			m.isCompacting = tc.initialBusy
			if tc.busyInDialog || tc.busyAfterAbort {
				m.showCompactionStatusIndicator("manual")
			}
			originalIndicator := m.activeStatusIndicator
			t.Cleanup(func() {
				m.clearStatusIndicator("")
				if m.abortFn != nil {
					m.abortFn()
				}
			})
			if tc.streaming {
				m.turnActive.Store(true)
				m.turnSettled = make(chan struct{})
				m.agent.FollowUp(userMsg("queued"))
				m.abortFn = func() {
					aborts++
					if m.editor.Text() != "queued" {
						t.Error("queue restore did not precede abort")
					}
					m.turnActive.Store(false)
					if tc.busyAfterAbort {
						m.isCompacting = true
					}
					close(m.turnSettled)
				}
			}
			sc := m.buildSlashContext(t.Context())
			sc.ShowExtensionSelector = func(string, []string, string) (string, bool) {
				m.isCompacting = tc.busyInDialog
				return tc.choice, true
			}
			err = treeNavigateWithSummarize(sc, target)
			if tc.wantCalls == 0 {
				if err == nil || err.Error() != busy {
					t.Fatalf("busy error=%v", err)
				}
				if m.activeStatusIndicator != originalIndicator || m.branchSummaryCancel != nil {
					t.Fatal("rejection replaced or cleared the active operation UI")
				}
				if got := session.GetLeafID(); got == nil || *got != originalLeaf {
					t.Fatalf("rejection changed leaf to %v", got)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if calls != tc.wantCalls || aborts != tc.wantAborts {
				t.Fatalf("navigate/abort calls=%d/%d, want %d/%d", calls, aborts, tc.wantCalls, tc.wantAborts)
			}
			if strings.Contains(renderedChat(m), "Summarizing branch") {
				t.Fatal("rejected or unsummarized navigation added a summary indicator")
			}
		})
	}
}
