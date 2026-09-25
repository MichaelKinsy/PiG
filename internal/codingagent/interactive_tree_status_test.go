package codingagent

import (
	"context"
	"errors"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

type lifecycleNavigationHandle struct {
	recordingCompactHandle
	navigate func(context.Context) (NavigateTreeResult, error)
	events   chan agent.AgentEvent
	aborts   int
}

func (h *lifecycleNavigationHandle) NavigateTreeHandle(ctx context.Context, _ string, _ bool, _ string) (NavigateTreeResult, error) {
	return h.navigate(ctx)
}
func (h *lifecycleNavigationHandle) AbortBranchSummary() { h.aborts++ }

// Like the real Session, deliver a FIFO acknowledgement after navigation so
// buffered retry events are handled before the caller clears its final status.
func (h *lifecycleNavigationHandle) FlushEvents(ctx context.Context) error {
	barrier := &lifecycleBarrier{done: make(chan struct{})}
	select {
	case h.events <- barrier:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case <-barrier.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type lifecycleBarrier struct {
	agent.AgentEvent
	done  chan struct{}
	apply func()
}

func (b *lifecycleBarrier) AcknowledgeEvent() {
	if b.apply != nil {
		b.apply()
	}
	close(b.done)
}

// Pi keeps the editor live while awaiting navigateTree, routes Esc to
// abortBranchSummary even during retry, and reopens the same tree selection.
func TestTreeSummaryStatusAndEscapeKeepOwnerLoopLive(t *testing.T) {
	for _, retry := range []bool{false, true} {
		for _, embedded := range []bool{false, true} {
			t.Run(map[bool]string{false: "summary", true: "retry"}[retry]+"/"+map[bool]string{false: "standalone", true: "border"}[embedded], func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					m := statusBorderMode(t, embedded)
					m.keybindings = DefaultKeybindingsManager()
					ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
					defer cancel()
					m.runCtx = ctx
					m.abortCtx, m.abortFn = context.WithCancel(ctx)
					defer m.abortFn()
					t.Cleanup(func() { m.clearStatusIndicator("") })
					h := &lifecycleNavigationHandle{events: make(chan agent.AgentEvent, 8)}
					m.opts.SessionHandle = h
					m.eventCh = h.events
					observed := false
					h.navigate = func(navCtx context.Context) (NavigateTreeResult, error) {
						if retry {
							h.events <- agent.SummarizationRetryScheduledEvent{Attempt: 1, MaxAttempts: 3, DelayMs: 30000, ErrorMessage: "overloaded"}
						}
						h.events <- &lifecycleBarrier{done: make(chan struct{}), apply: func() {
							observed = true
							if retry {
								assertLifecycleStatus(t, m, "retry", "Retrying (1/3) in 30s... (escape to cancel)")
							} else {
								assertLifecycleStatus(t, m, "branchSummary", "Summarizing branch... (escape to cancel)")
							}
							m.editor.SetText("discard draft")
							if err := m.dispatchKey(ctx, "\x03"); err != nil {
								t.Error(err)
							}
							if m.editor.Text() != "" || h.aborts != 0 {
								t.Error("Ctrl+C did not just clear the editor")
							}
							m.editor.SetText("keep draft")
							if err := m.dispatchKey(ctx, "\x1b"); err != nil {
								t.Error(err)
							}
						}}
						select {
						case <-navCtx.Done():
							if retry {
								h.events <- agent.SummarizationRetryFinishedEvent{}
							}
							return NavigateTreeResult{Aborted: true, Cancelled: true}, nil
						case <-ctx.Done():
							return NavigateTreeResult{}, errors.New("owner loop did not dispatch Esc")
						}
					}
					sc := m.buildSlashContext(ctx)
					sc.ShowExtensionSelector = func(string, []string, string) (string, bool) { return "Summarize", true }
					reopened := ""
					sc.PickTreeEntry = func(id string) (string, bool) { reopened = id; return "", false }
					if err := treeNavigateWithSummarize(sc, "target"); err != nil {
						t.Fatal(err)
					}
					if !observed {
						t.Fatal("navigation blocked the owner loop")
					}
					if h.aborts != 1 || h.abortCompactionCount != 0 || h.abortRetryCount != 0 {
						t.Fatalf("wrong cancel target: branch=%d compaction=%d retry=%d", h.aborts, h.abortCompactionCount, h.abortRetryCount)
					}
					if reopened != "target" || m.editor.Text() != "keep draft" {
						t.Fatalf("reopened=%q editor=%q", reopened, m.editor.Text())
					}
					if m.activeStatusIndicator != nil || m.retryCountdownStop != nil || m.isCompacting {
						t.Fatal("navigation retained busy status after cancellation")
					}
					chat := widthx.StripAnsi(strings.Join(m.chatContainer.Render(120), "\n"))
					if strings.Contains(chat, "Summarizing branch...") || !strings.Contains(chat, "Branch summarization cancelled") {
						t.Fatalf("chat=%q", chat)
					}
				})
			})
		}
	}
}

