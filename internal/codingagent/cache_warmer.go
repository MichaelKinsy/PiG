package codingagent

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// Port of upstream core/cache-warmer.ts.

const (
	// maxWarmingAgeMs: streaming warming never continues past this long after
	// the real request that started it.
	maxWarmingAgeMs int64 = 60 * 60_000
	// maxIdleWarmingAgeMs: idle warming uses a shorter horizon because
	// continuation estimates become less reliable with age.
	maxIdleWarmingAgeMs int64 = 30 * 60_000
	// cacheWarmingMinimumExpectedSavings: a refresh is sent only when it is
	// expected to save at least this many dollars.
	cacheWarmingMinimumExpectedSavings = 0.05
	// idleContinuationProbability is the chance that a real request arrives
	// before the cache entry expires while the agent sits idle.
	idleContinuationProbability = 0.15
)

// GetCacheWarmingDelayMs refreshes at 90% of the TTL while preserving at least
// ten seconds of margin. The bool is false when the TTL is too short.
func GetCacheWarmingDelayMs(ttlMs int64) (int64, bool) {
	if ttlMs <= 10_000 {
		return 0, false
	}
	return max(1, int64(math.Floor(math.Min(float64(ttlMs)*0.9, float64(ttlMs-10_000))))), true
}

// GetPromptCacheTtlMs is the lifetime of the prompt cache entry a request
// writes, from the model's promptCache tier for the retention the request
// used. The bool is false when the model has no lifetime for that tier.
//
// PiG's StreamOptions has no per-request cacheRetention (providers read only
// PI_CACHE_RETENTION), so the retention comes from the environment, as it does
// for every upstream session request.
func GetPromptCacheTtlMs(model *ai.Model, options ai.StreamOptions) (int64, bool) {
	retention := "short"
	if cacheWarmingEnvValue("PI_CACHE_RETENTION", options.Env) == "long" {
		retention = "long"
	}
	seconds, ok := model.PromptCache[retention]
	if !ok {
		return 0, false
	}
	return int64(seconds) * 1000, true
}

// cacheWarmingEnvValue mirrors getProviderEnvValue: request env first, then
// the process environment.
func cacheWarmingEnvValue(name string, env ai.ProviderEnv) string {
	if value := env[name]; value != "" {
		return value
	}
	return os.Getenv(name)
}

// IsReplayable reports whether replaying the request with a one-token output
// cap leaves its cache entry untouched. Anthropic's budget-based thinking
// derives budget_tokens from max_tokens, and Anthropic keys the message cache
// on that budget, so only adaptive thinking replays safely.
func IsReplayable(model *ai.Model, options ai.StreamOptions) bool {
	reasoning := options.Thinking != "" && options.Thinking != ai.ThinkingOff
	if !reasoning || model.ProviderMeta.API != ai.APIAnthropicMessages {
		return true
	}
	compat := model.ProviderMeta.Compat
	return compat != nil && compat.ForceAdaptiveThinking != nil && *compat.ForceAdaptiveThinking
}

// lastPromptTokens is the prompt size of the most recent real request on the
// branch, as reported by the provider.
func lastPromptTokens(entries []SessionEntry) int {
	for _, entry := range slices.Backward(entries) {
		if entry.Base.Type != "message" {
			continue
		}
		var probe struct {
			Message struct {
				Role  string   `json:"role"`
				Usage ai.Usage `json:"usage"`
			} `json:"message"`
		}
		if json.Unmarshal(entry.raw, &probe) != nil || probe.Message.Role != "assistant" {
			continue
		}
		usage := probe.Message.Usage
		return usage.Input + usage.CacheRead + usage.CacheWrite
	}
	return 0
}

func price(model *ai.Model, usage ai.Usage) float64 {
	return ai.CalculateCost(model, &usage).Total
}

// CacheWarmingAction is Pi's warm-or-stop decision.
type CacheWarmingAction string

const (
	CacheWarmingActionWarm CacheWarmingAction = "warm"
	CacheWarmingActionStop CacheWarmingAction = "stop"
)

