package cachewarming

import (
	"context"
	"fmt"
	"os"
	"slices"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/durable"
)

// Ports apps/p2/plugins/cache-warming/warmer.ts (Pi c9552c7d, #11857): prompt cache warming for the session's
// conversations, as pi-coding-agent does it for its one session: before the cache entry a conversation's last request wrote
// expires, send that request again with a one-token output cap, for as long as that is expected to pay off. The policy is
// pi-ai's (ai/cache_warming.go); this file schedules it per conversation.

// Mode is the `cacheWarming` setting: "idle" also warms after a run settled, "streaming" only while it runs.
type Mode string

// The modes of the setting.
const (
	ModeOff       Mode = "off"
	ModeStreaming Mode = "streaming"
	ModeIdle      Mode = "idle"
)

// Host is what the warmers read and do outside themselves; the worker facet binds it to the Harness and the settings.
type Host interface {
	Mode() Mode
	// Phase is "streaming" while the conversation's run is under way, "idle" after it, or ok false when warming the
	// conversation is pointless: its next request will not start with request, or nobody is expected to continue it.
	Phase(ctx context.Context, conversationID durable.ConversationId, request *p2runtime.ModelRequest) (phase ai.CacheWarmingPhase, ok bool, err error)
	// Record adds a refresh's spend to the conversation's usage.
	Record(ctx context.Context, conversationID durable.ConversationId, message *ai.AssistantMessage) error
	// KeepAlive counts the conversations being warmed; a count keeps the worker process alive.
	KeepAlive(delta int)
}

// run is one conversation's warming.
type run struct {
	request *p2runtime.ModelRequest
	models  durable.Models
	// promptTokens is the prompt size of the request, as the provider reported it.
	promptTokens int
	delayMs      float64
	startedAt    int64
	ctx          context.Context
	cancel       context.CancelFunc
	stopTimer    func()
}

// ConversationWarmers is one warming run per conversation, started by each response and replaced by the next one. Each
// refresh asks the host for the conversation's phase and refreshes while the request is at most an hour old (30 minutes once
// idle) and the refresh pays off by pi-ai's EvaluateCacheWarming.
type ConversationWarmers struct {
	host Host
	// base is the lifetime every run's context derives from.
	base context.Context
	mu   sync.Mutex
	runs map[durable.ConversationId]*run
	// refreshes are the refreshes in flight; Close waits for them.
	refreshes sync.WaitGroup
	closed    bool
}

// NewConversationWarmers is warmers on host, whose runs' contexts derive from base.
func NewConversationWarmers(base context.Context, host Host) *ConversationWarmers {
	return &ConversationWarmers{host: host, base: base, runs: map[durable.ConversationId]*run{}}
}

// Start warms the cache entry request wrote, replacing the conversation's run: message is its response, models sends the
// refreshes.
func (w *ConversationWarmers) Start(conversationID durable.ConversationId, message ai.AssistantMessage, request *p2runtime.ModelRequest, models durable.Models) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.stopLocked(conversationID)
	if w.closed {
		return
	}
	// A failed or aborted response ended its run.
	if message.StopReason == ai.StopReasonError || message.StopReason == ai.StopReasonAborted {
		return
	}
	// Mode "off" uninstalls the hook; a run already armed stops at its next refresh.
	if !ai.IsCacheWarmingReplayable(request.Model, request.Options) {
		return
	}
	ttlMs, ok := ai.GetPromptCacheTtlMs(request.Model, request.Options)
	if !ok {
		return
	}
	delayMs, ok := ai.GetCacheWarmingDelayMs(ttlMs)
	if !ok {
		return
	}
	ctx, cancel := context.WithCancel(w.base)
	started := &run{
		request:      request,
		models:       models,
		promptTokens: message.Usage.Input + message.Usage.CacheRead + message.Usage.CacheWrite,
		delayMs:      delayMs,
		startedAt:    time.Now().UnixMilli(),
		ctx:          ctx,
		cancel:       cancel,
	}
	w.runs[conversationID] = started
	w.host.KeepAlive(1)
	w.armLocked(conversationID, started)
}

// maxTimeoutMs is the longest delay Node's setTimeout takes: Pi turns a longer one into a millisecond (with a warning).
const maxTimeoutMs = 2147483647

