package codingagent

import (
	"fmt"
	"maps"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// StatusLine is the rich footer rendered at the bottom of the
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
type StatusLine struct {
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
	// subscriptionFor decides the "(sub)" marker for a newly bound model.
	subscriptionFor func(*ai.Model) bool

	// Cwd and gitBranch for line 1.
	cwd       string
	gitBranch string // cached; resolved once at init via resolveGitBranch

	// Session name shown in footer as " • <name>".
	name string

	// Thinking level display on line 2 right side.
	thinkingLevel string
	// Auto-compact indicator "(auto)" shown next to context %.
	autoCompactEnabled bool

	// customWorkingMessage: extension-set message shown during streaming.
	// Empty = use default spinner. Mirrors upstream loadingAnimation.setMessage.
	customWorkingMessage string

	// hiddenThinkingLabel: extension-set label for hidden thinking blocks.
	// Empty = use default "Thinking...". Mirrors upstream setHiddenThinkingLabel.
	hiddenThinkingLabel string

	// providerCount: number of authenticated+reachable providers.
	// When >1, model line shows "(provider) model" prefix.
	// Mirrors upstream footer-data-provider.ts.
	providerCount int
	// usingSubscription: OAuth subscription pricing (e.g. GitHub Copilot).
	// Shows "(sub)" in cost display. Mirrors upstream footer.ts:128.
	usingSubscription bool
	statusHook        func(string)

	// branchChangeMu guards the OnBranchChange subscribers, upstream
	// footer-data-provider.ts branchChangeCallbacks.
	branchChangeMu     sync.Mutex
	branchChangeHooks  map[int]func()
	branchChangeNextID int

	// extensionStatuses: keyed status strings set by extensions via
	// ctx.setStatus(key, text). Rendered as a third footer line when
	// non-empty. Mirrors upstream footer.ts:205-215 / footer-data-provider.ts.
	extensionStatuses map[string]string

	// suppressedByExtFooter hides the standard footer. The custom footer owns
	// keyed status presentation through its FooterData snapshot.
	suppressedByExtFooter bool
}

// NewStatusLine creates a StatusLine bound to the given model + agent.
// timings may be nil; cost/elapsed columns are then suppressed.
func NewStatusLine(model *ai.Model, agentName string, timings *agent.Recorder) *StatusLine {
	if agentName == "" {
		agentName = "default"
	}
	s := &StatusLine{
		model:              model,
		agentName:          agentName,
		timings:            timings,
		autoCompactEnabled: true, // default matches upstream
		extensionStatuses:  make(map[string]string),
		branchChangeHooks:  make(map[int]func()),
	}
	return s
}

// SetCwd sets the working directory shown in the footer and resolves
// the git branch. Call once at init.
func (s *StatusLine) SetCwd(cwd string) {
	s.mu.Lock()
	s.cwd = cwd
	s.gitBranch = resolveGitBranch(cwd)
	s.mu.Unlock()
	s.Invalidate()
}

// SetWorking flips the spinner column on/off.
func (s *StatusLine) SetWorking(b bool) {
	s.mu.Lock()
	s.working = b
	s.mu.Unlock()
	s.Invalidate()
}

func (s *StatusLine) SetStatusHook(fn func(string)) {
	s.mu.Lock()
	s.statusHook = fn
	s.mu.Unlock()
}

// Flash forwards status text to the interactive-mode chat status sink.
// The ttl argument is retained so existing callers do not need to change,
// but upstream-style status lines are not time-based footer overlays.
func (s *StatusLine) Flash(msg string, ttl time.Duration) {
	_ = ttl
	s.mu.Lock()
	hook := s.statusHook
	s.mu.Unlock()
	if hook != nil {
		hook(msg)
	}
}

// SetAgentName updates the agent persona label.
func (s *StatusLine) SetAgentName(name string) {
	s.mu.Lock()
	s.agentName = name
	s.mu.Unlock()
	s.Invalidate()
}

// SetModel rebinds the model. Upstream footer.ts derives the subscription
// marker from the active model on every render, so a rebind re-evaluates it.
func (s *StatusLine) SetModel(m *ai.Model) {
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
func (s *StatusLine) SetSubscriptionResolver(resolve func(*ai.Model) bool) {
	s.mu.Lock()
	s.subscriptionFor = resolve
	s.mu.Unlock()
}

// SetUsageTotalsSource installs the session usage totals read on each render.
func (s *StatusLine) SetUsageTotalsSource(source func() footerUsageTotals) {
	s.mu.Lock()
	s.usageTotals = source
	s.mu.Unlock()
	s.Invalidate()
}

// SetName updates the session name shown in the footer.
func (s *StatusLine) SetName(name string) {
	s.mu.Lock()
	s.name = name
	s.mu.Unlock()
	s.Invalidate()
}

// SetThinkingLevel updates the thinking level display.
func (s *StatusLine) SetThinkingLevel(level string) {
	s.mu.Lock()
	s.thinkingLevel = level
	s.mu.Unlock()
	s.Invalidate()
}

// SetAutoCompactEnabled updates the "(auto)" indicator.
func (s *StatusLine) SetAutoCompactEnabled(enabled bool) {
	s.mu.Lock()
	s.autoCompactEnabled = enabled
	s.mu.Unlock()
	s.Invalidate()
}

// SetProviderCount updates the number of authenticated+reachable providers.
// When >1, the footer shows "(provider) model" instead of just "model".
// Mirrors upstream footer.ts:165-170.
func (s *StatusLine) SetProviderCount(n int) {
	s.mu.Lock()
	s.providerCount = n
	s.mu.Unlock()
	s.Invalidate()
}

// OnBranchChange subscribes to git-branch updates. Returns an unsubscribe.
func (s *StatusLine) OnBranchChange(fn func()) func() {
	s.branchChangeMu.Lock()
	id := s.branchChangeNextID
	s.branchChangeNextID++
	s.branchChangeHooks[id] = fn
	s.branchChangeMu.Unlock()
	return func() {
		s.branchChangeMu.Lock()
		delete(s.branchChangeHooks, id)
		s.branchChangeMu.Unlock()
	}
}

// notifyBranchChange runs the OnBranchChange subscribers in subscription order,
// like upstream footer-data-provider's branchChangeCallbacks.
func (s *StatusLine) notifyBranchChange() {
	s.branchChangeMu.Lock()
	ids := slices.Sorted(maps.Keys(s.branchChangeHooks))
	hooks := make([]func(), len(ids))
	for i, id := range ids {
		hooks[i] = s.branchChangeHooks[id]
	}
	s.branchChangeMu.Unlock()
	for _, fn := range hooks {
		fn()
	}
}

// SetUsingSubscription updates the OAuth subscription indicator.
// When true, cost display shows "(sub)". Mirrors upstream footer.ts:128.
func (s *StatusLine) SetUsingSubscription(v bool) {
	s.mu.Lock()
	s.usingSubscription = v
	s.mu.Unlock()
	s.Invalidate()
}

// SetExtensionStatus sets (or clears) a keyed status entry in the footer's
// extension-status line. Mirrors upstream footer-data-provider.ts setStatus.
// Pass empty text to remove the key.
func (s *StatusLine) SetExtensionStatus(key, text string) {
	s.mu.Lock()
	if text == "" {
		delete(s.extensionStatuses, key)
	} else {
		s.extensionStatuses[key] = text
	}
	s.mu.Unlock()
	s.Invalidate()
}

// GitBranch returns the cached git branch, empty outside a repo.
func (s *StatusLine) GitBranch() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.gitBranch
}

// ProviderCount returns the number of authenticated, reachable providers.
func (s *StatusLine) ProviderCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.providerCount
}