// CacheWarmingDecision holds the inputs and outcome of one warm-or-stop
// decision, as shown by /session.
type CacheWarmingDecision struct {
	// Phase is "streaming" while the agent run that sent the request is still
	// active, then "idle".
	Phase string `json:"phase"`
	// WarmCost is the price of this refresh: a cache read of the prompt plus
	// one output token.
	WarmCost float64 `json:"warmCost"`
	// MissCost is the extra price of the next real request if the entry is lost.
	MissCost float64 `json:"missCost"`
	// ContinuationProbability estimates the chance that a real request
	// arrives before the entry expires.
	ContinuationProbability float64 `json:"continuationProbability"`
	// ExpectedSavings is ContinuationProbability*MissCost - WarmCost.
	ExpectedSavings float64 `json:"expectedSavings"`
	// EconomicsAvailable is false when the prompt size or prices are unknown.
	EconomicsAvailable bool `json:"economicsAvailable"`
	// Action is "warm" when ExpectedSavings is at least $0.05.
	Action CacheWarmingAction `json:"action"`
}

// CacheWarmingDecisionEvent is fired before each refresh with Pi's decision
// filled in; an extension may override Action. Field order matches the
// payload upstream emits.
type CacheWarmingDecisionEvent struct {
	Type                    string             `json:"type"`
	WarmCost                float64            `json:"warmCost"`
	MissCost                float64            `json:"missCost"`
	ContinuationProbability float64            `json:"continuationProbability"`
	Action                  CacheWarmingAction `json:"action"`
}

// CacheWarmingStatus is the warmer state /session reports.
type CacheWarmingStatus struct {
	// State is "inactive", "scheduled" (a refresh timer is armed), or
	// "refreshing" (a warm request is in flight).
	State string `json:"state"`
	// Reason says why nothing is scheduled.
	Reason string `json:"reason,omitempty"`
	// NextWarmAt is the Unix millisecond time of the next decision, or 0.
	NextWarmAt int64 `json:"nextWarmAt,omitempty"`
	// Decision is the pending decision, or the decision that stopped warming.
	Decision *CacheWarmingDecision `json:"decision,omitempty"`
	// ExtensionOverride is true when an extension changed Decision.Action.
	ExtensionOverride bool `json:"extensionOverride,omitempty"`
}

// CacheWarmRequest is the request whose prompt cache entry should be kept
// warm, exactly as it was sent.
type CacheWarmRequest struct {
	Model   *ai.Model
	Context ai.TranscriptContext
	Options ai.StreamOptions
}

// CacheWarmerSessionManager is the Session surface the warmer uses. Mirrors
// Pick<SessionManager, "appendUsage" | "getBranch">.
type CacheWarmerSessionManager interface {
	AppendUsage(kind, provider, model string, usage ai.Usage, note string) (UsageEntry, error)
	GetBranch() []SessionEntry
}

// CacheWarmingDecide lets an extension override a decision. An error falls
// back to Pi's decision.
type CacheWarmingDecide func(context.Context, CacheWarmingDecisionEvent) (CacheWarmingAction, error)

type cacheWarmingRun struct {
	CacheWarmRequest
	// isCurrent is false once the session's model or messages no longer
	// match the request.
	isCurrent func() bool
	ttlMs     int64
	delayMs   int64
	// refreshDeadlineAt is the latest safe time to send this refresh,
	// leaving half the original expiry margin.
	refreshDeadlineAt int64
	startedAt         int64
	ctx               context.Context
	cancel            context.CancelFunc
	phase             string
	nextWarmAt        int64
	// extensionOverride is set while a refresh that an extension forced is
	// in flight.
	extensionOverride bool
	// timer is nil while a refresh is running.
	timer *time.Timer
}

// CacheWarmer keeps one prompt cache entry alive by re-sending its request
// with a one-token output cap before the entry expires. Start replaces any
// previous run; warm requests never extend the fixed safety windows.
//
// Upstream's unawaited setTimeout refresh maps to an owned task: a timer whose
// callback the warmer tracks, cancelled through the run's context and drained
// by Wait.
type CacheWarmer struct {
	mu             sync.Mutex
	run            *cacheWarmingRun
	inactive       CacheWarmingStatus
	closed         bool
	stream         agent.StreamFn
	sessionManager CacheWarmerSessionManager
	getMode        func() CacheWarmingMode
	decide         CacheWarmingDecide
	onWarmed       func(context.Context, UsageEntry)
	tasks          sync.WaitGroup
	// lifetime is cancelled by Close. publishMu is held while onWarmed runs,
	// so Close can wait out a report that began before it.
	lifetime  context.Context
	dispose   context.CancelFunc
	publishMu sync.Mutex
}

