package cachewarming

import (
	"context"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/durable"
)

// Ports apps/p2/test/p2/cache-warming.test.ts (Pi c9552c7d): a conversation's last request is sent again with one output
// token before its prompt cache entry expires, while pi's policy says it pays off. The warmers run on testing/synctest's
// fake clock (Pi's mocked timers) with fake models. The worker case is e2e/cache_warming_test.go.

const conversation = durable.ConversationId(1)

// testModel is Pi's claude-opus-5-5 on Radius: costs $4/$20 per million input/output tokens, $0.2 cache reads and $5 writes,
// a 5-minute cache lifetime (1 hour with long retention).
func testModel(t *testing.T) *ai.Model {
	t.Helper()
	generated, ok := ai.LookupModelExact("anthropic/claude-opus-4-6")
	if !ok {
		t.Fatal("catalog has no anthropic/claude-opus-4-6")
	}
	model := generated.ToModel()
	model.Capabilities = generated.ToCapabilities()
	model.Capabilities.InputCostPer1M, model.Capabilities.OutputCostPer1M = 4, 20
	model.Capabilities.CacheReadCostPer1M, model.Capabilities.CacheWriteCostPer1M = 0.2, 5
	model.PromptCache = ai.ModelPromptCache{"short": 300, "long": 3600}
	return model
}

// fakeModels records each refresh's options and answers with an empty `length` response (aborted once cancelled).
type fakeModels struct {
	durable.Models
	messages []ai.Message
	mu       sync.Mutex
	sent     []ai.StreamOptions
	retries  []int
}

func (m *fakeModels) CompleteSimple(ctx context.Context, _ *ai.Model, request ai.Context, options ...ai.StreamOptions) *ai.AssistantMessage {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(request.Messages) != len(m.messages) {
		panic("the refresh does not repeat the request's messages")
	}
	m.sent = append(m.sent, options[0])
	m.retries = append(m.retries, ai.ProviderMaxRetries(ctx))
	reason := ai.StopReasonLength
	if ctx.Err() != nil {
		reason = ai.StopReasonAborted
	}
	return &ai.AssistantMessage{StopReason: reason}
}

func (m *fakeModels) refreshes() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.sent)
}

// fakeHost is what the host reports; phase nil is "warming is pointless".
type fakeHost struct {
	mu       sync.Mutex
	mode     Mode
	phase    *ai.CacheWarmingPhase
	alive    int
	recorded int
}

func (h *fakeHost) Mode() Mode {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.mode
}

func (h *fakeHost) Phase(context.Context, durable.ConversationId, *p2runtime.ModelRequest) (ai.CacheWarmingPhase, bool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.phase == nil {
		return "", false, nil
	}
	return *h.phase, true, nil
}

func (h *fakeHost) Record(context.Context, durable.ConversationId, *ai.AssistantMessage) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.recorded++
	return nil
}

func (h *fakeHost) KeepAlive(delta int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.alive += delta
}

func (h *fakeHost) state() (alive, recorded int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.alive, h.recorded
}

type rig struct {
	t       *testing.T
	host    *fakeHost
	models  *fakeModels
	warmers *ConversationWarmers
	request *p2runtime.ModelRequest
}

// setup is warmers on the fake clock; call it inside synctest.Test.
func setup(t *testing.T, mode Mode) *rig {
	t.Helper()
	request := &p2runtime.ModelRequest{
		Model:    testModel(t),
		Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hello"), Timestamp: 1}},
		Options:  ai.StreamOptions{SessionID: "provider-session", Thinking: ai.ThinkingLevel("high")},
	}
	h := &rig{t: t, host: &fakeHost{mode: mode, phase: new(ai.CacheWarmingPhaseStreaming)}, models: &fakeModels{messages: request.Messages}, request: request}
	h.warmers = NewConversationWarmers(context.Background(), h.host)
	t.Cleanup(h.warmers.Close)
	return h
}

// start starts warming after a response to a promptTokens-token prompt.
func (h *rig) start(promptTokens int, stopReason ai.StopReason, options *ai.StreamOptions) {
	request := *h.request
	if options != nil {
		request.Options = *options
	}
	usage := ai.Usage{Input: 2, CacheRead: promptTokens - 1002, CacheWrite: 1000}
	h.warmers.Start(conversation, ai.AssistantMessage{StopReason: stopReason, Usage: usage}, &request, h.models)
}

// advance moves the clock by d and lets the refreshes it fired finish (they arm the next timer when done).
func advance(d time.Duration) {
	time.Sleep(d)
	synctest.Wait()
}

// runOut advances refresh by refresh until warming stops; it returns how many refreshes were sent.
func (h *rig) runOut() int {
	for range 100 {
		if alive, _ := h.host.state(); alive == 0 {
			break
		}
		advance(270 * time.Second)
	}
	return h.models.refreshes()
}

func TestRefreshesEvery270sWithTheRequestsOptionsAndOneOutputTokenAndCountsTheSpend(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := setup(t, ModeIdle)
		h.start(600_000, ai.StopReasonToolUse, nil)
		if alive, _ := h.host.state(); alive != 1 {
			t.Fatalf("alive = %d, want 1", alive)
		}
		advance(269 * time.Second)
		if h.models.refreshes() != 0 {
			t.Fatal("refreshed before 270 s")
		}
		advance(time.Second)
		if h.models.refreshes() != 1 {
			t.Fatalf("refreshes after 270 s = %d, want 1", h.models.refreshes())
		}
		options := h.models.sent[0]
		if options.SessionID != "provider-session" || options.Thinking != "high" || options.MaxTokens != 1 || !options.DisableContinuation {
			t.Fatalf("options = %+v", options)
		}
		// Pi's `maxRetries: 0`: Go carries it on the context the refresh runs with.
		if h.models.retries[0] != 0 {
			t.Fatalf("refresh retries = %d, want 0", h.models.retries[0])
		}
		if _, recorded := h.host.state(); recorded != 1 {
			t.Fatalf("recorded = %d, want 1", recorded)
		}
		advance(270 * time.Second)
		if h.models.refreshes() != 2 {
			t.Fatalf("refreshes = %d, want 2", h.models.refreshes())
		}
	})
}

