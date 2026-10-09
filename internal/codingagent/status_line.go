package codingagent

import (
	"context"
	"fmt"
	"maps"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/text/collate"
	"golang.org/x/text/language"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/nodepath"
	"github.com/MichaelKinsy/PiG/internal/nodespawn"
	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// FooterComponent is the rich footer rendered at the bottom of the
// interactive viewport. Renders as two lines matching upstream pi's
// FooterComponent (footer.ts):
//
//	Line 1: ~/<pwd> (<branch>) • <session-name>
//	Line 2: ↑<in> ↓<out> $<cost> <context%>/<window> (auto)     <model> • <thinking>
//
// Color coding on the context-% column (upstream thresholds):
//
//	<=70%  default (no color)
//	>70%   yellow (warning)
//	>90%   red (error)
//
// All inputs are read on every Render() so the line reflects live state.
type FooterComponent struct {
	tui.BaseComponent

	mu sync.RWMutex

	model                  *ai.Model
	agentName              string
	timings                *agent.Recorder
	contextTokens          int // last turn's total tokens (input+output+cacheRead+cacheWrite) for context%
	contextUnknown         bool
	projectedContextWindow int
	working                bool

	// usageTotals reads the session's all-entry usage totals on each render,
	// as upstream footer.ts sums every session entry's stored usage.
	usageTotals func() footerUsageTotals
	// routedModel reads, under a virtual model selection, the physical model of the latest response on each render (footer.ts reads session.routedModel).
	routedModel func() *RoutedModelSelection
	// subscriptionFor decides the "(sub)" marker for a newly bound model.
	subscriptionFor func(*ai.Model) bool

	// FooterDataProvider holds the cwd, git branch, extension statuses and provider count the footer reads on each render
	// (footer.ts footerData; footer-data-provider.ts).
	*FooterDataProvider

	// Session name shown in footer as " • <name>".
	name string

	// Thinking level display on line 2 right side.
	thinkingLevel string
	// Auto-compact indicator "(auto)" shown next to context %.
	autoCompactEnabled bool

	// customWorkingMessage: extension-set message shown during streaming.
	// Empty = use default spinner. Mirrors upstream loadingAnimation.setMessage.
	customWorkingMessage string

	// usingSubscription: OAuth subscription pricing (e.g. GitHub Copilot).
	// Shows "(sub)" in cost display. Mirrors upstream footer.ts:128.
	usingSubscription bool
	statusHook        func(string)

	// suppressedByExtFooter hides the standard footer. The custom footer owns
	// keyed status presentation through its FooterData snapshot.
	suppressedByExtFooter bool
}

// NewFooterComponent creates a FooterComponent bound to the given model + agent.
// timings may be nil; cost/elapsed columns are then suppressed.
func NewFooterComponent(model *ai.Model, agentName string, timings *agent.Recorder) *FooterComponent {
	if agentName == "" {
		agentName = "default"
	}
	s := &FooterComponent{
		model:              model,
		agentName:          agentName,
		timings:            timings,
		autoCompactEnabled: true, // default matches upstream
		FooterDataProvider: NewFooterDataProvider(),
	}
	return s
}

// NewFooterComponentForSession is Pi's FooterComponent constructor (footer.ts:68-71): a footer that reads usage totals and the routed model
// from session and its cwd, git branch, extension statuses and provider count from footerData. The model is set with SetModel.
func NewFooterComponentForSession(session FooterSession, footerData *FooterDataProvider) *FooterComponent {
	s := NewFooterComponent(nil, "", nil)
	if footerData != nil {
		s.FooterDataProvider = footerData
	}
	s.SetSession(session)
	return s
}

// GitBranch returns the cached git branch, empty outside a repo.
func (s *FooterComponent) GitBranch() string { return s.GetGitBranch() }

// ProviderCount returns the number of authenticated, reachable providers.
func (s *FooterComponent) ProviderCount() int { return s.GetAvailableProviderCount() }

// SetCwd binds the footer and its branch watcher to the repository present at initialization.
func (s *FooterComponent) SetCwd(cwd string) {
	s.FooterDataProvider.SetCwd(cwd)
	s.Invalidate()
}

// SetWorking flips the spinner column on/off.
func (s *FooterComponent) SetWorking(b bool) {
	s.mu.Lock()
	s.working = b
	s.mu.Unlock()
	s.Invalidate()
}

func (s *FooterComponent) SetStatusHook(fn func(string)) {
	s.mu.Lock()
	s.statusHook = fn
	s.mu.Unlock()
}

// Flash forwards status text to the interactive-mode chat status sink.
// The ttl argument is retained so existing callers do not need to change,
// but upstream-style status lines are not time-based footer overlays.
func (s *FooterComponent) Flash(msg string, ttl time.Duration) {
	_ = ttl
	s.mu.Lock()
	hook := s.statusHook
	s.mu.Unlock()
	if hook != nil {
		hook(msg)
	}
}

// SetAgentName updates the agent persona label.
func (s *FooterComponent) SetAgentName(name string) {
	s.mu.Lock()
	s.agentName = name
	s.mu.Unlock()
	s.Invalidate()
}

// SetModel rebinds the model. Upstream footer.ts derives the subscription
// marker from the active model on every render, so a rebind re-evaluates it.
func (s *FooterComponent) SetModel(m *ai.Model) {
	s.mu.RLock()
	subscriptionFor := s.subscriptionFor
	s.mu.RUnlock()
	usingSubscription := false
	if subscriptionFor != nil {
		usingSubscription = subscriptionFor(m)
	}
	s.mu.Lock()
	s.model = m
	if subscriptionFor != nil {
		s.usingSubscription = usingSubscription
	}
	s.mu.Unlock()
	s.Invalidate()
}

// SetSubscriptionResolver installs the "(sub)" decision used by SetModel.
func (s *FooterComponent) SetSubscriptionResolver(resolve func(*ai.Model) bool) {
	s.mu.Lock()
	s.subscriptionFor = resolve
	s.mu.Unlock()
}

// FooterSession is the members of Pi's AgentSession that FooterComponent reads (footer.ts:103-107, :241): sessionManager, whose entries carry the
// all-entry usage totals (getSessionStats), and routedModel, the physical model the latest response was routed to. Go's AgentSession is
// coding.Session, which imports this package, so the footer takes this consumer-owned interface of exactly those two members, and package coding
// asserts that *coding.Session implements it.
type FooterSession interface {
	// SessionManager is `sessionManager`; a nil manager has no usage.
	SessionManager() *Session
	// RoutedModelSelection is `routedModel` in the form the footer renders: nil unless a virtual model selection routed the latest response.
	RoutedModelSelection() *RoutedModelSelection
}

// SetSession points the footer at another session (footer.ts setSession); the next render reads that session's totals and routed model.
// A nil session reads neither.
func (s *FooterComponent) SetSession(session FooterSession) {
	s.mu.Lock()
	if session == nil {
		s.usageTotals, s.routedModel = nil, nil
	} else {
		s.usageTotals = func() footerUsageTotals {
			manager := session.SessionManager()
			if manager == nil {
				return footerUsageTotals{}
			}
			return manager.FooterUsageTotals()
		}
		s.routedModel = session.RoutedModelSelection
	}
	s.mu.Unlock()
	s.Invalidate()
}

// SetName updates the session name shown in the footer.
func (s *FooterComponent) SetName(name string) {
	s.mu.Lock()
	s.name = name
	s.mu.Unlock()
	s.Invalidate()
}

// SetThinkingLevel updates the thinking level display.
func (s *FooterComponent) SetThinkingLevel(level string) {
	s.mu.Lock()
	s.thinkingLevel = level
	s.mu.Unlock()
	s.Invalidate()
}

// SetAutoCompactEnabled updates the "(auto)" indicator.
func (s *FooterComponent) SetAutoCompactEnabled(enabled bool) {
	s.mu.Lock()
	s.autoCompactEnabled = enabled
	s.mu.Unlock()
	s.Invalidate()
}

// SetProviderCount updates the number of authenticated+reachable providers.
// When >1, the footer shows "(provider) model" instead of just "model".
// Mirrors upstream footer.ts:165-170.
func (s *FooterComponent) SetProviderCount(n int) {
	s.FooterDataProvider.SetProviderCount(n)
	s.Invalidate()
}

// SetUsingSubscription updates the OAuth subscription indicator.
// When true, cost display shows "(sub)". Mirrors upstream footer.ts:128.
func (s *FooterComponent) SetUsingSubscription(v bool) {
	s.mu.Lock()
	s.usingSubscription = v
	s.mu.Unlock()
	s.Invalidate()
}

// SetExtensionStatus sets (or clears) a keyed status entry in the footer's
// extension-status line. Mirrors upstream footer-data-provider.ts setStatus.
// Pass empty text to remove the key.
func (s *FooterComponent) SetExtensionStatus(key, text string) {
	s.FooterDataProvider.SetExtensionStatus(key, text)
	s.Invalidate()
}

// SetTurnContextUsage records the latest usage for the context-window column.
// Token and cost totals come from the usage totals source instead.
func (s *FooterComponent) SetTurnContextUsage(u *ai.Usage) {
	if u == nil {
		return
	}
	s.mu.Lock()
	// Context tokens = this turn's total (represents actual context window
	// usage). Upstream footer.ts uses session.getContextUsage() which calls
	// estimateContextTokens → calculateContextTokens(lastAssistant.usage)
	// = usage.totalTokens || (input + output + cacheRead + cacheWrite).
	s.contextTokens = u.Input + u.Output + u.CacheRead + u.CacheWrite
	s.mu.Unlock()
	s.Invalidate()
}

// ResetContextUsage clears the context-window column before a transcript
// rebuild re-reads it from the branch.
func (s *FooterComponent) ResetContextUsage() {
	s.mu.Lock()
	s.contextTokens = 0
	s.mu.Unlock()
	s.Invalidate()
}

// SetWorkingMessage sets a custom message shown during streaming.
// Pass empty to restore the default spinner. Mirrors upstream
// loadingAnimation.setMessage (interactive-mode.ts:1877-1885).
func (s *FooterComponent) SetWorkingMessage(message string) {
	s.mu.Lock()
	s.customWorkingMessage = message
	s.mu.Unlock()
	s.Invalidate()
}

// GetWorkingMessage returns the current custom working message or "".
func (s *FooterComponent) GetWorkingMessage() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.customWorkingMessage
}