// NewCacheWarmer creates a warmer. A nil decide keeps Pi's own decision.
func NewCacheWarmer(stream agent.StreamFn, sessionManager CacheWarmerSessionManager, getMode func() CacheWarmingMode, decide CacheWarmingDecide) *CacheWarmer {
	if decide == nil {
		decide = func(_ context.Context, event CacheWarmingDecisionEvent) (CacheWarmingAction, error) {
			return event.Action, nil
		}
	}
	lifetime, dispose := context.WithCancel(context.Background())
	return &CacheWarmer{
		stream:         stream,
		sessionManager: sessionManager,
		getMode:        getMode,
		decide:         decide,
		inactive:       inactiveCacheWarming("waiting for first request"),
		lifetime:       lifetime,
		dispose:        dispose,
	}
}

func inactiveCacheWarming(reason string) CacheWarmingStatus {
	return CacheWarmingStatus{State: "inactive", Reason: reason}
}

func cacheWarmingNowMs() int64 { return time.Now().UnixMilli() }

// SetOnWarmed sets the callback that receives the persisted usage entry after
// each successful refresh. Its context is cancelled when Close starts; a
// callback blocked on delivery must return once it is, because Close waits for
// it. The callback must not call Close.
func (w *CacheWarmer) SetOnWarmed(onWarmed func(context.Context, UsageEntry)) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.closed {
		w.onWarmed = onWarmed
	}
}

// Status reports the current warming state and the policy inputs that
// produced it.
func (w *CacheWarmer) Status() CacheWarmingStatus {
	if w.getMode() == "off" {
		return inactiveCacheWarming("cache warming disabled")
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	run := w.run
	if run == nil {
		return w.inactive
	}
	if !run.isCurrent() {
		return inactiveCacheWarming("conversation context changed")
	}
	decision := w.evaluate(run)
	refreshing := run.timer == nil
	if !decision.EconomicsAvailable && !refreshing {
		return inactiveCacheWarming("cache economics unavailable")
	}
	state := "scheduled"
	if refreshing {
		state = "refreshing"
	}
	return CacheWarmingStatus{State: state, NextWarmAt: run.nextWarmAt, Decision: &decision, ExtensionOverride: run.extensionOverride}
}

// Start keeps the prompt cache entry written by request warm while isCurrent
// holds.
func (w *CacheWarmer) Start(request CacheWarmRequest, isCurrent func() bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return
	}
	w.clearRunLocked()
	if reason := w.startStopReason(request); reason != "" {
		w.stopLocked(reason, nil, false)
		return
	}
	ttlMs, _ := GetPromptCacheTtlMs(request.Model, request.Options)
	delayMs, _ := GetCacheWarmingDelayMs(ttlMs)
	ctx, cancel := context.WithCancel(context.Background())
	w.run = &cacheWarmingRun{
		CacheWarmRequest: request,
		isCurrent:        isCurrent,
		ttlMs:            ttlMs,
		delayMs:          delayMs,
		startedAt:        cacheWarmingNowMs(),
		ctx:              ctx,
		cancel:           cancel,
		phase:            "streaming",
	}
	w.scheduleLocked(w.run)
}

// startStopReason says why a request cannot be warmed, or "" when it can.
func (w *CacheWarmer) startStopReason(request CacheWarmRequest) string {
	if w.getMode() == "off" {
		return "cache warming disabled"
	}
	if !IsReplayable(request.Model, request.Options) {
		return "request cannot be replayed safely"
	}
	ttlMs, ok := GetPromptCacheTtlMs(request.Model, request.Options)
	if !ok {
		return "cache lifetime unavailable"
	}
	if _, ok := GetCacheWarmingDelayMs(ttlMs); !ok {
		return "cache lifetime unavailable"
	}
	return ""
}

// OnAgentSettled moves the run to its idle phase, or stops it in streaming
// mode.
func (w *CacheWarmer) OnAgentSettled() {
	w.mu.Lock()
	defer w.mu.Unlock()
	run := w.run
	if run == nil {
		return
	}
	if w.getMode() == "streaming" {
		w.stopLocked("agent run settled", nil, false)
		return
	}
	run.phase = "idle"
	deadline := run.startedAt + maxIdleWarmingAgeMs
	if run.nextWarmAt > deadline || cacheWarmingNowMs() >= deadline {
		w.stopLocked("30-minute idle safety limit reached", nil, false)
	}
}

// OnModeChanged reconciles an active run after the persisted mode changes.
func (w *CacheWarmer) OnModeChanged() {
	w.mu.Lock()
	defer w.mu.Unlock()
	run := w.run
	if run == nil {
		return
	}
	if reason := w.modeStopReason(run); reason != "" {
		w.stopLocked(reason, nil, false)
	}
}

