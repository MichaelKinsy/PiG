package coding

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// warmingProvider answers every request with a 100k-token prompt and records
// each request's options.
type warmingProvider struct {
	mu      sync.Mutex
	options []ai.StreamOptions
}

func (*warmingProvider) ID() string   { return "anthropic" }
func (*warmingProvider) Close() error { return nil }
func (p *warmingProvider) Stream(_ context.Context, _ ai.TranscriptContext, options ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
	p.mu.Lock()
	p.options = append(p.options, options)
	p.mu.Unlock()
	message := &ai.AssistantMessage{
		API: ai.APIAnthropicMessages, Provider: "anthropic", Model: "warm-model",
		Content:    []ai.AssistantContentBlock{ai.TextContent{Text: "done"}},
		Usage:      ai.Usage{Input: 100_000, Output: 1, TotalTokens: 100_001},
		StopReason: ai.StopReasonStop,
	}
	return newSessionTestStream(ai.StartEvent{Partial: message}, ai.DoneEvent{Reason: ai.StopReasonStop, Message: message}), nil
}

func (p *warmingProvider) calls() []ai.StreamOptions {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.options)
}

func warmingModel(provider ai.Provider) *ai.Model {
	return &ai.Model{
		ID: "warm-model", DisplayName: "warm-model", Provider: provider,
		Capabilities: ai.ModelCapabilities{
			ContextWindow: 200_000, InputCostPer1M: 10, OutputCostPer1M: 50,
			CacheReadCostPer1M: 0.25, CacheWriteCostPer1M: 12.5,
		},
		PromptCache:  ai.ModelPromptCache{"short": 300},
		ProviderMeta: ai.ProviderMetadata{ProviderID: "anthropic", API: ai.APIAnthropicMessages},
	}
}