// SetSuppressedByExtFooter controls whether an extension-owned footer replaces
// the complete standard footer, including the keyed status row. The custom
// footer receives those statuses through FooterData and owns their rendering.
func (s *FooterComponent) SetSuppressedByExtFooter(v bool) {
	s.mu.Lock()
	s.suppressedByExtFooter = v
	s.mu.Unlock()
	s.Invalidate()
}

// Render returns the footer lines (normally 2 plus keyed statuses).
func (s *FooterComponent) Render(width int) []string {
	totals, routed, snap := s.footerSources()
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.suppressedByExtFooter {
		return nil
	}

	return renderFooter(s.footerDataLocked(totals, routed, snap), width)
}

// footerSources reads the usage totals and the routed model, which may take
// the session's locks, without holding s.mu.
func (s *FooterComponent) footerSources() (footerUsageTotals, *RoutedModelSelection, footerSnapshot) {
	s.mu.RLock()
	source, routedSource := s.usageTotals, s.routedModel
	s.mu.RUnlock()
	var totals footerUsageTotals
	if source != nil {
		totals = source()
	}
	var routed *RoutedModelSelection
	if routedSource != nil {
		routed = routedSource()
	}
	cwd, gitBranch, providerCount, extensionStatuses := s.snapshot()
	return totals, routed, footerSnapshot{cwd: cwd, gitBranch: gitBranch, providerCount: providerCount, extensionStatuses: extensionStatuses}
}