// Cancel stops warming. Mirrors upstream cancel().
func (w *CacheWarmer) Cancel() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.stopLocked("inactive", nil, false)
}

// Close cancels warming for good, as upstream dispose does: later Start calls
// are ignored and, once Close returns, no refresh reports to OnWarmed, even
// one still persisting its usage entry. Close waits for a report already in
// progress, after cancelling that report's context.
func (w *CacheWarmer) Close() {
	w.mu.Lock()
	w.closed = true
	w.onWarmed = nil
	w.stopLocked("inactive", nil, false)
	w.dispose()
	w.mu.Unlock()
	// An empty critical section waits out an in-progress report.
	w.publishMu.Lock()
	//lint:ignore SA2001 the empty critical section is the wait.
	w.publishMu.Unlock() //nolint:staticcheck // SA2001: see above.
}

// Wait blocks until every scheduled or in-flight refresh has finished, then releases the closed warmer's provider and Session references. Call it after Close.
func (w *CacheWarmer) Wait() {
	w.tasks.Wait()
	w.mu.Lock()
	w.sessionManager = nil
	w.stream = nil
	w.decide = nil
	w.mu.Unlock()
}

func (w *CacheWarmer) clearRunLocked() {
	run := w.run
	if run == nil {
		return
	}
	w.run = nil
	if run.timer != nil && run.timer.Stop() {
		// A stopped AfterFunc may remain in the timer heap. Its callback cannot run, so release the captured request and conversation immediately.
		run.CacheWarmRequest = CacheWarmRequest{}
		run.isCurrent = nil
		w.tasks.Done()
	}
	run.cancel()
}

func (w *CacheWarmer) stopLocked(reason string, decision *CacheWarmingDecision, extensionOverride bool) {
	w.clearRunLocked()
	w.inactive = CacheWarmingStatus{State: "inactive", Reason: reason, Decision: decision, ExtensionOverride: extensionOverride}
}

func (w *CacheWarmer) scheduleLocked(run *cacheWarmingRun) {
	run.extensionOverride = false
	now := cacheWarmingNowMs()
	run.nextWarmAt = now + run.delayMs
	// A timer can run late after sleep or a stalled process. Keep half of the
	// planned pre-expiry margin for that delay and request dispatch; a late
	// refresh is likely a full-price cache write, not a cache warm.
	run.refreshDeadlineAt = run.nextWarmAt + (run.ttlMs-run.delayMs)/2
	limit, reason := maxWarmingAgeMs, "one-hour safety limit reached"
	if run.phase == "idle" {
		limit, reason = maxIdleWarmingAgeMs, "30-minute idle safety limit reached"
	}
	if deadline := run.startedAt + limit; run.nextWarmAt > deadline || now >= deadline {
		w.stopLocked(reason, nil, false)
		return
	}
	w.tasks.Add(1)
	run.timer = time.AfterFunc(time.Duration(max(0, run.nextWarmAt-now))*time.Millisecond, func() {
		defer w.tasks.Done()
		w.refresh(run)
	})
}

