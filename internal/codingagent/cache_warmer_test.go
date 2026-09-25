package codingagent

import (
	"context"
	"errors"
	"math"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// Port of upstream test/cache-warmer.test.ts. testing/synctest supplies the
// fake clock vitest's fake timers provide upstream.

func cacheWarmingTestModel(t *testing.T, spec string, promptCache ai.ModelPromptCache) *ai.Model {
	t.Helper()
	generated, ok := ai.LookupModelExact(spec)
	if !ok {
		t.Fatalf("catalog has no %s", spec)
	}
	model := generated.ToModel()
	model.Capabilities = generated.ToCapabilities()
	model.PromptCache = promptCache
	return model
}

type cacheWarmingModels struct {
	adaptive, budget, openai, unknown *ai.Model
}

func newCacheWarmingModels(t *testing.T) cacheWarmingModels {
	adaptive := cacheWarmingTestModel(t, "anthropic/claude-opus-4-6", ai.ModelPromptCache{"short": 300, "long": 3600})
	unknown := *adaptive
	unknown.PromptCache = nil
	return cacheWarmingModels{
		adaptive: adaptive,
		budget:   cacheWarmingTestModel(t, "anthropic/claude-sonnet-4-5", ai.ModelPromptCache{"short": 300, "long": 3600}),
		openai:   cacheWarmingTestModel(t, "openai/gpt-5", ai.ModelPromptCache{"short": 300, "long": 86_400}),
		unknown:  &unknown,
	}
}

var warmUsage = ai.Usage{Output: 1, CacheRead: 100, TotalTokens: 101, Cost: ai.UsageCost{CacheRead: 0.01, Total: 0.01}}

func warmResponse(model *ai.Model, stopReason ai.StopReason) *ai.AssistantMessage {
	return &ai.AssistantMessage{
		API: model.ProviderMeta.API, Provider: model.ProviderMeta.ProviderID, Model: model.ID,
		Usage: warmUsage, StopReason: stopReason,
	}
}

func finishedStream(message *ai.AssistantMessage) *ai.AssistantMessageEventStream {
	stream := ai.NewAssistantMessageEventStream()
	_ = stream.Push(ai.StartEvent{Partial: &ai.AssistantMessage{}})
	if message.StopReason == ai.StopReasonError || message.StopReason == ai.StopReasonAborted {
		_ = stream.Push(ai.ErrorEvent{Reason: message.StopReason, Error: message})
	} else {
		_ = stream.Push(ai.DoneEvent{Reason: message.StopReason, Message: message})
	}
	return stream
}

func branchWithPrompt(t *testing.T, promptTokens int) []SessionEntry {
	t.Helper()
	session := NewSession("branch", "/tmp")
	if _, err := session.AppendMessage(agent.AgentMessage{Assistant: &agent.AssistantMessage{
		Role: "assistant", Usage: &ai.Usage{Output: 10, CacheRead: promptTokens, TotalTokens: promptTokens + 10},
	}}); err != nil {
		t.Fatal(err)
	}
	return session.Entries()
}

type warmCall struct {
	ctx     context.Context
	model   *ai.Model
	options ai.StreamOptions
}

type fakeWarmRuntime struct {
	t        *testing.T
	warmer   *CacheWarmer
	session  *Session
	mu       sync.Mutex
	mode     CacheWarmingMode
	branch   []SessionEntry
	calls    []warmCall
	events   []CacheWarmingDecisionEvent
	appended []UsageEntry
	warmed   []UsageEntry
	result   func(*ai.Model) *ai.AssistantMessageEventStream
	decide   func(CacheWarmingDecisionEvent) CacheWarmingAction
	// appendGate runs before each usage entry is persisted.
	appendGate func()
}

func (f *fakeWarmRuntime) AppendUsage(kind, provider, model string, usage ai.Usage, note string) (UsageEntry, error) {
	if f.appendGate != nil {
		f.appendGate()
	}
	entry, err := f.session.AppendUsage(kind, provider, model, usage, note)
	f.mu.Lock()
	f.appended = append(f.appended, entry)
	f.mu.Unlock()
	return entry, err
}

func (f *fakeWarmRuntime) GetBranch() []SessionEntry {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.branch
}

func (f *fakeWarmRuntime) setMode(mode CacheWarmingMode) {
	f.mu.Lock()
	f.mode = mode
	f.mu.Unlock()
}

func (f *fakeWarmRuntime) callCount() int {
	return len(f.snapshot().calls)
}

// fakeWarmRecord is a copy of what the fake runtime observed.
type fakeWarmRecord struct {
	calls    []warmCall
	events   []CacheWarmingDecisionEvent
	appended []UsageEntry
	warmed   []UsageEntry
}

func (f *fakeWarmRuntime) snapshot() fakeWarmRecord {
	f.mu.Lock()
	defer f.mu.Unlock()
	return fakeWarmRecord{
		calls:    append([]warmCall(nil), f.calls...),
		events:   append([]CacheWarmingDecisionEvent(nil), f.events...),
		appended: append([]UsageEntry(nil), f.appended...),
		warmed:   append([]UsageEntry(nil), f.warmed...),
	}
}

type fakeWarmOption func(*fakeWarmRuntime)

func withMode(mode CacheWarmingMode) fakeWarmOption {
	return func(f *fakeWarmRuntime) { f.mode = mode }
}

func withBranch(branch []SessionEntry) fakeWarmOption {
	return func(f *fakeWarmRuntime) { f.branch = branch }
}

func withDecide(decide func(CacheWarmingDecisionEvent) CacheWarmingAction) fakeWarmOption {
	return func(f *fakeWarmRuntime) { f.decide = decide }
}

func withAppendGate(gate func()) fakeWarmOption {
	return func(f *fakeWarmRuntime) { f.appendGate = gate }
}

func withResult(result func(*ai.Model) *ai.AssistantMessageEventStream) fakeWarmOption {
	return func(f *fakeWarmRuntime) { f.result = result }
}

func newFakeWarmRuntime(t *testing.T, options ...fakeWarmOption) *fakeWarmRuntime {
	f := &fakeWarmRuntime{t: t, mode: "idle", session: NewSession("usage", "/tmp")}
	for _, option := range options {
		option(f)
	}
	if f.branch == nil {
		f.branch = branchWithPrompt(t, 100_000)
	}
	stream := func(ctx context.Context, model *ai.Model, _ ai.TranscriptContext, options ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
		f.mu.Lock()
		f.calls = append(f.calls, warmCall{ctx, model, options})
		result := f.result
		f.mu.Unlock()
		if result != nil {
			return result(model), nil
		}
		return finishedStream(warmResponse(model, ai.StopReasonLength)), nil
	}
	getMode := func() CacheWarmingMode {
		f.mu.Lock()
		defer f.mu.Unlock()
		return f.mode
	}
	decide := func(_ context.Context, event CacheWarmingDecisionEvent) (CacheWarmingAction, error) {
		f.mu.Lock()
		f.events = append(f.events, event)
		decideFn := f.decide
		f.mu.Unlock()
		if decideFn != nil {
			return decideFn(event), nil
		}
		return event.Action, nil
	}
	f.warmer = NewCacheWarmer(stream, f, getMode, decide)
	f.warmer.SetOnWarmed(func(_ context.Context, entry UsageEntry) {
		f.mu.Lock()
		f.warmed = append(f.warmed, entry)
		f.mu.Unlock()
	})
	t.Cleanup(func() {
		f.warmer.Close()
		f.warmer.Wait()
	})
	return f
}

func warmRequest(model *ai.Model, options ai.StreamOptions) CacheWarmRequest {
	return CacheWarmRequest{Model: model, Context: ai.NormalizeContext(ai.Context{}), Options: options}
}

func alwaysCurrent() bool { return true }

// advance moves the fake clock and lets every woken refresh finish.
func advance(d time.Duration) {
	time.Sleep(d)
	synctest.Wait()
}

// stopTimerForTest mirrors vi.clearAllTimers: the run stays active but its
// scheduled refresh never fires.
func stopTimerForTest(w *CacheWarmer) *cacheWarmingRun {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.run.timer.Stop() {
		w.tasks.Done()
	}
	return w.run
}

func closeTo(got, want float64) bool { return math.Abs(got-want) < 0.5e-2 }

func TestCacheWarmingDerivesEligibilityAndTiming(t *testing.T) {
	models := newCacheWarmingModels(t)
	ttl := func(model *ai.Model, env ai.ProviderEnv) any {
		if ms, ok := GetPromptCacheTtlMs(model, ai.StreamOptions{Env: env}); ok {
			return ms
		}
		return nil
	}
	t.Setenv("PI_CACHE_RETENTION", "")
	for _, tc := range []struct {
		name string
		got  any
		want any
	}{
		{"default short", ttl(models.adaptive, nil), int64(300_000)},
		{"env long", ttl(models.adaptive, ai.ProviderEnv{"PI_CACHE_RETENTION": "long"}), int64(3_600_000)},
		{"openai long", ttl(models.openai, ai.ProviderEnv{"PI_CACHE_RETENTION": "long"}), int64(86_400_000)},
		{"unknown", ttl(models.unknown, nil), nil},
	} {
		if tc.got != tc.want {
			t.Errorf("%s: ttl = %v, want %v", tc.name, tc.got, tc.want)
		}
	}
	for ttlMs, want := range map[int64]int64{300_000: 270_000, 60_000: 50_000} {
		if got, ok := GetCacheWarmingDelayMs(ttlMs); !ok || got != want {
			t.Errorf("delay(%d) = %d, %t, want %d", ttlMs, got, ok, want)
		}
	}
	if _, ok := GetCacheWarmingDelayMs(10_000); ok {
		t.Error("delay(10000) is defined, want undefined")
	}
	replayable := []bool{
		IsReplayable(models.budget, ai.StreamOptions{Thinking: ai.ThinkingMedium}),
		IsReplayable(models.budget, ai.StreamOptions{}),
		IsReplayable(models.adaptive, ai.StreamOptions{Thinking: ai.ThinkingMedium}),
		IsReplayable(models.openai, ai.StreamOptions{Thinking: ai.ThinkingMedium}),
	}
	if want := []bool{false, true, true, true}; !equalBools(replayable, want) {
		t.Errorf("replayable = %v, want %v", replayable, want)
	}
}

func equalBools(a, b []bool) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestCacheWarmingReplaysProfitableRequestsAndPreservesOptions(t *testing.T) {
	models := newCacheWarmingModels(t)
	synctest.Test(t, func(t *testing.T) {
		f := newFakeWarmRuntime(t)
		transformHeaders := func(_ context.Context, headers ai.ProviderHeaders) (ai.ProviderHeaders, error) { return headers, nil }
		requestCtx, cancelRequest := context.WithCancel(context.Background())
		defer cancelRequest()
		f.warmer.Start(warmRequest(models.adaptive, ai.StreamOptions{Thinking: ai.ThinkingHigh, SessionID: "s", TransformHeaders: transformHeaders}), alwaysCurrent)
		advance(270 * time.Second)

		if f.callCount() != 1 {
			t.Fatalf("calls = %d, want 1", f.callCount())
		}
		record := f.snapshot()
		call := record.calls[0]
		if call.model != models.adaptive || call.options.Thinking != ai.ThinkingHigh || call.options.SessionID != "s" || call.options.TransformHeaders == nil || call.options.MaxTokens != 1 {
			t.Fatalf("warm request = %+v", call.options)
		}
		if got := ai.ProviderMaxRetries(call.ctx); got != 0 {
			t.Fatalf("warm request maxRetries = %d, want 0", got)
		}
		if call.ctx == requestCtx {
			t.Fatal("warm request reused the original cancellation context")
		}
		event := record.events[0]
		if event.Type != "cache_warming_decision" || event.ContinuationProbability != 1 || event.Action != CacheWarmingActionWarm {
			t.Fatalf("decision event = %+v", event)
		}
		if !closeTo(event.MissCost, 0.575) || !closeTo(event.WarmCost, 0.050025) {
			t.Fatalf("costs = miss %v warm %v, want 0.575 and 0.050025", event.MissCost, event.WarmCost)
		}
		if len(record.appended) != 1 || record.appended[0].Kind != "cache_warm" || record.appended[0].Provider != "anthropic" || record.appended[0].Model != models.adaptive.ID || record.appended[0].Usage != warmUsage || record.appended[0].Note != "" {
			t.Fatalf("appended usage = %+v", record.appended)
		}
		if len(record.warmed) != 1 || record.warmed[0].ID != record.appended[0].ID {
			t.Fatalf("onWarmed = %+v, want the appended entry", record.warmed)
		}
		advance(270 * time.Second)
		if f.callCount() != 2 {
			t.Fatalf("calls after second window = %d, want 2", f.callCount())
		}
		f.warmer.Cancel()
	})
}

func TestCacheWarmingSkipsRefreshesAfterTheirSafeDeadline(t *testing.T) {
	models := newCacheWarmingModels(t)
	synctest.Test(t, func(t *testing.T) {
		f := newFakeWarmRuntime(t)
		f.warmer.Start(warmRequest(models.adaptive, ai.StreamOptions{}), alwaysCurrent)
		// A five-minute cache is scheduled for 4m30s and retains 15 seconds
		// of the 30-second expiry margin. Simulate a timer delayed by sleep.
		run := stopTimerForTest(f.warmer)
		time.Sleep(285_001 * time.Millisecond)
		f.warmer.refresh(run)
		if f.callCount() != 0 {
			t.Fatalf("calls = %d, want 0", f.callCount())
		}
		if status := f.warmer.Status(); status.State != "inactive" || status.Reason != "cache refresh deadline missed" {
			t.Fatalf("status = %+v", status)
		}
	})
}

func TestCacheWarmingRechecksTheDeadlineAfterAnExtensionDecision(t *testing.T) {
	models := newCacheWarmingModels(t)
	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		f := newFakeWarmRuntime(t, withDecide(func(CacheWarmingDecisionEvent) CacheWarmingAction {
			time.Sleep(time.Until(start.Add(285_001 * time.Millisecond)))
			return CacheWarmingActionWarm
		}))
		f.warmer.Start(warmRequest(models.adaptive, ai.StreamOptions{}), alwaysCurrent)
		f.warmer.refresh(stopTimerForTest(f.warmer))
		if f.callCount() != 0 {
			t.Fatalf("calls = %d, want 0", f.callCount())
		}
		if status := f.warmer.Status(); status.State != "inactive" || status.Reason != "cache refresh deadline missed" {
			t.Fatalf("status = %+v", status)
		}
	})
}