// armLocked schedules started's next refresh. A lifetime (models.json promptCache) whose refresh delay is beyond 2^31-1 ms
// arms the timer for 2^31-1 ms: it fires past the hour's window, wanted says no, and the run stops without a request.
func (w *ConversationWarmers) armLocked(conversationID durable.ConversationId, started *run) {
	delayMs := started.delayMs
	// pig divergence (D612): clamp the arm delay to 2^31-1 ms; Pi's setTimeout turns a longer delay into 1 ms.
	if delayMs > maxTimeoutMs {
		delayMs = maxTimeoutMs
	}
	delay := time.Duration(delayMs * float64(time.Millisecond))
	timer := time.AfterFunc(delay, func() {
		w.mu.Lock()
		if w.closed || w.runs[conversationID] != started {
			w.mu.Unlock()
			return
		}
		w.refreshes.Add(1)
		w.mu.Unlock()
		defer w.refreshes.Done()
		w.refresh(conversationID, started)
	})
	started.stopTimer = func() { timer.Stop() }
}

// Stop stops warming the conversation; a refresh in flight is aborted.
func (w *ConversationWarmers) Stop(conversationID durable.ConversationId) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.stopLocked(conversationID)
}

func (w *ConversationWarmers) stopLocked(conversationID durable.ConversationId) {
	stopped := w.runs[conversationID]
	if stopped == nil {
		return
	}
	delete(w.runs, conversationID)
	if stopped.stopTimer != nil {
		stopped.stopTimer()
	}
	stopped.cancel()
	w.host.KeepAlive(-1)
}

// Close stops every run (a reload or the worker's stop) and waits for the refreshes in flight, which it aborted.
func (w *ConversationWarmers) Close() {
	w.mu.Lock()
	w.closed = true
	for conversationID := range w.runs {
		w.stopLocked(conversationID)
	}
	w.mu.Unlock()
	w.refreshes.Wait()
}

func (w *ConversationWarmers) current(conversationID durable.ConversationId, r *run) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.runs[conversationID] == r
}

func (w *ConversationWarmers) refresh(conversationID durable.ConversationId, r *run) {
	w.refreshOnce(conversationID, r)
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.runs[conversationID] == r && !w.closed {
		w.armLocked(conversationID, r)
	}
}

// refreshOnce is one refresh; a run no longer wanted is stopped. Warming is best effort: a failed refresh never touches the
// conversation, the next one may succeed.
func (w *ConversationWarmers) refreshOnce(conversationID durable.ConversationId, r *run) {
	defer func() {
		if value := recover(); value != nil && r.ctx.Err() == nil {
			fmt.Fprintln(os.Stderr, "p2 cache-warming: refreshing failed:", value)
		}
	}()
	wanted, err := w.wanted(conversationID, r)
	if err != nil {
		if r.ctx.Err() == nil {
			fmt.Fprintln(os.Stderr, "p2 cache-warming: refreshing failed:", err)
		}
		return
	}
	if !wanted {
		if w.current(conversationID, r) {
			w.Stop(conversationID)
		}
		return
	}
	ctx, options := ai.GetCacheWarmingRequestOptions(r.request.Options, r.ctx)
	message := r.models.CompleteSimple(ctx, r.request.Model, ai.Context{Messages: slices.Clone(r.request.Messages)}, options)
	if message == nil || !w.current(conversationID, r) {
		return
	}
	if message.StopReason != ai.StopReasonError && message.StopReason != ai.StopReasonAborted {
		if err := w.host.Record(r.ctx, conversationID, message); err != nil && r.ctx.Err() == nil {
			fmt.Fprintln(os.Stderr, "p2 cache-warming: refreshing failed:", err)
		}
	}
}

// wanted is whether this refresh is still wanted: the run current, in its phase's window, and paying off. An error is a
// refresh that failed (the next may succeed).
func (w *ConversationWarmers) wanted(conversationID durable.ConversationId, r *run) (bool, error) {
	mode := w.host.Mode()
	if mode == ModeOff {
		return false, nil
	}
	phase, ok, err := w.host.Phase(r.ctx, conversationID, r.request)
	if err != nil {
		return false, err
	}
	if !ok || (phase == ai.CacheWarmingPhaseIdle && mode == ModeStreaming) {
		return false, nil
	}
	maxAge := ai.CacheWarmingMaxAgeMs
	if phase == ai.CacheWarmingPhaseIdle {
		maxAge = ai.CacheWarmingMaxIdleAgeMs
	}
	if time.Now().UnixMilli() > r.startedAt+maxAge {
		return false, nil
	}
	decision := ai.EvaluateCacheWarming(r.request.Model, r.promptTokens, phase)
	return decision.Action == ai.CacheWarmingActionWarm && w.current(conversationID, r), nil
}
