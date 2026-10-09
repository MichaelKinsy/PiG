package coding

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

func newStateTestSession(t *testing.T, model *ai.Model) *Session {
	t.Helper()
	sess, err := NewSession(newTestServices(t), SessionOptions{Model: model, SkipBuiltinTools: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := sess.Close(); err != nil {
			t.Error(err)
		}
	})
	return sess
}

// upstream: agent-session.ts get steeringMode / get followUpMode return the Agent's queue modes, each from its own queue.
func TestSessionQueueModesFollowTheAgent(t *testing.T) {
	sess := newStateTestSession(t, fakeModel())
	for _, tc := range []struct{ steering, followUp agent.QueueMode }{
		{agent.QueueModeAll, agent.QueueModeOneAtATime},
		{agent.QueueModeOneAtATime, agent.QueueModeAll},
	} {
		if err := sess.SetSteeringMode(tc.steering); err != nil {
			t.Fatal(err)
		}
		if err := sess.SetFollowUpMode(tc.followUp); err != nil {
			t.Fatal(err)
		}
		if sess.SteeringMode() != tc.steering || sess.FollowUpMode() != tc.followUp {
			t.Errorf("modes = %q/%q, want %q/%q", sess.SteeringMode(), sess.FollowUpMode(), tc.steering, tc.followUp)
		}
	}
}

// upstream: agent-session.ts supportsThinking is `!!this.model?.reasoning`; cycleThinkingLevel returns undefined without it (:2628).
// Pi: packages/coding-agent/src/core/agent-session.ts:2627 (Session.cycleThinkingLevel); packages/coding-agent/src/core/agent-session.ts:2628 (Session.supportsThinking).
func TestSessionSupportsThinkingGatesCycleThinkingLevel(t *testing.T) {
	plain := newStateTestSession(t, fakeModel())
	if plain.SupportsThinking() {
		t.Error("a model without reasoning supports thinking")
	}
	if level, err := plain.CycleThinkingLevel(); err != nil || level != "" {
		t.Errorf("CycleThinkingLevel on a non-reasoning model = %q, %v", level, err)
	}
	reasoning := fakeModel()
	reasoning.ProviderMeta.Reasoning = true
	sess := newStateTestSession(t, reasoning)
	if !sess.SupportsThinking() {
		t.Fatal("a reasoning model does not support thinking")
	}
	if level, err := sess.CycleThinkingLevel(); err != nil || level == "" {
		t.Errorf("CycleThinkingLevel on a reasoning model = %q, %v", level, err)
	}
	capped := fakeModel()
	capped.Capabilities.MaxThinking = ai.ThinkingLevelHigh
	if !newStateTestSession(t, capped).SupportsThinking() {
		t.Error("a model with a maximum thinking level does not support thinking")
	}
}

// upstream: agent-session.ts setAutoCompactionEnabled / get autoCompactionEnabled and setAutoRetryEnabled / get autoRetryEnabled
// write and read the global compaction.enabled and retry.enabled settings.
func TestSessionAutoSettingsRoundTrip(t *testing.T) {
	sess := newStateTestSession(t, fakeModel())
	if !sess.AutoCompactionEnabled() || !sess.AutoRetryEnabled() {
		t.Fatal("compaction and retry are enabled by default")
	}
	for _, enabled := range []bool{false, true} {
		if err := sess.SetAutoCompactionEnabled(enabled); err != nil {
			t.Fatal(err)
		}
		if err := sess.SetAutoRetryEnabled(enabled); err != nil {
			t.Fatal(err)
		}
		if sess.AutoCompactionEnabled() != enabled || sess.AutoRetryEnabled() != enabled {
			t.Errorf("after enabling=%v: compaction %v, retry %v", enabled, sess.AutoCompactionEnabled(), sess.AutoRetryEnabled())
		}
		if got := sess.services.SettingsManager().GetCompactionEnabled(); got != enabled {
			t.Errorf("persisted compaction = %v, want %v", got, enabled)
		}
	}
}