// footerSnapshot is the footer data provider's state, read together without holding s.mu.
type footerSnapshot struct {
	cwd, gitBranch    string
	providerCount     int
	extensionStatuses map[string]string
}

// footerDataLocked snapshots the footer's inputs; the caller holds s.mu.
func (s *FooterComponent) footerDataLocked(totals footerUsageTotals, routed *RoutedModelSelection, snap footerSnapshot) footerData {
	return footerData{
		model:                  s.model,
		agentName:              s.agentName,
		sessionName:            s.name,
		cwd:                    snap.cwd,
		gitBranch:              snap.gitBranch,
		usage:                  totals,
		routed:                 routed,
		contextTokens:          s.contextTokens,
		contextUnknown:         s.contextUnknown,
		projectedContextWindow: s.projectedContextWindow,
		timings:                s.timings,
		working:                s.working,
		thinkingLevel:          s.thinkingLevel,
		autoCompactEnabled:     s.autoCompactEnabled,
		providerCount:          snap.providerCount,
		usingSubscription:      s.usingSubscription,
		extensionStatuses:      snap.extensionStatuses,
	}
}

// footerData is the snapshot of all data needed to render the footer.
// Passed to renderFooter as a value so the free function can be tested.
type footerData struct {
	model                  *ai.Model
	agentName              string
	sessionName            string
	cwd                    string
	gitBranch              string
	usage                  footerUsageTotals
	routed                 *RoutedModelSelection
	contextTokens          int // last turn's total tokens for context% (not cumulative)
	contextUnknown         bool
	projectedContextWindow int
	timings                *agent.Recorder
	working                bool
	thinkingLevel          string
	autoCompactEnabled     bool
	// providerCount is the number of authenticated+reachable providers.
	// When >1, the model line shows "(provider) model" instead of just "model".
	// Mirrors upstream footer.ts:165-170 / footer-data-provider.ts.
	providerCount int
	// usingSubscription is true when the model's provider uses OAuth
	// subscription pricing (e.g. GitHub Copilot). Shows "(sub)" in cost.
	// Mirrors upstream footer.ts:128.
	usingSubscription bool
	// extensionStatuses: keyed status text set by extensions via setStatus.
	// Rendered as a third footer line (sorted by key). Mirrors upstream
	// footer.ts:205-215.
	extensionStatuses map[string]string
}