// GetExtensionStatuses returns a snapshot of all extension statuses.
// Used by diagnostics and the footer renderer.
func (s *StatusLine) GetExtensionStatuses() map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cp := make(map[string]string, len(s.extensionStatuses))
	maps.Copy(cp, s.extensionStatuses)
	return cp
}

// SetTurnContextUsage records the latest usage for the context-window column.
// Token and cost totals come from the usage totals source instead.
func (s *StatusLine) SetTurnContextUsage(u *ai.Usage) {
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
func (s *StatusLine) ResetContextUsage() {
	s.mu.Lock()
	s.contextTokens = 0
	s.mu.Unlock()
	s.Invalidate()
}

// SetWorkingMessage sets a custom message shown during streaming.
// Pass empty to restore the default spinner. Mirrors upstream
// loadingAnimation.setMessage (interactive-mode.ts:1877-1885).
func (s *StatusLine) SetWorkingMessage(message string) {
	s.mu.Lock()
	s.customWorkingMessage = message
	s.mu.Unlock()
	s.Invalidate()
}

// GetWorkingMessage returns the current custom working message or "".
func (s *StatusLine) GetWorkingMessage() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.customWorkingMessage
}

// SetHiddenThinkingLabel sets the label for hidden thinking blocks.
// Pass empty to restore the default "Thinking...". Mirrors upstream
// setHiddenThinkingLabel (interactive-mode.ts:1635-1649).
func (s *StatusLine) SetHiddenThinkingLabel(label string) {
	s.mu.Lock()
	s.hiddenThinkingLabel = label
	s.mu.Unlock()
	s.Invalidate()
}