func TestCacheWarmingAppliesEconomicDecisionsAndExtensionOverrides(t *testing.T) {
	models := newCacheWarmingModels(t)
	synctest.Test(t, func(t *testing.T) {
		unprofitable := newFakeWarmRuntime(t, withBranch(branchWithPrompt(t, 5_000)))
		unprofitable.warmer.Start(warmRequest(models.adaptive, ai.StreamOptions{}), alwaysCurrent)
		advance(270 * time.Second)
		status := unprofitable.warmer.Status()
		if unprofitable.callCount() != 0 || status.State != "inactive" || status.Decision == nil || status.Decision.Action != CacheWarmingActionStop || !status.Decision.EconomicsAvailable || status.ExtensionOverride {
			t.Fatalf("unprofitable: calls %d status %+v", unprofitable.callCount(), status)
		}

		forced := newFakeWarmRuntime(t, withBranch(branchWithPrompt(t, 5_000)), withDecide(func(CacheWarmingDecisionEvent) CacheWarmingAction { return CacheWarmingActionWarm }))
		forced.warmer.Start(warmRequest(models.adaptive, ai.StreamOptions{}), alwaysCurrent)
		advance(270 * time.Second)
		if forced.callCount() != 1 || len(forced.snapshot().warmed) != 1 || forced.snapshot().warmed[0].Note != "extension override" {
			t.Fatalf("forced: calls %d warmed %+v", forced.callCount(), forced.snapshot().warmed)
		}
		forced.warmer.Cancel()

		vetoed := newFakeWarmRuntime(t, withDecide(func(CacheWarmingDecisionEvent) CacheWarmingAction { return CacheWarmingActionStop }))
		vetoed.warmer.Start(warmRequest(models.adaptive, ai.StreamOptions{}), alwaysCurrent)
		advance(270 * time.Second)
		if status := vetoed.warmer.Status(); vetoed.callCount() != 0 || status.State != "inactive" || !status.ExtensionOverride {
			t.Fatalf("vetoed: calls %d status %+v", vetoed.callCount(), status)
		}

		unavailable := newFakeWarmRuntime(t, withBranch(branchWithPrompt(t, 0)))
		unavailable.warmer.Start(warmRequest(models.adaptive, ai.StreamOptions{}), alwaysCurrent)
		if status := unavailable.warmer.Status(); status.State != "inactive" || status.Reason != "cache economics unavailable" {
			t.Fatalf("unavailable: status %+v", status)
		}
		advance(270 * time.Second)
		if unavailable.callCount() != 0 {
			t.Fatalf("unavailable: calls = %d", unavailable.callCount())
		}
	})
}