// footerUsageTotals is upstream footer.ts's usageTotals over every session
// entry plus the cache-hit rate of the latest assistant message.
type footerUsageTotals struct {
	input, output, cacheRead, cacheWrite int
	cost                                 float64
	latestCacheHitRate                   *float64
}

// footerUsageParts renders footer.ts's token, cache-hit, and cost stats.
// Kimi Coding and OAuth subscription logins show the cost even at zero, with
// "(sub)"; otherwise a zero total shows no cost segment.
func footerUsageParts(u footerUsageTotals, usingSubscription bool) []string {
	var parts []string
	if u.input != 0 {
		parts = append(parts, "↑"+formatTokens(u.input))
	}
	if u.output != 0 {
		parts = append(parts, "↓"+formatTokens(u.output))
	}
	if u.cacheRead != 0 {
		parts = append(parts, "R"+formatTokens(u.cacheRead))
	}
	if u.cacheWrite != 0 {
		parts = append(parts, "W"+formatTokens(u.cacheWrite))
	}
	if (u.cacheRead > 0 || u.cacheWrite > 0) && u.latestCacheHitRate != nil {
		parts = append(parts, "CH"+tui.JSToFixed(*u.latestCacheHitRate, 1)+"%")
	}
	if (u.cost != 0 && !math.IsNaN(u.cost)) || usingSubscription {
		costStr := "$" + tui.JSToFixed(u.cost, 3)
		if usingSubscription {
			costStr += " (sub)"
		}
		parts = append(parts, costStr)
	}
	return parts
}

