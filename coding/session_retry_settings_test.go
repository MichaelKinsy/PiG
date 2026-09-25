package coding

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// failOnceCompleter fails its first summarization with a retryable error.
type failOnceCompleter struct{ calls atomic.Int32 }

func (c *failOnceCompleter) CompleteSimple(context.Context, *ai.Model, string, []agent.AgentMessage, ai.StreamOptions) (string, *ai.Usage, error) {
	if c.calls.Add(1) == 1 {
		return "", nil, errors.New("overloaded_error")
	}
	return "summary", nil, nil
}

// Upstream retryDelayMs caps every summarization retry delay at
// settings.retry.maxAgentDelayMs, 60000 by default.
func TestCompactionRetryDelayIsCappedByMaxAgentDelay(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)
	agentDir := filepath.Join(home, "agent")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	settings := `{"compaction":{"keepRecentTokens":1},"retry":{"maxRetries":1,"baseDelayMs":600000}}`
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(settings), 0o644); err != nil {
		t.Fatal(err)
	}
	svcs, err := NewServices(ServicesOptions{CWD: t.TempDir(), AgentDir: agentDir})
	if err != nil {
		t.Fatal(err)
	}
	sess := buildSessionWithMessages(t, svcs, 3)
	defer func() { _ = sess.Close() }()
	sess.completer = &failOnceCompleter{}
	delays := make(chan int, 1)
	unsubscribe := sess.Subscribe(func(event agent.AgentEvent) {
		if scheduled, ok := event.(agent.SummarizationRetryScheduledEvent); ok {
			delays <- scheduled.DelayMs
		}
	})
	defer unsubscribe()
	go func() {
		for range sess.Events() { //nolint:revive // drain
		}
	}()
	compacted := make(chan error, 1)
	go func() { compacted <- sess.Compact(context.Background(), "") }()
	select {
	case delay := <-delays:
		if delay != ai.DefaultMaxAgentRetryDelayMs {
			t.Fatalf("scheduled retry delay = %d, want the %d cap", delay, ai.DefaultMaxAgentRetryDelayMs)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no summarization retry was scheduled")
	}
	sess.AbortCompaction()
	<-compacted
}

// Automatic and manual compaction read the current model's
// compaction.modelOverrides entry (upstream getCompactionSettings(model)).
func TestSessionCompactionSettingsUseModelOverride(t *testing.T) {
	h := newRecoveryHarness(t, harnessOptions{settings: `{"compaction":{"reserveTokens":9000,"modelOverrides":{"faux/faux-1":{"reserveTokens":1234}}}}`})
	if got := h.session.compactionSettings().ReserveTokens; got != 1234 {
		t.Fatalf("reserveTokens = %d, want the faux/faux-1 override 1234", got)
	}
}