func TestCacheWarmingStopsForUnsupportedRequestsContextChangesAndModeChanges(t *testing.T) {
	models := newCacheWarmingModels(t)
	synctest.Test(t, func(t *testing.T) {
		unsupported := newFakeWarmRuntime(t)
		reason := func() string { return unsupported.warmer.Status().Reason }
		unsupported.setMode("off")
		unsupported.warmer.Start(warmRequest(models.adaptive, ai.StreamOptions{}), alwaysCurrent)
		if got := reason(); got != "cache warming disabled" {
			t.Fatalf("off: reason = %q", got)
		}
		unsupported.setMode("idle")
		unsupported.warmer.Start(warmRequest(models.unknown, ai.StreamOptions{}), alwaysCurrent)
		if got := reason(); got != "cache lifetime unavailable" {
			t.Fatalf("unknown model: reason = %q", got)
		}
		unsupported.warmer.Start(warmRequest(models.budget, ai.StreamOptions{Thinking: ai.ThinkingHigh}), alwaysCurrent)
		if got := reason(); got != "request cannot be replayed safely" {
			t.Fatalf("budget thinking: reason = %q", got)
		}

		var stillCurrent sync.Mutex
		current := true
		unsupported.warmer.Start(warmRequest(models.adaptive, ai.StreamOptions{}), func() bool {
			stillCurrent.Lock()
			defer stillCurrent.Unlock()
			return current
		})
		stillCurrent.Lock()
		current = false
		stillCurrent.Unlock()
		if got := reason(); got != "conversation context changed" {
			t.Fatalf("context change: reason = %q", got)
		}
		advance(270 * time.Second)
		if unsupported.callCount() != 0 {
			t.Fatalf("context change: calls = %d", unsupported.callCount())
		}

		unsupported.warmer.Start(warmRequest(models.adaptive, ai.StreamOptions{}), alwaysCurrent)
		unsupported.setMode("off")
		advance(270 * time.Second)
		if unsupported.callCount() != 0 {
			t.Fatalf("mode off: calls = %d", unsupported.callCount())
		}

		streaming := newFakeWarmRuntime(t, withMode("streaming"), withBranch(branchWithPrompt(t, 400_000)))
		streaming.warmer.Start(warmRequest(models.adaptive, ai.StreamOptions{}), alwaysCurrent)
		streaming.warmer.OnAgentSettled()
		if got := streaming.warmer.Status().Reason; got != "agent run settled" {
			t.Fatalf("streaming settle: reason = %q", got)
		}
	})
}