func (w *CacheWarmer) refresh(run *cacheWarmingRun) {
	decision, ok := w.beginRefresh(run)
	if !ok {
		return
	}
	action := decision.Action
	if decided, err := w.decide(run.ctx, CacheWarmingDecisionEvent{
		Type:                    "cache_warming_decision",
		WarmCost:                decision.WarmCost,
		MissCost:                decision.MissCost,
		ContinuationProbability: decision.ContinuationProbability,
		Action:                  action,
	}); err == nil {
		action = decided
	}
	extensionOverride, warm := w.applyDecision(run, decision, action)
	if !warm || !w.sendRefresh(run, extensionOverride) {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.run == run {
		w.scheduleLocked(run)
	}
}

// beginRefresh marks the run as refreshing and evaluates Pi's decision.
func (w *CacheWarmer) beginRefresh(run *cacheWarmingRun) (CacheWarmingDecision, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	run.timer = nil
	if !w.validateRunLocked(run) || w.refreshDeadlineMissedLocked(run) {
		return CacheWarmingDecision{}, false
	}
	return w.evaluate(run), true
}

// applyDecision rechecks the run after the extension decision and stops it
// when the final action is "stop".
func (w *CacheWarmer) applyDecision(run *cacheWarmingRun, decision CacheWarmingDecision, action CacheWarmingAction) (extensionOverride, warm bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.validateRunLocked(run) || w.refreshDeadlineMissedLocked(run) {
		return false, false
	}
	extensionOverride = action != decision.Action
	if action == CacheWarmingActionStop {
		reason := "cache economics unavailable"
		switch {
		case extensionOverride:
			reason = "stopped by extension"
		case decision.EconomicsAvailable:
			reason = "expected savings below threshold"
		}
		w.stopLocked(reason, &decision, extensionOverride)
		return extensionOverride, false
	}
	run.extensionOverride = extensionOverride
	return extensionOverride, true
}

// sendRefresh replays the request with a one-token output cap and no provider
// retries, and records its usage. It reports whether the run should be
// rescheduled. Warming is best-effort: a failed request only skips the usage.
func (w *CacheWarmer) sendRefresh(run *cacheWarmingRun, extensionOverride bool) bool {
	options := run.Options
	options.MaxTokens = 1
	stream, err := w.stream(ai.WithProviderMaxRetries(run.ctx, 0), run.Model, run.Context, options)
	if err != nil {
		return true
	}
	message := stream.Result()
	w.mu.Lock()
	valid := w.validateRunLocked(run)
	w.mu.Unlock()
	if !valid {
		return false
	}
	if message == nil || message.StopReason == ai.StopReasonError || message.StopReason == ai.StopReasonAborted {
		return true
	}
	model := message.ResponseModel
	if model == "" {
		model = message.Model
	}
	note := ""
	if extensionOverride {
		note = "extension override"
	}
	entry, err := w.sessionManager.AppendUsage("cache_warm", message.Provider, model, message.Usage, note)
	if err == nil {
		w.publishWarmed(entry)
	}
	return true
}

// publishWarmed reports a persisted entry unless Close has started.
// Persisting can block, and Close does not wait for it, so the callback is
// read only after the entry is written, under publishMu, which Close takes
// after clearing it.
func (w *CacheWarmer) publishWarmed(entry UsageEntry) {
	w.publishMu.Lock()
	defer w.publishMu.Unlock()
	w.mu.Lock()
	onWarmed := w.onWarmed
	w.mu.Unlock()
	if onWarmed != nil {
		onWarmed(w.lifetime, entry)
	}
}

func (w *CacheWarmer) refreshDeadlineMissedLocked(run *cacheWarmingRun) bool {
	if cacheWarmingNowMs() <= run.refreshDeadlineAt {
		return false
	}
	w.stopLocked("cache refresh deadline missed", nil, false)
	return true
}

func (w *CacheWarmer) validateRunLocked(run *cacheWarmingRun) bool {
	if w.run != run {
		return false
	}
	reason := w.modeStopReason(run)
	if reason == "" && !run.isCurrent() {
		reason = "conversation context changed"
	}
	if reason == "" {
		return true
	}
	w.stopLocked(reason, nil, false)
	return false
}

func (w *CacheWarmer) modeStopReason(run *cacheWarmingRun) string {
	switch mode := w.getMode(); {
	case mode == "off":
		return "cache warming disabled"
	case mode == "streaming" && run.phase == "idle":
		return "agent run settled"
	}
	return ""
}

func (w *CacheWarmer) evaluate(run *cacheWarmingRun) CacheWarmingDecision {
	model := run.Model
	promptTokens := lastPromptTokens(w.sessionManager.GetBranch())
	cacheHitCost := price(model, ai.Usage{CacheRead: promptTokens})
	missUsage := ai.Usage{Input: promptTokens}
	if model.Capabilities.CacheWriteCostPer1M > 0 {
		missUsage = ai.Usage{CacheWrite: promptTokens}
	}
	cacheMissCost := price(model, missUsage)
	warmCost := price(model, ai.Usage{CacheRead: promptTokens, Output: 1})
	missCost := math.Max(0, cacheMissCost-cacheHitCost)
	continuationProbability := 1.0
	if run.phase == "idle" {
		continuationProbability = idleContinuationProbability
	}
	expectedSavings := continuationProbability*missCost - warmCost
	action := CacheWarmingActionStop
	if expectedSavings >= cacheWarmingMinimumExpectedSavings {
		action = CacheWarmingActionWarm
	}
	return CacheWarmingDecision{
		Phase:                   run.phase,
		WarmCost:                warmCost,
		MissCost:                missCost,
		ContinuationProbability: continuationProbability,
		ExpectedSavings:         expectedSavings,
		EconomicsAvailable:      promptTokens > 0 && (cacheHitCost > 0 || cacheMissCost > 0),
		Action:                  action,
	}
}

// jsToFixed mirrors JavaScript Number.prototype.toFixed for a finite value:
// it rounds the exact binary value, and an exact tie rounds away from zero.
func jsToFixed(value float64, digits int) string {
	sign := ""
	if value < 0 {
		sign, value = "-", -value
	}
	scale := new(big.Float).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(digits)), nil))
	scaled := new(big.Float).SetPrec(512).Mul(new(big.Float).SetFloat64(value), scale)
	whole, _ := scaled.Int(nil)
	if new(big.Float).SetPrec(512).Sub(scaled, new(big.Float).SetInt(whole)).Cmp(big.NewFloat(0.5)) >= 0 {
		whole.Add(whole, big.NewInt(1))
	}
	text := fmt.Sprintf("%0*s", digits+1, whole.String())
	if whole.Sign() == 0 {
		sign = ""
	}
	return sign + text[:len(text)-digits] + "." + text[len(text)-digits:]
}

