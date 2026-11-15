package codingagent_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// compactStreamProvider answers its first chat turns at once with a long reply and hands the stream of the next request, the summary, to the test, which pushes its deltas and ends it. A request context that ends aborts that stream.
type compactStreamProvider struct {
	chat      int
	calls     int
	summary   chan *ai.AssistantMessageEventStream
	abortDone chan struct{}
}

func (p *compactStreamProvider) ID() string   { return "faux" }
func (p *compactStreamProvider) Close() error { return nil }

func (p *compactStreamProvider) Stream(ctx context.Context, _ ai.TranscriptContext, _ ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
	p.calls++
	stream := ai.NewAssistantMessageEventStream()
	if p.calls <= p.chat || p.calls > p.chat+1 {
		message := &ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.TextContent{Text: strings.Repeat("reply ", 15_000)}}, Provider: "faux", Model: "faux-1", StopReason: ai.StopReasonStop, Timestamp: time.Now().UnixMilli()}
		_ = stream.Push(ai.StartEvent{Partial: message})
		_ = stream.Push(ai.DoneEvent{Reason: message.StopReason, Message: message})
		return stream, nil
	}
	partial := &ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.TextContent{}}, Provider: "faux", Model: "faux-1", Timestamp: time.Now().UnixMilli()}
	_ = stream.Push(ai.StartEvent{Partial: partial})
	go func() {
		defer close(p.abortDone)
		<-ctx.Done()
		aborted := &ai.AssistantMessage{Provider: "faux", Model: "faux-1", StopReason: ai.StopReasonAborted, Timestamp: time.Now().UnixMilli()}
		_ = stream.Push(ai.ErrorEvent{Reason: ai.StopReasonAborted, Error: aborted})
	}()
	p.summary <- stream
	return stream, nil
}

// compactingHarness is a mode on a real Session with three long chat turns behind it and /compact entered; the summary request has started and its stream is returned.
func compactingHarness(t *testing.T) (*icodingagent.TestHarness, *coding.Session, *compactStreamProvider, *ai.AssistantMessageEventStream) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)
	agentDir := filepath.Join(home, "agent")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: cwd, AgentDir: agentDir})
	if err != nil {
		t.Fatal(err)
	}
	if err := configureFauxAuth(services, "faux-key"); err != nil {
		t.Fatal(err)
	}
	provider := &compactStreamProvider{chat: 3, summary: make(chan *ai.AssistantMessageEventStream, 1), abortDone: make(chan struct{})}
	model := &ai.Model{ID: "faux-1", DisplayName: "faux-1", Provider: provider, Capabilities: ai.ModelCapabilities{ContextWindow: 100_000_000}}
	session, err := coding.NewSession(services, coding.SessionOptions{Model: model, NoSession: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	h := icodingagent.NewTestHarness(t, icodingagent.InteractiveModeOptions{
		CWD: cwd, AgentDir: agentDir, Model: model, SessionHandle: session,
		SettingsManager: services.SettingsManager(), Settings: services.SettingsManager().Get(),
	}, nil)
	for _, text := range []string{"one", "two", "three"} {
		h.Do(func() { h.Enter(text) })
		h.WaitIdle(t, 10*time.Second)
	}
	h.Do(func() { h.Enter("/compact") })
	select {
	case stream := <-provider.summary:
		return h, session, provider, stream
	case <-time.After(10 * time.Second):
		t.Fatal("the summary request did not start")
		return nil, nil, nil, nil
	}
}

// waitLabel waits until the compaction line, read on the status tick at the given clock reading, contains want: the counter is fed by the goroutine that reads the stream, so the test waits on the condition, not on time.
func waitLabel(t *testing.T, h *icodingagent.TestHarness, at time.Time, want string) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		h.TickStatus(at)
		_, label := h.Status()
		if strings.Contains(label, want) {
			return label
		}
		if time.Now().After(deadline) {
			t.Fatalf("the compaction line %q never showed %q", label, want)
		}
		runtime.Gosched()
	}
}

func pushDelta(t *testing.T, stream *ai.AssistantMessageEventStream, chars int) {
	t.Helper()
	partial := &ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.TextContent{}}, Provider: "faux", Model: "faux-1"}
	if err := stream.Push(ai.TextDeltaEvent{Delta: strings.Repeat("x", chars), Partial: partial}); err != nil {
		t.Fatal(err)
	}
}