func TestCacheWarmingAbortsReplacedRequestsAndSkipsFailedRefreshes(t *testing.T) {
	models := newCacheWarmingModels(t)
	synctest.Test(t, func(t *testing.T) {
		var pendingStream *ai.AssistantMessageEventStream
		pending := newFakeWarmRuntime(t, withResult(func(*ai.Model) *ai.AssistantMessageEventStream {
			pendingStream = ai.NewAssistantMessageEventStream()
			return pendingStream
		}))
		pending.warmer.Start(warmRequest(models.adaptive, ai.StreamOptions{}), alwaysCurrent)
		advance(270 * time.Second)
		pending.warmer.Start(warmRequest(models.adaptive, ai.StreamOptions{}), alwaysCurrent)
		if !errors.Is(pending.snapshot().calls[0].ctx.Err(), context.Canceled) {
			t.Fatalf("replaced request was not aborted: %v", pending.snapshot().calls[0].ctx.Err())
		}
		_ = pendingStream.Push(ai.StartEvent{Partial: &ai.AssistantMessage{}})
		_ = pendingStream.Push(ai.DoneEvent{Reason: ai.StopReasonLength, Message: warmResponse(models.adaptive, ai.StopReasonLength)})
		pending.warmer.Cancel()
		advance(600 * time.Second)
		if pending.callCount() != 1 || len(pending.snapshot().appended) != 0 {
			t.Fatalf("pending: calls %d appended %d", pending.callCount(), len(pending.snapshot().appended))
		}

		failed := newFakeWarmRuntime(t, withResult(func(model *ai.Model) *ai.AssistantMessageEventStream {
			return finishedStream(warmResponse(model, ai.StopReasonError))
		}))
		failed.warmer.Start(warmRequest(models.adaptive, ai.StreamOptions{}), alwaysCurrent)
		advance(270 * time.Second)
		if len(failed.snapshot().appended) != 0 {
			t.Fatalf("failed refresh recorded usage: %+v", failed.snapshot().appended)
		}
		failed.warmer.Cancel()
	})
}