func formatDollars(value float64) string {
	if value < 0 {
		return "-$" + jsToFixed(math.Abs(value), 3)
	}
	return "$" + jsToFixed(value, 3)
}

func formatCacheWarmingEconomics(decision CacheWarmingDecision) string {
	if !decision.EconomicsAvailable {
		return "cache economics unavailable"
	}
	probability := int(math.Round(decision.ContinuationProbability * 100))
	probabilityText := fmt.Sprintf("%d%% continuation probability", probability)
	if decision.Phase == "streaming" {
		probabilityText += " while agent is running"
	}
	comparison := "<"
	if decision.Action == CacheWarmingActionWarm {
		comparison = ">="
	}
	return fmt.Sprintf("%s, expected savings %s %s $%s", probabilityText, formatDollars(decision.ExpectedSavings), comparison, jsToFixed(cacheWarmingMinimumExpectedSavings, 3))
}

func formatCacheWarmingDecisionTime(nextWarmAt, now int64) string {
	if nextWarmAt == 0 || nextWarmAt <= now {
		return "Decision now"
	}
	remainingSeconds := int64(math.Ceil(float64(nextWarmAt-now) / 1000))
	hours := remainingSeconds / 3600
	minutes := remainingSeconds % 3600 / 60
	seconds := remainingSeconds % 60
	var parts []string
	if hours > 0 {
		parts = append(parts, fmt.Sprintf("%dh", hours))
	}
	if minutes > 0 {
		parts = append(parts, fmt.Sprintf("%dm", minutes))
	}
	if seconds > 0 || len(parts) == 0 {
		parts = append(parts, fmt.Sprintf("%ds", seconds))
	}
	return "Decision in " + strings.Join(parts, " ")
}

// FormatCacheWarmingStatus is the one-line status /session shows. now is a
// Unix millisecond time.
func FormatCacheWarmingStatus(status CacheWarmingStatus, now int64) string {
	decision := status.Decision
	// A decision is attached once Pi (or an extension) acted on it; "inactive"
	// without one never got that far.
	if decision == nil || (status.State == "inactive" && !decision.EconomicsAvailable && !status.ExtensionOverride) {
		reason := status.Reason
		if reason == "" {
			reason = "unknown reason"
		}
		return "Inactive (" + reason + ")"
	}
	details := formatCacheWarmingEconomics(*decision) + " -> " + string(decision.Action)
	if status.ExtensionOverride {
		details = "extension override, " + formatCacheWarmingEconomics(*decision)
	}
	switch status.State {
	case "inactive":
		return "Stopped (" + details + ")"
	case "refreshing":
		return "Warming cache (" + details + ")"
	}
	return formatCacheWarmingDecisionTime(status.NextWarmAt, now) + " (" + details + ")"
}

// FormatCacheWarmingUsage is the one-line transcript text for persisted
// cache-warming usage.
func FormatCacheWarmingUsage(entry UsageEntry) string {
	note := ""
	if entry.Note != "" {
		note = " (" + entry.Note + ")"
	}
	cost := jsToFixed(entry.Usage.Cost.Total, 6)
	// Keep at least three decimals and drop trailing zeros after them.
	if dot := strings.IndexByte(cost, '.'); dot >= 0 && len(cost) > dot+4 {
		cost = cost[:dot+4] + strings.TrimRight(cost[dot+4:], "0")
	}
	return "Cache warmed" + note + ": $" + cost
}