func TestWarmsUpToAnHourWhileStreamingAnd30MinutesWhileIdleModeStreamingStopsWhenIdle(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		streaming := setup(t, ModeIdle)
		streaming.start(600_000, ai.StopReasonToolUse, nil)
		if got := streaming.runOut(); got != 13 {
			t.Fatalf("streaming refreshes = %d, want 13", got)
		}
		idle := setup(t, ModeIdle)
		idle.host.phase = new(ai.CacheWarmingPhaseIdle)
		idle.start(600_000, ai.StopReasonToolUse, nil)
		if got := idle.runOut(); got != 6 {
			t.Fatalf("idle refreshes = %d, want 6", got)
		}
		streamingOnly := setup(t, ModeStreaming)
		streamingOnly.host.phase = new(ai.CacheWarmingPhaseIdle)
		streamingOnly.start(600_000, ai.StopReasonToolUse, nil)
		if got := streamingOnly.runOut(); got != 0 {
			t.Fatalf("mode streaming, idle refreshes = %d, want 0", got)
		}
	})
}

func TestDoesNotWarmWhatIsNotWorthItOrCannotBeRepeated(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := setup(t, ModeIdle)
		// A failed or aborted response, a model without a cache lifetime for the retention: nothing starts.
		h.start(600_000, ai.StopReasonError, nil)
		h.start(600_000, ai.StopReasonAborted, nil)
		h.start(600_000, ai.StopReasonToolUse, &ai.StreamOptions{CacheRetention: ai.CacheRetentionNone})
		if alive, _ := h.host.state(); alive != 0 {
			t.Fatalf("alive = %d, want 0", alive)
		}
		// Mode off (which also uninstalls the hook) stops a run at its next refresh.
		h.start(600_000, ai.StopReasonToolUse, nil)
		h.host.mu.Lock()
		h.host.mode = ModeOff
		h.host.mu.Unlock()
		advance(270 * time.Second)
		if alive, _ := h.host.state(); alive != 0 {
			t.Fatalf("alive after mode off = %d, want 0", alive)
		}
		h.host.mu.Lock()
		h.host.mode = ModeIdle
		h.host.mu.Unlock()

		// 10k tokens: a refresh costs more than it is expected to save.
		h.start(10_000, ai.StopReasonToolUse, nil)
		advance(270 * time.Second)
		// 80k tokens: worth it while the run streams, not once only a 15% chance remains that it continues.
		h.start(80_000, ai.StopReasonToolUse, nil)
		advance(270 * time.Second)
		if h.models.refreshes() != 1 {
			t.Fatalf("refreshes = %d, want 1", h.models.refreshes())
		}
		h.host.mu.Lock()
		h.host.phase = new(ai.CacheWarmingPhaseIdle)
		h.host.mu.Unlock()
		advance(270 * time.Second)
		if alive, _ := h.host.state(); h.models.refreshes() != 1 || alive != 0 {
			t.Fatalf("refreshes = %d, alive = %d, want 1 and 0", h.models.refreshes(), alive)
		}

		// The host says the conversation changed (reset, compaction, model) or ended (a settled subagent): it stops.
		h.host.mu.Lock()
		h.host.phase = nil
		h.host.mu.Unlock()
		h.start(600_000, ai.StopReasonToolUse, nil)
		advance(270 * time.Second)
		if alive, _ := h.host.state(); h.models.refreshes() != 1 || alive != 0 {
			t.Fatalf("refreshes = %d, alive = %d, want 1 and 0", h.models.refreshes(), alive)
		}
	})
}

func TestTheNextResponseReplacesTheRunCloseStopsIt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := setup(t, ModeIdle)
		h.start(600_000, ai.StopReasonToolUse, nil)
		advance(200 * time.Second)
		h.start(600_000, ai.StopReasonToolUse, nil)
		if alive, _ := h.host.state(); alive != 1 {
			t.Fatalf("alive = %d, want 1", alive)
		}
		// The first run's timer is gone: the refresh comes 270 s after the second response.
		advance(200 * time.Second)
		if h.models.refreshes() != 0 {
			t.Fatalf("refreshes = %d, want 0", h.models.refreshes())
		}
		advance(70 * time.Second)
		if h.models.refreshes() != 1 {
			t.Fatalf("refreshes = %d, want 1", h.models.refreshes())
		}
		h.warmers.Close()
		if alive, _ := h.host.state(); alive != 0 {
			t.Fatalf("alive after close = %d, want 0", alive)
		}
		advance(600 * time.Second)
		if h.models.refreshes() != 1 {
			t.Fatalf("refreshes after close = %d, want 1", h.models.refreshes())
		}
	})
}

// Pi arms its timer with setTimeout, which fires a delay beyond 2^31-1 ms after a millisecond (Node's TimeoutOverflowWarning):
// a models.json promptCache lifetime of more than 27 days refreshes at once.
func TestALifetimeBeyondSetTimeoutsRangeRefreshesAfterAMillisecond(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := setup(t, ModeIdle)
		h.request.Model.PromptCache = ai.ModelPromptCache{"short": 3_000_000}
		h.start(600_000, ai.StopReasonToolUse, nil)
		advance(2 * time.Millisecond)
		if h.models.refreshes() == 0 {
			t.Fatal("no refresh after a millisecond")
		}
	})
}