func TestCacheWarmingFormatsStatusAndUsageEntries(t *testing.T) {
	decision := &CacheWarmingDecision{
		Phase: "idle", WarmCost: 0.013, MissCost: 0.621, ContinuationProbability: 0.6,
		ExpectedSavings: 0.36, EconomicsAvailable: true, Action: CacheWarmingActionWarm,
	}
	if got, want := FormatCacheWarmingStatus(CacheWarmingStatus{State: "scheduled", NextWarmAt: 222_000, Decision: decision}, 0),
		"Decision in 3m 42s (60% continuation probability, expected savings $0.360 >= $0.050 -> warm)"; got != want {
		t.Fatalf("status = %q, want %q", got, want)
	}
	usage := warmUsage
	usage.Cost = ai.UsageCost{Input: 0.00004, Output: 0.00005, CacheRead: 0.02940725, Total: 0.02949725}
	entry, err := NewSession("format", "/tmp").AppendUsage("cache_warm", "anthropic", "claude-opus-4-6", usage, "extension override")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := FormatCacheWarmingUsage(entry), "Cache warmed (extension override): $0.029497"; got != want {
		t.Fatalf("usage = %q, want %q", got, want)
	}
}

// The refresh is an owned background task: Close cancels an in-flight warm
// request through its context, Wait drains it, and a closed warmer ignores
// later requests.
func TestCacheWarmerCloseAbortsAndDrainsInFlightRefresh(t *testing.T) {
	models := newCacheWarmingModels(t)
	synctest.Test(t, func(t *testing.T) {
		f := newFakeWarmRuntime(t)
		stream := func(ctx context.Context, model *ai.Model, _ ai.TranscriptContext, options ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
			f.mu.Lock()
			f.calls = append(f.calls, warmCall{ctx, model, options})
			f.mu.Unlock()
			pending := ai.NewAssistantMessageEventStream()
			_ = pending.Push(ai.StartEvent{Partial: &ai.AssistantMessage{}})
			go func() {
				<-ctx.Done()
				_ = pending.Push(ai.ErrorEvent{Reason: ai.StopReasonAborted, Error: warmResponse(model, ai.StopReasonAborted)})
			}()
			return pending, nil
		}
		f.warmer.stream = stream
		f.warmer.Start(warmRequest(models.adaptive, ai.StreamOptions{}), alwaysCurrent)
		advance(270 * time.Second)
		if status := f.warmer.Status(); status.State != "refreshing" {
			t.Fatalf("status during refresh = %+v, want refreshing", status)
		}
		f.warmer.Close()
		f.warmer.Wait()
		record := f.snapshot()
		if len(record.calls) != 1 || !errors.Is(record.calls[0].ctx.Err(), context.Canceled) {
			t.Fatalf("in-flight request was not cancelled: %+v", record.calls)
		}
		if len(record.appended) != 0 {
			t.Fatalf("aborted refresh recorded usage: %+v", record.appended)
		}
		f.warmer.Start(warmRequest(models.adaptive, ai.StreamOptions{}), alwaysCurrent)
		advance(time.Hour)
		if f.callCount() != 1 {
			t.Fatalf("closed warmer sent %d requests, want 1", f.callCount())
		}
	})
}