// formatCwdForFooter abbreviates home and its descendants as "~" and
// "~<sep><relative path>", and leaves every other cwd, including a sibling
// that only shares home's prefix, as given. The caller passes upstream's home,
// HOME then USERPROFILE. Mirrors upstream footer.ts formatCwdForFooter.
func formatCwdForFooter(cwd, home string) string {
	if home == "" {
		return cwd
	}
	resolvedCwd, err := nodepath.Resolve(cwd)
	if err != nil {
		return cwd
	}
	resolvedHome, err := nodepath.Resolve(home)
	if err != nil {
		return cwd
	}
	relativeToHome, err := filepath.Rel(resolvedHome, resolvedCwd)
	if err != nil {
		return cwd
	}
	if relativeToHome == "." {
		return "~"
	}
	if relativeToHome == ".." || strings.HasPrefix(relativeToHome, ".."+string(filepath.Separator)) || filepath.IsAbs(relativeToHome) {
		return cwd
	}
	return "~" + string(filepath.Separator) + relativeToHome
}

// renderFooter produces the 2-line footer matching upstream footer.ts.
// Line 1: pwd (branch) • name
// Line 2: ↑in ↓out [Rcache] [Wcache] $cost context%/window (auto)   model • thinking
func renderFooter(d footerData, width int) []string {
	if width <= 0 {
		width = 80
	}

	// ── Line 1: pwd ──────────────────────────────────────────────────
	pwd := footerCwd(d.cwd)
	if d.gitBranch != "" {
		pwd += " (" + d.gitBranch + ")"
	}
	if d.sessionName != "" {
		pwd += " \u2022 " + d.sessionName
	}
	line1 := widthx.TruncateToWidth(dim(pwd), width, dim("..."), false)

	// ── Line 2: stats (left) + model (right) ─────────────────────────
	// Left side: ↑in ↓out [Rcache] [Wcache] $cost context%/window (auto)
	leftParts := footerUsageParts(d.usage, d.usingSubscription)

	// Context usage: context%/window (auto)
	contextWindow, pct := footerContextUsage(d)
	autoTag := ""
	if d.autoCompactEnabled {
		autoTag = " (auto)"
	}
	display := tui.JSToFixed(pct, 1) + "%/" + formatTokens(contextWindow) + autoTag
	if d.contextUnknown {
		display = fmt.Sprintf("?/%s%s", formatTokens(contextWindow), autoTag)
	}
	leftParts = append(leftParts, colorContextDisplay(pct, display))

	// Experimental features indicator. Upstream footer.ts:162-164 pushes a dim
	// "•" separator plus a bold warning "xp" badge onto the stats when
	// PI_EXPERIMENTAL=1. Off by default, so the idle footer is unchanged.
	if experimentalFeaturesEnabled() {
		leftParts = append(leftParts, dim("•")+" "+boldWarning("xp"))
	}

	// Upstream footer.ts does NOT show a spinner or elapsed timer.
	// The working indicator lives in the statusContainer Loader
	// (between chat and editor), not the footer.

	statsLeft := strings.Join(leftParts, " ")

	// Right side: [provider] model • thinking
	// When multiple providers are available, prepend "(provider)" prefix.
	// Mirrors upstream footer.ts:165-174.
	modelName, modelProvider, thinkingLevel := footerModel(d)
	rightSide := modelName
	// Upstream footer.ts:160-162 renders the thinking level as plain
	// text: no per-level color. The entire right side is wrapped in
	// dim() with the rest of line 2, so it appears in dim grey.
	switch thinkingLevel {
	case "":
	case "off":
		rightSide = modelName + " \u2022 thinking " + thinkingLevel
	default:
		rightSide = modelName + " \u2022 " + thinkingLevel
	}

	// A virtual model routes each request; show where the latest response went (footer.ts:237-243).
	if d.routed != nil && d.routed.Model != nil {
		level := ""
		if d.routed.ThinkingLevel != "" {
			level = " \u2022 " + string(d.routed.ThinkingLevel)
		}
		rightSide += " \u2192 " + d.routed.Model.ID + level
	}

	// Prepend provider in parentheses when multiple providers are active.
	rightSideWithProvider := rightSide
	if d.providerCount > 1 && modelProvider != "" {
		rightSideWithProvider = "(" + modelProvider + ") " + rightSide
	}

	// Compose line 2 with right-alignment.
	// Try provider-prefixed right side first; fall back to plain if too wide.
	// Mirrors upstream footer.ts:167-173.
	statsLeftWidth := widthx.VisibleWidth(statsLeft)
	// If statsLeft is too wide, truncate it (upstream footer.ts).
	if statsLeftWidth > width {
		statsLeft = widthx.TruncateToWidth(statsLeft, width, "...", false)
		statsLeftWidth = widthx.VisibleWidth(statsLeft)
	}
	minPad := 2

	// Pick the widest right-side variant that fits.
	chosenRight := rightSideWithProvider
	chosenRightWidth := widthx.VisibleWidth(chosenRight)
	if statsLeftWidth+minPad+chosenRightWidth > width {
		// Provider prefix doesn't fit; fall back to plain.
		chosenRight = rightSide
		chosenRightWidth = widthx.VisibleWidth(chosenRight)
	}

	var line2 string
	totalNeeded := statsLeftWidth + minPad + chosenRightWidth
	if totalNeeded <= width {
		padding := strings.Repeat(" ", width-statsLeftWidth-chosenRightWidth)
		line2 = dim(statsLeft) + dim(padding+chosenRight)
	} else {
		// Right side doesn't fit at full width; truncate or omit
		avail := width - statsLeftWidth - minPad
		if avail > 0 {
			truncRight := widthx.TruncateToWidth(chosenRight, avail, "", false)
			truncWidth := widthx.VisibleWidth(truncRight)
			padding := strings.Repeat(" ", max(0, width-statsLeftWidth-truncWidth))
			line2 = dim(statsLeft) + dim(padding+truncRight)
		} else {
			// The remainder after the stats is empty but still dimmed (footer.ts:262-263).
			line2 = dim(statsLeft) + dim("")
		}
	}

	result := []string{line1, line2}

	// ── Line 3: extension statuses (optional) ──────────────────────────
	// Mirrors upstream footer.ts:205-215: sorted by key, space-separated,
	// sanitized (control chars → space), truncated to width.
	if status, ok := renderExtensionStatuses(d.extensionStatuses, width); ok {
		result = append(result, status)
	}

	return result
}

