package codingagent

import (
	"os"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// Pi handleEvent replaces the operation with RetryStatusIndicator, restores a
// fresh source-specific indicator on attempt start, and clears only retry on finish.
func TestSummarizationRetryReplacesStatusAndRestoresOperation(t *testing.T) {
	for _, embedded := range []bool{false, true} {
		for _, tc := range []struct{ source, reason, kind, label string }{
			{"compaction", "manual", "compaction", "Compacting context... (escape to cancel)"},
			{"compaction", "threshold", "compaction", "Auto-compacting... (escape to cancel)"},
			{"compaction", "overflow", "compaction", "Context overflow detected, Auto-compacting... (escape to cancel)"},
			{"branchSummary", "", "branchSummary", "Summarizing branch... (escape to cancel)"},
		} {
			t.Run(tc.source+"/"+tc.reason+"/"+map[bool]string{true: "border", false: "standalone"}[embedded], func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					m := statusBorderMode(t, embedded)
					t.Cleanup(func() { m.clearStatusIndicator("") })
					m.handleAgentEvent(agent.SummarizationRetryAttemptStartEvent{Source: tc.source, Reason: tc.reason})
					original := m.activeStatusIndicator
					m.handleAgentEvent(agent.SummarizationRetryScheduledEvent{Attempt: 2, MaxAttempts: 3, DelayMs: 2500, ErrorMessage: "overloaded"})
					assertLifecycleStatus(t, m, "retry", "Retrying (2/3) in 3s... (escape to cancel)")
					if m.activeStatusIndicator == original {
						t.Fatal("retry relabelled the original operation instead of replacing it")
					}
					time.Sleep(time.Second)
					synctest.Wait()
					select {
					case apply := <-m.uiTaskCh:
						apply()
					default:
						t.Fatal("summarization retry did not queue its countdown")
					}
					assertLifecycleStatus(t, m, "retry", "Retrying (2/3) in 2s... (escape to cancel)")
					// Leave a countdown frame queued while the operation replaces it.
					time.Sleep(time.Second)
					synctest.Wait()
					m.handleAgentEvent(agent.SummarizationRetryAttemptStartEvent{Source: tc.source, Reason: tc.reason})
					assertLifecycleStatus(t, m, tc.kind, tc.label)
					if m.activeStatusIndicator == original {
						t.Fatal("attempt start reused the disposed operation")
					}
					(<-m.uiTaskCh)()
					m.handleAgentEvent(agent.SummarizationRetryFinishedEvent{})
					assertLifecycleStatus(t, m, tc.kind, tc.label)
					m.handleAgentEvent(agent.SummarizationRetryScheduledEvent{Attempt: 3, MaxAttempts: 3, DelayMs: 30000})
					m.handleAgentEvent(agent.SummarizationRetryFinishedEvent{})
					if m.activeStatusIndicator != nil || m.retryCountdownStop != nil {
						t.Fatal("retry finish retained its indicator or timer")
					}
				})
			})
		}
	}
}

func assertLifecycleStatus(t *testing.T, m *InteractiveMode, kind, message string) {
	t.Helper()
	indicator := m.activeStatusIndicator
	if indicator == nil || indicator.Kind != kind || indicator.Message != message {
		t.Fatalf("status = %#v, want %s: %s", indicator, kind, message)
	}
	var lines []string
	if m.editor.EmbedWorkingStatus {
		lines = m.editor.Render(120)
		if !m.statusContainer.IsEmpty() {
			t.Fatal("embedded status also occupies standalone rows")
		}
	} else {
		lines = m.statusContainer.Render(120)
	}
	if rendered := widthx.StripAnsi(strings.Join(lines, "\n")); !strings.Contains(rendered, message) {
		t.Fatalf("rendered status = %q, want %q", rendered, message)
	}
}

func TestResumeClearsStatusBeforeLoading(t *testing.T) {
	m := statusBorderMode(t, true)
	m.startWorkingLoader()
	if err := m.buildSlashContext(t.Context()).LoadSessionPath(t.TempDir() + "/missing.jsonl"); err == nil {
		t.Fatal("expected missing session failure")
	}
	if m.activeStatusIndicator != nil {
		t.Fatal("resume retained a status from the outgoing session")
	}
}

func TestNewSessionDisposesQueuedCountdown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m := statusBorderMode(t, true)
		m.opts.SessionDir = t.TempDir()
		m.handleAgentEvent(agent.AutoRetryStartEvent{Attempt: 1, MaxAttempts: 3, DelayMs: 30000})
		time.Sleep(time.Second)
		synctest.Wait()
		if err := m.buildSlashContext(t.Context()).NewSession(); err != nil {
			t.Fatal(err)
		}
		(<-m.uiTaskCh)()
		if m.activeStatusIndicator != nil || m.retryCountdownStop != nil {
			t.Fatal("new session retained or repainted a disposed countdown")
		}
	})
}

func TestStatusUsesConfiguredInterruptHint(t *testing.T) {
	for _, event := range []agent.AgentEvent{
		agent.CompactionStartEvent{Reason: "manual"},
		agent.SummarizationRetryAttemptStartEvent{Source: "branchSummary"},
		agent.AutoRetryStartEvent{Attempt: 1, MaxAttempts: 3, DelayMs: 30000},
	} {
		m := statusBorderMode(t, true)
		t.Cleanup(func() { m.clearStatusIndicator("") })
		m.keybindings = DefaultKeybindingsManager()
		m.keybindings.SetUserBindings(map[string][]KeyID{"app.interrupt": {"ctrl+q"}})
		m.handleAgentEvent(event)
		if m.activeStatusIndicator == nil || !strings.HasSuffix(m.activeStatusIndicator.Message, "(ctrl+q to cancel)") {
			t.Fatalf("%T status hint = %#v", event, m.activeStatusIndicator)
		}
	}
}

// Pi clears its status before handleClearCommand calls runtimeHost.newSession,
// even if session creation fails. The disposed countdown cannot repaint later.
func TestNewSessionClearsStatusBeforeCreation(t *testing.T) {
	for _, kind := range []string{"working", "compaction", "retry", "branchSummary"} {
		t.Run(kind, func(t *testing.T) {
			m := statusBorderMode(t, true)
			t.Cleanup(func() { m.clearStatusIndicator("") })
			m.startWorkingLoader()
			m.activeStatusIndicator.Kind = kind
			// A directory cannot be created below a regular file.
			m.opts.SessionDir = t.TempDir() + "/blocked"
			if err := os.WriteFile(m.opts.SessionDir, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			err := m.buildSlashContext(t.Context()).NewSession()
			if err == nil {
				t.Fatal("expected session creation to fail")
			}
			if m.activeStatusIndicator != nil {
				t.Fatalf("failed new session retained %s status", kind)
			}
		})
	}
}