// Close while a refresh is still persisting its usage entry: the entry is
// written, but the disposed warmer never reports it.
func TestCacheWarmerCloseDuringPersistenceSuppressesOnWarmed(t *testing.T) {
	models := newCacheWarmingModels(t)
	synctest.Test(t, func(t *testing.T) {
		entered, release := make(chan struct{}), make(chan struct{})
		f := newFakeWarmRuntime(t, withAppendGate(func() {
			close(entered)
			<-release
		}))
		f.warmer.Start(warmRequest(models.adaptive, ai.StreamOptions{}), alwaysCurrent)
		time.Sleep(270 * time.Second)
		<-entered
		f.warmer.Close()
		close(release)
		f.warmer.Wait()
		record := f.snapshot()
		if len(record.appended) != 1 {
			t.Fatalf("appended = %+v, want the one persisted refresh", record.appended)
		}
		if len(record.warmed) != 0 {
			t.Fatalf("closed warmer reported %d usage entries", len(record.warmed))
		}
	})
}

// A report blocked on delivery when Close starts sees its context cancelled,
// and Close returns only after the report does.
func TestCacheWarmerCloseReleasesABlockedReport(t *testing.T) {
	models := newCacheWarmingModels(t)
	synctest.Test(t, func(t *testing.T) {
		f := newFakeWarmRuntime(t)
		var reportErr error
		f.warmer.SetOnWarmed(func(ctx context.Context, _ UsageEntry) {
			<-ctx.Done()
			reportErr = ctx.Err()
		})
		f.warmer.Start(warmRequest(models.adaptive, ai.StreamOptions{}), alwaysCurrent)
		advance(270 * time.Second)
		f.warmer.Close()
		if !errors.Is(reportErr, context.Canceled) {
			t.Fatalf("report returned with %v before Close finished, want context.Canceled", reportErr)
		}
		f.warmer.Wait()
	})
}