// renderExtensionStatuses is footer.ts:205-215: the statuses sorted by key with localeCompare, each sanitized (sanitizeStatusText) and
// joined by spaces on one row, truncated to the width with a dim ellipsis. An empty sanitized text still takes its place.
func renderExtensionStatuses(statuses map[string]string, width int) (string, bool) {
	parts := footerExtensionStatuses(statuses)
	if len(parts) == 0 {
		return "", false
	}
	return widthx.TruncateToWidth(strings.Join(parts, " "), width, dim("..."), false), true
}

// footerExtensionStatuses returns the non-blank extension statuses in key
// order, each with its runs of white space collapsed, as footer.ts's
// sanitizeStatusText does.
func footerExtensionStatuses(statuses map[string]string) []string {
	if len(statuses) == 0 {
		return nil
	}
	keys := slices.Collect(maps.Keys(statuses))
	collator := collate.New(language.Und)
	slices.SortStableFunc(keys, func(a, b string) int { return collator.CompareString(a, b) })
	parts := make([]string, len(keys))
	for i, key := range keys {
		parts[i] = sanitizeStatusText(statuses[key])
	}
	return parts
}

// footerCwd is the footer's working directory with home abbreviated.
// Upstream's session cwd is always absolute. Without one, pig shows ".",
// which is not a path to abbreviate.
func footerCwd(cwd string) string {
	if cwd == "" {
		return "."
	}
	home := os.Getenv("HOME")
	if home == "" {
		home = os.Getenv("USERPROFILE")
	}
	return formatCwdForFooter(cwd, home)
}