// pig divergence (D652): the compaction line shows the elapsed time and the size of the summary streamed so far. The line is read on the status tick's own clock, here stepped without sleeping.
func TestCompactionLineShowsElapsedTimeAndStreamedTokens(t *testing.T) {
	h, session, _, stream := compactingHarness(t)
	base := h.StatusClockBase()
	label := func(at time.Duration) string {
		t.Helper()
		h.TickStatus(base.Add(at))
		kind, label := h.Status()
		if kind != "compaction" {
			t.Fatalf("indicator kind %q, want compaction", kind)
		}
		return label
	}
	if got, want := label(500*time.Millisecond), "Compacting context... (escape to cancel)"; got != want {
		t.Fatalf("before any delta and within the first second: %q, want %q", got, want)
	}
	if got, want := label(12*time.Second), "Compacting context... 12s (escape to cancel)"; got != want {
		t.Fatalf("before the first delta: %q, want %q", got, want)
	}
	for k, want := range []string{
		"Compacting context... 13s · ~1.0k tokens (escape to cancel)",
		"Compacting context... 1m 12s · ~2.0k tokens (escape to cancel)",
		"Compacting context... 1m 13s · ~3.0k tokens (escape to cancel)",
	} {
		pushDelta(t, stream, 4000)
		at := []time.Duration{13 * time.Second, 72 * time.Second, 73 * time.Second}[k]
		if got := waitLabel(t, h, base.Add(at), want); got != want {
			t.Fatalf("after %d deltas: %q, want %q", k+1, got, want)
		}
	}
	entriesBefore := len(session.Inner().GetEntries())
	done := &ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.TextContent{Text: "the summary"}}, Provider: "faux", Model: "faux-1", StopReason: ai.StopReasonStop, Timestamp: time.Now().UnixMilli()}
	if err := stream.Push(ai.DoneEvent{Reason: ai.StopReasonStop, Message: done}); err != nil {
		t.Fatal(err)
	}
	waitStatusGone(t, h)
	if kind, label := h.Status(); kind != "" || label != "" {
		t.Fatalf("after the compaction the indicator is %q %q, want none", kind, label)
	}
	entries := session.Inner().GetEntries()
	last := entries[len(entries)-1]
	if len(entries) != entriesBefore+1 || last.Base().Type != "compaction" {
		t.Fatalf("entries %d -> %d, last %q; want one compaction entry", entriesBefore, len(entries), last.Base().Type)
	}
	if summary := last.(icodingagent.CompactionEntry).Summary; !strings.HasPrefix(summary, "the summary") {
		t.Fatalf("compaction summary %q, want the stream's result", summary)
	}
}

// waitStatusGone waits until the compaction's end event has cleared the indicator: the Session is idle before its end event reaches the mode.
func waitStatusGone(t *testing.T, h *icodingagent.TestHarness) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if kind, _ := h.Status(); kind == "" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the compaction indicator was not cleared")
		}
		runtime.Gosched()
	}
}

// Escape during the summary stream: "Compaction cancelled" as before, the session unchanged, the counter cleared and nothing left running.
func TestCompactionEscapeDuringTheStreamLeavesNothingRunning(t *testing.T) {
	h, session, provider, stream := compactingHarness(t)
	pushDelta(t, stream, 4000)
	base := h.StatusClockBase()
	waitLabel(t, h, base.Add(5*time.Second), "5s · ~1.0k tokens")
	entriesBefore := len(session.Inner().GetEntries())
	h.Do(func() { h.Key("\x1b") })
	waitStatusGone(t, h)
	select {
	case <-provider.abortDone:
	case <-time.After(10 * time.Second):
		t.Fatal("the summary request's context did not end")
	}
	if !strings.Contains(h.Chat(), "Compaction cancelled") {
		t.Fatalf("chat %q does not say the compaction was cancelled", h.Chat())
	}
	if kind, _ := h.Status(); kind != "" {
		t.Fatalf("indicator %q survives the cancelled compaction", kind)
	}
	if got := len(session.Inner().GetEntries()); got != entriesBefore {
		t.Fatalf("entries %d -> %d after Escape; the session must be unchanged", entriesBefore, got)
	}
}