func TestTreeSummaryParentCancellationJoinsWorker(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m := statusBorderMode(t, true)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		m.runCtx = ctx
		h := &lifecycleNavigationHandle{events: make(chan agent.AgentEvent, 1)}
		m.opts.SessionHandle = h
		m.eventCh = h.events
		returned := false
		h.navigate = func(navCtx context.Context) (NavigateTreeResult, error) {
			<-navCtx.Done()
			returned = true
			return NavigateTreeResult{}, navCtx.Err()
		}
		m.uiTaskCh <- cancel
		_, err := m.buildSlashContext(t.Context()).NavigateTreeFull(t.Context(), "target", true, "")
		if !errors.Is(err, context.Canceled) || !returned {
			t.Fatalf("shutdown did not join navigation: returned=%v error=%v", returned, err)
		}
		if m.activeStatusIndicator != nil || m.branchSummaryCancel != nil || m.isCompacting {
			t.Fatal("shutdown retained navigation state")
		}
	})
}

// A navigation failure, extension cancellation, or success all dispose the
// branch status. Finishing must not overtake buffered retry events.
func TestTreeSummaryCompletionDrainsEventsAndPreservesDraft(t *testing.T) {
	for _, outcome := range []string{"success", "cancelled", "error"} {
		t.Run(outcome, func(t *testing.T) {
			m := statusBorderMode(t, true)
			h := &lifecycleNavigationHandle{events: make(chan agent.AgentEvent, 8)}
			m.opts.SessionHandle = h
			m.eventCh = h.events
			h.navigate = func(context.Context) (NavigateTreeResult, error) {
				h.events <- agent.SummarizationRetryScheduledEvent{Attempt: 1, MaxAttempts: 3, DelayMs: 30000}
				h.events <- agent.SummarizationRetryAttemptStartEvent{Source: "branchSummary"}
				h.events <- agent.SummarizationRetryFinishedEvent{}
				switch outcome {
				case "cancelled":
					return NavigateTreeResult{Cancelled: true}, nil
				case "error":
					return NavigateTreeResult{}, errors.New("summary failed")
				default:
					return NavigateTreeResult{EditorText: "target text"}, nil
				}
			}
			m.editor.SetText("draft typed during summary")
			sc := m.buildSlashContext(t.Context())
			sc.ShowExtensionSelector = func(string, []string, string) (string, bool) { return "Summarize", true }
			err := treeNavigateWithSummarize(sc, "target")
			if (err != nil) != (outcome == "error") {
				t.Fatalf("error=%v", err)
			}
			if m.activeStatusIndicator != nil || m.retryCountdownStop != nil || len(h.events) != 0 {
				t.Fatal("completion overtook status events or retained the indicator")
			}
			if m.editor.Text() != "draft typed during summary" {
				t.Fatalf("navigation overwrote editor with %q", m.editor.Text())
			}
		})
	}
}