// footerContextUsage returns the context window and the context's percentage
// of it. Upstream footer.ts uses session.getContextUsage(), which returns the
// last turn's token count (not cumulative), the actual current context window
// pressure.
func footerContextUsage(d footerData) (contextWindow int, pct float64) {
	// The window is getContextUsage's: the limits model's, which under a virtual selection is the physical model that answered last, then the selected model's (footer.ts:162, agent-session.ts:4139-4144 _limitsModel).
	limits := d.model
	if d.routed != nil && d.routed.Model != nil {
		limits = d.routed.Model
	}
	switch {
	case limits != nil && limits.Capabilities.ContextWindow > 0:
		contextWindow = limits.Capabilities.ContextWindow
	case d.model != nil:
		contextWindow = d.model.Capabilities.ContextWindow
	case d.projectedContextWindow > 0:
		contextWindow = d.projectedContextWindow
	}
	if contextWindow > 0 {
		pct = float64(d.contextTokens) / float64(contextWindow) * 100
	}
	return contextWindow, pct
}

// footerModel returns the model's id, its provider's id, and the thinking
// level of a model that reasons ("off" when unset), or "" for one that does
// not. The upstream Agent supplies its "unknown" default model when no model
// is selected, so the footer still renders a stable model identity.
func footerModel(d footerData) (name, provider, thinkingLevel string) {
	if d.model == nil {
		return "unknown", "", ""
	}
	if d.model.Provider != nil {
		provider = d.model.Provider.ID()
	}
	if d.model.Capabilities.MaxThinking != "" {
		thinkingLevel = d.thinkingLevel
		if thinkingLevel == "" {
			thinkingLevel = "off"
		}
	}
	return d.model.ID, provider, thinkingLevel
}

// sanitizeStatusText is footer.ts sanitizeStatusText: carriage returns, newlines and tabs become spaces, runs of spaces collapse to
// one, and JavaScript's trim removes the ends. Other whitespace, such as a no-break space, stays.
func sanitizeStatusText(text string) string {
	text = strings.NewReplacer("\r", " ", "\n", " ", "\t", " ").Replace(text)
	var b strings.Builder
	previousSpace := false
	for _, r := range text {
		if r == ' ' && previousSpace {
			continue
		}
		previousSpace = r == ' '
		b.WriteRune(r)
	}
	return jsstring.Trim(b.String())
}

// colorContextDisplay wraps the full `pct%/window (auto)` display
// string in the color appropriate to the percent, matching upstream
// footer.ts which applies theme.fg("warning"/"error", contextPercentDisplay)
// to the entire token, not just the number.
func colorContextDisplay(pct float64, display string) string {
	return applyContextColor(pct, display)
}

func applyContextColor(pct float64, body string) string {
	switch {
	case pct > 90:
		return tui.ActiveTheme().Fg("error", body)
	case pct > 70:
		return tui.ActiveTheme().Fg("warning", body)
	default:
		return body // no color
	}
}

// FormatTokens is footer.ts formatTokens for presentations outside the interactive footer.
func FormatTokens(n int) string { return formatTokens(n) }