// GetHiddenThinkingLabel returns the current hidden thinking label
// or "Thinking..." if not set.
func (s *StatusLine) GetHiddenThinkingLabel() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.hiddenThinkingLabel != "" {
		return s.hiddenThinkingLabel
	}
	return "Thinking..."
}

// SetSuppressedByExtFooter controls whether an extension-owned footer replaces
// the complete standard footer, including the keyed status row. The custom
// footer receives those statuses through FooterData and owns their rendering.
func (s *StatusLine) SetSuppressedByExtFooter(v bool) {
	s.mu.Lock()
	s.suppressedByExtFooter = v
	s.mu.Unlock()
	s.Invalidate()
}

// Render returns the footer lines (normally 2 plus keyed statuses).
func (s *StatusLine) Render(width int) []string {
	s.mu.RLock()
	source := s.usageTotals
	s.mu.RUnlock()
	var totals footerUsageTotals
	if source != nil {
		totals = source()
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.suppressedByExtFooter {
		return nil
	}

	return renderFooter(footerData{
		model:                  s.model,
		agentName:              s.agentName,
		sessionName:            s.name,
		cwd:                    s.cwd,
		gitBranch:              s.gitBranch,
		usage:                  totals,
		contextTokens:          s.contextTokens,
		contextUnknown:         s.contextUnknown,
		projectedContextWindow: s.projectedContextWindow,
		timings:                s.timings,
		working:                s.working,
		thinkingLevel:          s.thinkingLevel,
		autoCompactEnabled:     s.autoCompactEnabled,
		providerCount:          s.providerCount,
		usingSubscription:      s.usingSubscription,
		extensionStatuses:      s.extensionStatuses,
	}, width)
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
	resolvedCwd, err := filepath.Abs(cwd)
	if err != nil {
		return cwd
	}
	resolvedHome, err := filepath.Abs(home)
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
	// Upstream's session cwd is always absolute. Without one, pig shows ".",
	// which is not a path to abbreviate.
	pwd := "."
	if d.cwd != "" {
		home := os.Getenv("HOME")
		if home == "" {
			home = os.Getenv("USERPROFILE")
		}
		pwd = formatCwdForFooter(d.cwd, home)
	}
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
	// Upstream footer.ts uses session.getContextUsage() which returns the
	// LAST turn's token count (not cumulative). This reflects actual current
	// context window pressure. d.contextTokens is set per-turn in AddUsage.
	contextWindow := 0
	if d.model != nil {
		contextWindow = d.model.Capabilities.ContextWindow
	} else if d.projectedContextWindow > 0 {
		contextWindow = d.projectedContextWindow
	}
	tokens := d.contextTokens
	pct := 0.0
	if contextWindow > 0 {
		pct = float64(tokens) / float64(contextWindow) * 100
	}
	autoTag := ""
	if d.autoCompactEnabled {
		autoTag = " (auto)"
	}
	display := fmt.Sprintf("%.1f%%/%s%s", pct, formatTokens(contextWindow), autoTag)
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
	// The upstream Agent supplies its "unknown" default model when no model is
	// selected, so the footer still renders a stable model identity.
	modelName := "unknown"
	modelProvider := ""
	modelReasoning := false
	if d.model != nil {
		modelName = d.model.ID
		if d.model.Provider != nil {
			modelProvider = d.model.Provider.ID()
		}
		modelReasoning = d.model.Capabilities.MaxThinking != ""
	}

	rightSide := modelName
	if modelReasoning {
		level := d.thinkingLevel
		if level == "" {
			level = "off"
		}
		// Upstream footer.ts:160-162 renders the thinking level as plain
		// text: no per-level color. The entire right side is wrapped in
		// dim() with the rest of line 2, so it appears in dim grey.
		if level == "off" {
			rightSide = modelName + " \u2022 thinking " + level
		} else {
			rightSide = modelName + " \u2022 " + level
		}
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
			line2 = dim(statsLeft)
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

func renderExtensionStatuses(statuses map[string]string, width int) (string, bool) {
	if len(statuses) == 0 {
		return "", false
	}
	keys := slices.Sorted(maps.Keys(statuses))
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		text := strings.Join(strings.Fields(statuses[key]), " ")
		if text != "" {
			parts = append(parts, text)
		}
	}
	if len(parts) == 0 {
		return "", false
	}
	return widthx.TruncateToWidth(strings.Join(parts, " "), width, dim("..."), false), true
}

// colorContextPercent renders just the percent number with the same
// thresholds. Retained for unit-test compatibility; the live footer
// path uses colorContextDisplay which wraps the full pct/window string
// to match upstream footer.ts.
//
//	<=70%  no color (plain)
//	>70%   yellow (warning)
//	>90%   red (error)
//
// Mirrors upstream footer.ts:143-150.
func colorContextPercent(pct float64) string {
	body := fmt.Sprintf("%.1f%%", pct)
	return applyContextColor(pct, body)
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
		return ansi(31, body) // red
	case pct > 70:
		return ansi(33, body) // yellow
	default:
		return body // no color
	}
}

// formatTokens renders an int token count as "1.2k", "230", "8.4M".
// Mirrors upstream footer.ts:21-26.
func formatTokens(n int) string {
	switch {
	case n < 1000:
		return fmt.Sprintf("%d", n)
	case n < 10_000:
		return fmt.Sprintf("%.1fk", float64(n)/1_000)
	case n < 1_000_000:
		return fmt.Sprintf("%dk", n/1_000)
	case n < 10_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	default:
		return fmt.Sprintf("%dM", n/1_000_000)
	}
}

// formatDuration renders a duration as "1.2s" / "3m12s" / "0.0s".
func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	mins := int(d / time.Minute)
	secs := int((d % time.Minute) / time.Second)
	return fmt.Sprintf("%dm%02ds", mins, secs)
}