// upstream: agent-session.ts get isRetrying is "the retry abort controller exists" and get retryAttempt is _retryAttempt.
// Pi: packages/coding-agent/src/core/agent-session.ts:1461 (Session.retryAttempt).
func TestSessionRetryState(t *testing.T) {
	sess := newStateTestSession(t, fakeModel())
	if sess.IsRetrying() || sess.RetryAttempt() != 0 {
		t.Fatalf("idle session retrying=%v attempt=%d", sess.IsRetrying(), sess.RetryAttempt())
	}
	sess.retryAttempt.Store(2)
	sess.retryMu.Lock()
	sess.retryCancel = &sessionRetryWait{}
	sess.retryMu.Unlock()
	if !sess.IsRetrying() || sess.RetryAttempt() != 2 {
		t.Errorf("retry wait published: retrying=%v attempt=%d", sess.IsRetrying(), sess.RetryAttempt())
	}
	sess.retryMu.Lock()
	sess.retryCancel = nil
	sess.retryMu.Unlock()
	if sess.IsRetrying() {
		t.Error("IsRetrying stays true after the wait is cleared")
	}
}

// upstream: agent-session.ts get isBashRunning / get hasPendingBashMessages.
func TestSessionBashState(t *testing.T) {
	sess := newStateTestSession(t, fakeModel())
	if sess.IsBashRunning() || sess.HasPendingBashMessages() {
		t.Fatal("idle session reports bash state")
	}
	invocations, operations := controlledBashPersistenceOperations()
	join := startBashPersistenceCall(t, sess, "sleep", operations)
	invocation := <-invocations
	if !sess.IsBashRunning() {
		t.Error("IsBashRunning is false while a command runs")
	}
	invocation.finish()
	join()
	if sess.IsBashRunning() {
		t.Error("IsBashRunning stays true after the command settles")
	}
}

// upstream: agent-session.ts exportToHtml exports the Session file, naming an unwritten Session
// ("Nothing to export yet - start a conversation first") and writing the HTML to outputPath once it has entries.
// Pi: packages/coding-agent/src/core/agent-session.ts:4273 (Session.exportToHtml).
func TestSessionExportToHTML(t *testing.T) {
	sess := newStateTestSession(t, fakeModel())
	out := filepath.Join(t.TempDir(), "session.html")
	if _, err := sess.ExportToHTML(out); err == nil || !strings.Contains(err.Error(), "Nothing to export yet") {
		t.Fatalf("export before the first turn = %v", err)
	}
	if _, err := sess.Send(t.Context(), "export marker prompt"); err != nil {
		t.Fatal(err)
	}
	got, err := sess.ExportToHTML(out)
	if err != nil {
		t.Fatal(err)
	}
	if got != out {
		t.Errorf("path = %q, want %q", got, out)
	}
	html, err := os.ReadFile(out)
	if err != nil || len(html) == 0 {
		t.Fatalf("exported file: %v (%d bytes)", err, len(html))
	}
}

// upstream: runner.ts:914 ExtensionContext.thinkingLevel is the Session's current level once bindExtensionCore ran.
func TestSessionBindsTheThinkingLevelIntoExtensionContexts(t *testing.T) {
	model := fakeModel()
	model.ProviderMeta.Reasoning = true
	sess := newStateTestSession(t, model)
	runner := inproc.NewRunner(nil, t.TempDir())
	sess.bindExtensionCore(runner)
	if err := sess.SetThinkingLevel(ai.ThinkingHigh); err != nil {
		t.Fatal(err)
	}
	got, err := extension.FromContext(runner.DispatchContext(t.Context())).ThinkingLevel()
	if err != nil || got != sess.ThinkingLevel() {
		t.Fatalf("ctx.thinkingLevel = %v, %v; want %v", got, err, sess.ThinkingLevel())
	}
}

// upstream: agent-session.ts exportToJsonl writes the session header followed by the entries of the current branch.
// Pi: packages/coding-agent/src/core/agent-session.ts:4299 (Session.exportToJsonl).
func TestSessionExportToJsonl(t *testing.T) {
	sess := newStateTestSession(t, fakeModel())
	if _, err := sess.Send(t.Context(), "jsonl marker prompt"); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "branch.jsonl")
	got, err := sess.ExportToJsonl(out)
	if err != nil || got != out {
		t.Fatalf("ExportToJsonl = %q, %v; want %q", got, err, out)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) < 3 || !strings.Contains(lines[0], `"type":"session"`) || !strings.Contains(string(data), "jsonl marker prompt") {
		t.Fatalf("exported branch = %q", data)
	}
}