// formatTokens renders an int token count as "1.2k", "230", "8.4M", as footer.ts formatTokens does: toFixed(1) below ten thousand and ten million, Math.round (half up) elsewhere.
func formatTokens(n int) string {
	switch {
	case n < 1000:
		return strconv.Itoa(n)
	case n < 10_000:
		return tui.JSToFixed(float64(n)/1_000, 1) + "k"
	case n < 1_000_000:
		return strconv.Itoa(int(math.Floor(float64(n)/1_000+0.5))) + "k"
	case n < 10_000_000:
		return tui.JSToFixed(float64(n)/1_000_000, 1) + "M"
	default:
		return strconv.Itoa(int(math.Floor(float64(n)/1_000_000+0.5))) + "M"
	}
}

// dim wraps text in the theme's dim foreground. Mirrors upstream footer.ts:
// theme.fg("dim", text), the token's resolved color (e.g.
// \x1b[38;2;102;102;102m for dark) and an fg-only reset. The system theme's
// dim token is faint text in the terminal's default color, closed with
// \x1b[22;39m (theme.ts fg of a dim token).
func dim(s string) string {
	return tui.ActiveTheme().Fg("dim", s)
}

// boldWarning renders text in bold with the theme's warning foreground,
// mirroring upstream footer.ts theme.bold(theme.fg("warning", text)): chalk.bold
// wraps \x1b[1m..\x1b[22m around theme.fg's <warning>text\x1b[39m, so the bytes
// are \x1b[1m<warning>text\x1b[39m\x1b[22m.
func boldWarning(s string) string {
	th := tui.ActiveTheme()
	return "\x1b[1m" + th.Warning + s + "\x1b[39m" + tui.SGRBoldDimReset
}

// stripANSI removes ANSI escape sequences (delegates to widthx.StripAnsi).
func stripANSI(s string) string { return widthx.StripAnsi(s) }

// resolveGitBranchFromPaths reads the bound HEAD, asking Git only for a reftable placeholder, as FooterDataProvider.resolveGitBranchSync does.
func resolveGitBranchFromPaths(paths gitPaths) string {
	content, err := os.ReadFile(paths.headPath)
	if err != nil {
		return ""
	}
	branch, ok := strings.CutPrefix(strings.TrimSpace(string(content)), "ref: refs/heads/")
	if !ok {
		return "detached"
	}
	if branch == ".invalid" {
		if resolved := resolveBranchWithGit(context.Background(), paths.repoDir); resolved != "" {
			return resolved
		}
		return "detached"
	}
	return branch
}

// resolveBranchWithGit asks git for the current branch. It returns "" on a
// detached HEAD or when git is unavailable. Mirrors upstream
// resolveBranchWithGitSync; tests replace it to observe process spawns.
var resolveBranchWithGit = func(ctx context.Context, repoDir string) string {
	cmd := gitBranchCommand(ctx, repoDir)
	cmd.Dir = repoDir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func gitBranchCommand(ctx context.Context, repoDir string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "git", "--no-optional-locks", "symbolic-ref", "--quiet", "--short", "HEAD")
	cmd.Dir = repoDir
	// Upstream's spawnSync and execFile find git with libuv's search on
	// Windows, starting in repoDir.
	nodespawn.SetProgram(cmd)
	return cmd
}

// SetContextUsage stores the Session projection estimate outside the render path.
// Nil tokens indicate unknown usage after compaction until a valid response.
func (s *FooterComponent) SetContextUsage(tokens *int, contextWindow int) {
	s.mu.Lock()
	s.contextUnknown = tokens == nil && contextWindow > 0
	s.projectedContextWindow = contextWindow
	s.contextTokens = 0
	if tokens != nil {
		s.contextTokens = *tokens
	}
	s.mu.Unlock()
	s.Invalidate()
}

// RoutedModelSelection is the physical model and thinking level a virtual model selection currently resolves to (AgentSession.routedModel).
type RoutedModelSelection struct {
	Model         *ai.Model
	ThinkingLevel ai.ModelThinkingLevel
}