// dim wraps text in the theme's dim foreground color and fg-only reset.
// Mirrors upstream footer.ts: theme.fg("dim", text) which emits the
// theme's resolved dim hex color (e.g. \x1b[38;2;102;102;102m for dark)
// followed by \x1b[39m (fg-only reset). Using theme colors instead of
// SGR dim (\x1b[2m) ensures byte-identical ANSI output with upstream.
func dim(s string) string {
	th := tui.ActiveTheme()
	return th.Dim + s + "\x1b[39m"
}

// boldWarning renders text in bold with the theme's warning foreground,
// mirroring upstream footer.ts theme.bold(theme.fg("warning", text)): chalk.bold
// wraps \x1b[1m..\x1b[22m around theme.fg's <warning>text\x1b[39m, so the bytes
// are \x1b[1m<warning>text\x1b[39m\x1b[22m.
func boldWarning(s string) string {
	th := tui.ActiveTheme()
	return "\x1b[1m" + th.Warning + s + "\x1b[39m" + tui.SGRBoldDimReset
}

func ansi(code int, body string) string {
	return fmt.Sprintf("\033[%dm%s\033[0m", code, body)
}

// stripANSI removes ANSI escape sequences (delegates to widthx.StripAnsi).
func stripANSI(s string) string { return widthx.StripAnsi(s) }

// resolveGitBranch returns the current git branch name, "detached" for a
// detached HEAD, or "" outside a repository. Mirrors upstream
// footer-data-provider.ts resolveGitBranchSync: it reads HEAD directly and runs
// git only for a reftable HEAD, whose placeholder ref names no branch.
func resolveGitBranch(cwd string) string {
	if cwd == "" {
		return ""
	}
	paths, ok := findGitPaths(cwd)
	if !ok {
		return ""
	}
	content, err := os.ReadFile(paths.headPath)
	if err != nil {
		return ""
	}
	branch, ok := strings.CutPrefix(strings.TrimSpace(string(content)), "ref: refs/heads/")
	if !ok {
		return "detached"
	}
	if branch == ".invalid" {
		if resolved := resolveBranchWithGit(paths.repoDir); resolved != "" {
			return resolved
		}
		return "detached"
	}
	return branch
}

// resolveBranchWithGit asks git for the current branch. It returns "" on a
// detached HEAD or when git is unavailable. Mirrors upstream
// resolveBranchWithGitSync; tests replace it to observe process spawns.
var resolveBranchWithGit = func(repoDir string) string {
	cmd := exec.Command("git", "--no-optional-locks", "symbolic-ref", "--quiet", "--short", "HEAD")
	cmd.Dir = repoDir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// SetContextUsage stores the Session projection estimate outside the render path.
// Nil tokens indicate unknown usage after compaction until a valid response.
func (s *StatusLine) SetContextUsage(tokens *int, contextWindow int) {
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