// Streaming warming stops at the one-hour safety window even while the agent
// keeps running; warm requests never extend it.
func TestCacheWarmingStopsAtTheOneHourSafetyLimit(t *testing.T) {
	models := newCacheWarmingModels(t)
	synctest.Test(t, func(t *testing.T) {
		f := newFakeWarmRuntime(t, withMode("streaming"))
		f.warmer.Start(warmRequest(models.adaptive, ai.StreamOptions{}), alwaysCurrent)
		advance(2 * time.Hour)
		// Refreshes at 4m30s intervals fit 13 times into the first hour.
		if got := f.callCount(); got != 13 {
			t.Fatalf("refreshes = %d, want 13", got)
		}
		if status := f.warmer.Status(); status.Reason != "one-hour safety limit reached" {
			t.Fatalf("status = %+v", status)
		}
	})
}

// Idle warming stops at the 30-minute idle window after the request.
func TestCacheWarmingStopsAtTheIdleSafetyLimit(t *testing.T) {
	models := newCacheWarmingModels(t)
	synctest.Test(t, func(t *testing.T) {
		f := newFakeWarmRuntime(t, withBranch(branchWithPrompt(t, 1_000_000)))
		f.warmer.Start(warmRequest(models.adaptive, ai.StreamOptions{}), alwaysCurrent)
		f.warmer.OnAgentSettled()
		advance(time.Hour)
		// Refreshes at 4m30s intervals fit 6 times into 30 minutes.
		if got := f.callCount(); got != 6 {
			t.Fatalf("refreshes = %d, want 6", got)
		}
		if status := f.warmer.Status(); status.Reason != "30-minute idle safety limit reached" {
			t.Fatalf("status = %+v", status)
		}
	})
}