func warmingServices(t *testing.T, mode string) *Services {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("PIG_HOME", tmp)
	agentDir := filepath.Join(tmp, "agent")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(`{"cacheWarming":"`+mode+`"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	services, err := NewServices(ServicesOptions{CWD: t.TempDir(), AgentDir: agentDir})
	if err != nil {
		t.Fatal(err)
	}
	return services
}

func newWarmingSession(t *testing.T, mode string, options SessionOptions) (*Session, *warmingProvider) {
	t.Helper()
	provider := &warmingProvider{}
	options.Model = warmingModel(provider)
	sess, err := NewSession(warmingServices(t, mode), options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	return sess, provider
}

// sendAndSettle sends one prompt and drains the Session's events until the
// run's agent_settled, so no event processing races the test.
func sendAndSettle(t *testing.T, sess *Session) {
	t.Helper()
	settled := make(chan struct{})
	go func() {
		for event := range sess.Events() {
			if _, ok := event.(agent.AgentSettledEvent); ok {
				close(settled)
				break
			}
		}
		for range sess.Events() {
		}
	}()
	if _, err := sess.Send(context.Background(), "test"); err != nil {
		t.Fatal(err)
	}
	<-settled
}

// Port of sdk-stream-options.test.ts "schedules cache warming after a
// completed session request".
func TestSessionSchedulesCacheWarmingAfterASessionRequest(t *testing.T) {
	sess, _ := newWarmingSession(t, "idle", SessionOptions{NoSession: true})
	sendAndSettle(t, sess)
	status := sess.CacheWarmingStatus()
	if status == nil || status.NextWarmAt <= time.Now().UnixMilli() {
		t.Fatalf("status after request = %+v, want a scheduled refresh", status)
	}

	// Equivalent shallow copies remain current, but removing the request
	// prefix does not.
	sess.Agent().SetMessages(sess.Agent().Messages())
	model := *sess.Agent().Model()
	sess.Agent().SetModel(&model)
	if status := sess.CacheWarmingStatus(); status.NextWarmAt <= time.Now().UnixMilli() {
		t.Fatalf("status after shallow copies = %+v, want still scheduled", status)
	}
	sess.Agent().SetMessages(sess.Agent().Messages()[1:])
	if status := sess.CacheWarmingStatus(); status.Reason != "conversation context changed" {
		t.Fatalf("status after prefix removal = %+v", status)
	}
}

// Port of sdk-stream-options.test.ts "waits for the next request instead of
// restoring cache warming".
func TestResumedSessionWaitsForTheNextRequest(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "resume.jsonl")
	inner := icodingagent.NewSession("resume", dir)
	inner.SetPath(path)
	if _, err := inner.AppendMessage(agent.AgentMessage{User: &agent.UserMessage{Role: "user", Content: []ai.UserContentBlock{ai.TextContent{Text: "test"}}}}); err != nil {
		t.Fatal(err)
	}
	usage := ai.Usage{Input: 100_000, Output: 1}
	if _, err := inner.AppendMessage(agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: "assistant", Provider: "anthropic", ModelID: "warm-model", Usage: &usage}}); err != nil {
		t.Fatal(err)
	}
	if _, err := inner.AppendUsage("cache_warm", "anthropic", "warm-model", usage, ""); err != nil {
		t.Fatal(err)
	}
	sess, provider := newWarmingSession(t, "idle", SessionOptions{ResumePath: path})
	if len(provider.calls()) != 0 {
		t.Fatalf("provider calls = %d, want 0", len(provider.calls()))
	}
	if status := sess.CacheWarmingStatus(); status == nil || *status != (icodingagent.CacheWarmingStatus{State: "inactive", Reason: "waiting for first request"}) {
		t.Fatalf("status = %+v", status)
	}
}

// The default streaming mode stops warming once the run settles, so a
// one-shot Send never leaves a refresh scheduled.
func TestStreamingModeStopsWarmingWhenTheRunSettles(t *testing.T) {
	sess, _ := newWarmingSession(t, "streaming", SessionOptions{NoSession: true})
	sendAndSettle(t, sess)
	if status := sess.CacheWarmingStatus(); status.Reason != "agent run settled" {
		t.Fatalf("status = %+v, want agent run settled", status)
	}
}

// Turning warming off through the Session persists the mode and stops the
// active run at once.
func TestSetCacheWarmingModeReconcilesActiveWarming(t *testing.T) {
	sess, _ := newWarmingSession(t, "idle", SessionOptions{NoSession: true})
	sendAndSettle(t, sess)
	if err := sess.SetCacheWarmingMode("off"); err != nil {
		t.Fatal(err)
	}
	if got := sess.Services().SettingsManager().GetCacheWarmingMode(); got != "off" {
		t.Fatalf("persisted mode = %q", got)
	}
	if status := sess.CacheWarmingStatus(); status.Reason != "cache warming disabled" {
		t.Fatalf("status = %+v", status)
	}
}

// Replacing the inner Session (/new, /resume) retires the old warmer: the
// replacement starts fresh and waits for its first request.
func TestReplaceInnerRetiresTheCacheWarmer(t *testing.T) {
	sess, _ := newWarmingSession(t, "idle", SessionOptions{NoSession: true})
	sendAndSettle(t, sess)
	sess.ReplaceInner(icodingagent.NewSession("replacement", t.TempDir()))
	if status := sess.CacheWarmingStatus(); *status != (icodingagent.CacheWarmingStatus{State: "inactive", Reason: "waiting for first request"}) {
		t.Fatalf("status after replacement = %+v", status)
	}
}

// emitEntryAppended must give up as soon as its ctx ends, even when the
// event channel is full and has no consumer draining it: CacheWarmer.Close
// cancels the report's ctx and then blocks until this call returns (see
// TestCacheWarmerCloseReleasesABlockedReport), so a caller stuck on channel
// capacity here would make Close hang. The delivery attempt itself now runs
// in a background goroutine (see emitEntryAppended), so this proves the
// restructure didn't reintroduce the blocking wait CL27-001 asked to avoid
// duplicating a hand-rolled select/recover for.
func TestEmitEntryAppendedGivesUpWhenContextEndsWithAFullChannel(t *testing.T) {
	sess, _ := newWarmingSession(t, "idle", SessionOptions{NoSession: true, EventBufferSize: 1})
	// Never drain sess.Events(): once the forwarder hands its second event to
	// the now-full events channel it stalls there and stops draining
	// rawEvents, giving genuine, persistent backpressure to test against
	// (rather than a moment of transient fullness the forwarder immediately
	// relieves).
	deadline := time.After(2 * time.Second)
	for {
		select {
		case sess.rawEvents <- agent.QueueUpdateEvent{}:
		case <-deadline:
			t.Fatal("rawEvents never stayed full: the forwarder kept draining it")
		}
		if len(sess.rawEvents) != cap(sess.rawEvents) {
			continue
		}
		time.Sleep(20 * time.Millisecond)
		if len(sess.rawEvents) == cap(sess.rawEvents) {
			break
		}
	}
	// ctx is live (Err() == nil) when emitEntryAppended is called, so it must
	// actually be blocked in the send/ctx-race, not short-circuited by the
	// leading ctx.Err() check, when cancel fires a moment later.
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		sess.emitEntryAppended(ctx, icodingagent.UsageEntry{Kind: "cache_warm"})
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("emitEntryAppended returned before ctx ended, on a full undrained channel")
	case <-time.After(50 * time.Millisecond):
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("emitEntryAppended blocked past ctx cancellation on a full, undrained channel")
	}
}

// Requests that do not carry the Session's id (compaction and summaries use
// their own paths upstream) never restart warming.
func TestOnlySessionRequestsStartCacheWarming(t *testing.T) {
	sess, provider := newWarmingSession(t, "idle", SessionOptions{NoSession: true})
	stream := cacheWarmingStreamFn(func() *Session { return sess })
	model := sess.Agent().Model()
	if _, err := stream(context.Background(), model, ai.NormalizeContext(ai.Context{}), ai.StreamOptions{SessionID: "other"}); err != nil {
		t.Fatal(err)
	}
	if status := sess.CacheWarmingStatus(); status.Reason != "waiting for first request" {
		t.Fatalf("foreign request started warming: %+v", status)
	}
	if len(provider.calls()) != 1 {
		t.Fatalf("provider calls = %d, want the request itself", len(provider.calls()))
	}
}
