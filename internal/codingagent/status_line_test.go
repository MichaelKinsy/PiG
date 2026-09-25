package codingagent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui"
)

// Footer must render the bare model id stored in ai.Model.ID (e.g.
// "gpt-4o"). Upstream footer.ts:134 uses `state.model?.id` which is
// always the bare catalog id. cmd/pig/model.go now stores the bare id
// at the source (changed from FQ spec in the footer-tighten loop,
// 2026-05-15).
func TestRenderFooterBareModelID(t *testing.T) {
	prov := ai.NewFauxProvider(ai.FauxConfig{ProviderID: "github-copilot"})
	model := &ai.Model{
		ID:           "gpt-4o", // bare id, matching upstream storage
		DisplayName:  "GPT-4o",
		Provider:     prov,
		Capabilities: ai.ModelCapabilities{ContextWindow: 128_000},
	}
	lines := renderFooter(footerData{model: model}, 120)
	line2 := stripANSI(lines[1])
	if !strings.Contains(line2, "gpt-4o") {
		t.Errorf("line2 missing bare model id: %q", line2)
	}
	// Regression guard: FQ spec must never appear in the footer.
	if strings.Contains(line2, "github-copilot/gpt-4o") {
		t.Errorf("line2 must not contain FQ spec (upstream renders bare id): %q", line2)
	}
	if strings.Contains(line2, "GPT-4o") {
		t.Errorf("line2 must not contain DisplayName: %q", line2)
	}
}

func TestExperimentalFeaturesEnabled(t *testing.T) {
	cases := []struct {
		pi, pig string
		want    bool
	}{
		{"1", "", true},     // PI_EXPERIMENTAL strict "1", mirrors upstream
		{"true", "", false}, // PI_ is strict === "1"; "true" does not enable
		{"0", "", false},
		{"", "1", true},    // PIG_ alias
		{"", "true", true}, // PIG_ alias accepts truthy spellings
		{"", "yes", true},
		{"", " TRUE ", true}, // trimmed + case-insensitive
		{"", "0", false},
		{"", "", false},
	}
	for _, c := range cases {
		t.Setenv("PI_EXPERIMENTAL", c.pi)
		t.Setenv("PIG_EXPERIMENTAL", c.pig)
		if got := experimentalFeaturesEnabled(); got != c.want {
			t.Errorf("PI_EXPERIMENTAL=%q PIG_EXPERIMENTAL=%q → %v, want %v", c.pi, c.pig, got, c.want)
		}
	}
}

func TestRenderFooterExperimentalIndicator(t *testing.T) {
	prov := ai.NewFauxProvider(ai.FauxConfig{ProviderID: "openai"})
	model := &ai.Model{
		ID:           "gpt-4o",
		Provider:     prov,
		Capabilities: ai.ModelCapabilities{ContextWindow: 128_000},
	}

	// Off by default: upstream footer.ts gates the badge on PI_EXPERIMENTAL=1.
	off := stripANSI(renderFooter(footerData{model: model}, 120)[1])
	if strings.Contains(off, "• xp") {
		t.Fatalf("experimental badge shown without PI_EXPERIMENTAL: %q", off)
	}

	// PI_EXPERIMENTAL=1: footer.ts:162-164 pushes a dim "•" separator plus a
	// bold warning "xp" badge.
	t.Setenv("PI_EXPERIMENTAL", "1")
	line2 := renderFooter(footerData{model: model}, 120)[1]
	if vis := stripANSI(line2); !strings.Contains(vis, "• xp") {
		t.Fatalf("experimental badge missing with PI_EXPERIMENTAL=1: %q", vis)
	}
	// Byte layout: chalk.bold(theme.fg("warning","xp")) = \x1b[1m<warn>xp\x1b[39m\x1b[22m.
	wantBadge := "\x1b[1m" + tui.ActiveTheme().Warning + "xp\x1b[39m\x1b[22m"
	if !strings.Contains(line2, wantBadge) {
		t.Fatalf("experimental badge bytes wrong: want %q in %q", wantBadge, line2)
	}
}

func TestAC53CustomFooterOwnsKeyedStatusRendering(t *testing.T) {
	status := NewStatusLine(nil, "", nil)
	status.SetExtensionStatus("zeta", "second")
	status.SetExtensionStatus("alpha", "first")
	status.SetSuppressedByExtFooter(true)

	// A custom footer receives the current statuses through FooterData and owns
	// their presentation, matching upstream. The host must not render a second
	// copy below it.
	suppressed := status.Render(80)
	if len(suppressed) != 0 {
		t.Fatalf("suppressed standard footer lines = %#v, want none", suppressed)
	}

	status.SetSuppressedByExtFooter(false)
	lines := status.Render(80)
	if len(lines) != 3 || stripANSI(lines[2]) != "first second" {
		t.Fatalf("restored standard footer lines = %#v", lines)
	}
}

// When model.ID is a bare id (no provider prefix), footer renders it verbatim.
// This is the standard case since cmd/pig/model.go stores bare IDs.
func TestRenderFooterBareIDVerbatim(t *testing.T) {
	prov := ai.NewFauxProvider(ai.FauxConfig{ProviderID: "openai"})
	model := &ai.Model{
		ID:           "gpt-4o",
		DisplayName:  "GPT-4o",
		Provider:     prov,
		Capabilities: ai.ModelCapabilities{ContextWindow: 128_000},
	}
	lines := renderFooter(footerData{model: model}, 120)
	line2 := stripANSI(lines[1])
	if !strings.Contains(line2, "gpt-4o") {
		t.Errorf("line2 missing model id: %q", line2)
	}
}

func TestRenderFooterUnknownModelMatchesAgentDefault(t *testing.T) {
	line2 := stripANSI(renderFooter(footerData{autoCompactEnabled: true}, 120)[1])
	for _, want := range []string{"0.0%/0 (auto)", "unknown"} {
		if !strings.Contains(line2, want) {
			t.Fatalf("no-model footer %q missing %q", line2, want)
		}
	}
}

func TestFormatTokens(t *testing.T) {
	cases := []struct {
		in   int
		want string
	}{
		{0, "0"},
		{42, "42"},
		{999, "999"},
		{1000, "1.0k"},
		{8200, "8.2k"},
		{10_000, "10k"},
		{200_000, "200k"},
		{1_500_000, "1.5M"},
		{12_000_000, "12M"},
	}
	for _, tc := range cases {
		if got := formatTokens(tc.in); got != tc.want {
			t.Errorf("formatTokens(%d)=%q want %q", tc.in, got, tc.want)
		}
	}
}

func TestFormatDuration(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want string
	}{
		{0, "0.0s"},
		{1200 * time.Millisecond, "1.2s"},
		{59*time.Second + 900*time.Millisecond, "59.9s"},
		{61 * time.Second, "1m01s"},
		{4*time.Minute + 5*time.Second, "4m05s"},
	}
	for _, tc := range cases {
		if got := formatDuration(tc.in); got != tc.want {
			t.Errorf("formatDuration(%v)=%q want %q", tc.in, got, tc.want)
		}
	}
}

func TestColorContextPercentBands(t *testing.T) {
	// <=70% = no color (plain)
	p10 := colorContextPercent(10)
	if strings.Contains(p10, "\033[") {
		t.Errorf("<=70%% should have no color: %q", p10)
	}
	// >70% = yellow
	if !strings.Contains(colorContextPercent(75), "\033[33m") {
		t.Error(">70%% should be yellow (33m)")
	}
	// >90% = red
	if !strings.Contains(colorContextPercent(95), "\033[31m") {
		t.Error(">90%% should be red (31m)")
	}
}

// footer renders as 2 lines matching upstream format.
func TestRenderFooterTwoLines(t *testing.T) {
	model := &ai.Model{
		ID:          "gpt-4o",
		DisplayName: "GPT-4o",
		Capabilities: ai.ModelCapabilities{
			ContextWindow:   128_000,
			InputCostPer1M:  2.5,
			OutputCostPer1M: 10.0,
		},
	}
	rec := agent.NewRecorder()
	rec.StartTurn()
	time.Sleep(2 * time.Millisecond)

	lines := renderFooter(footerData{
		model:              model,
		agentName:          "worker",
		cwd:                "/home/user/project",
		gitBranch:          "main",
		usage:              footerUsageTotals{input: 4000, output: 1000, cost: 0.02},
		timings:            rec,
		working:            true,
		autoCompactEnabled: true,
	}, 120)

	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2", len(lines))
	}

	line1 := stripANSI(lines[0])
	line2 := stripANSI(lines[1])

	// Line 1: pwd (branch)
	if !strings.Contains(line1, "/home/user/project") {
		t.Errorf("line1 missing cwd: %q", line1)
	}
	if !strings.Contains(line1, "(main)") {
		t.Errorf("line1 missing branch: %q", line1)
	}

	// Line 2: token stats + model
	if !strings.Contains(line2, "\u21914.0k") {
		t.Errorf("line2 missing input tokens: %q", line2)
	}
	if !strings.Contains(line2, "\u21931.0k") {
		t.Errorf("line2 missing output tokens: %q", line2)
	}
	if !strings.Contains(line2, "$") {
		t.Errorf("line2 missing cost: %q", line2)
	}
	if !strings.Contains(line2, "(auto)") {
		t.Errorf("line2 missing auto-compact indicator: %q", line2)
	}
	// Upstream renders model.id (lowercase), not DisplayName.
	if !strings.Contains(line2, "gpt-4o") {
		t.Errorf("line2 missing model id: %q", line2)
	}
	if strings.Contains(line2, "GPT-4o") {
		t.Errorf("line2 should not contain DisplayName (upstream uses id): %q", line2)
	}
}

// Upstream footer.ts:141-145 gates the cost segment on `totalCost || usingSubscription`.
// A priced model with no accrued cost and no subscription shows NO cost segment, not
// "$0.000". pig previously gated on "model has a price" and always printed the computed
// cost, so an idle priced model (e.g. github-copilot gpt-4.1 at session start) rendered
// "$0.000" while real pi renders nothing. Pre-existing divergence surfaced by parity
// footer/03 during the 0.79.10 sync (guard identical in v0.79.4/v0.79.10/v0.80.2).
func TestRenderFooterOmitsZeroCostWithoutSubscription(t *testing.T) {
	model := &ai.Model{
		ID: "gpt-4.1",
		Capabilities: ai.ModelCapabilities{
			ContextWindow:   128_000,
			InputCostPer1M:  2.0,
			OutputCostPer1M: 8.0,
		},
	}
	line2 := stripANSI(renderFooter(footerData{model: model}, 120)[1])
	if strings.Contains(line2, "$") {
		t.Errorf("idle priced model must render no cost segment (upstream hides $0.000); got %q", line2)
	}
}

// The subscription arm of the same guard is preserved: an OAuth subscription
// provider still shows "$0.000 (sub)" at zero accrued cost.
func TestRenderFooterShowsZeroCostWithSubscription(t *testing.T) {
	model := &ai.Model{
		ID:           "gpt-4.1",
		Capabilities: ai.ModelCapabilities{ContextWindow: 128_000, InputCostPer1M: 2.0},
	}
	line2 := stripANSI(renderFooter(footerData{model: model, usingSubscription: true}, 120)[1])
	if !strings.Contains(line2, "$0.000 (sub)") {
		t.Errorf("subscription provider must show $0.000 (sub) at zero cost; got %q", line2)
	}
}

func TestRenderFooterIdleOmitsLastTurnDuration(t *testing.T) {
	model := &ai.Model{
		ID:          "gpt-4o",
		DisplayName: "GPT-4o",
		Capabilities: ai.ModelCapabilities{
			ContextWindow: 128_000,
		},
	}
	rec := agent.NewRecorder()
	rec.StartTurn()
	time.Sleep(2 * time.Millisecond)
	rec.EndTurn()

	lines := renderFooter(footerData{
		model:              model,
		timings:            rec,
		working:            false,
		autoCompactEnabled: true,
	}, 120)

	line2 := stripANSI(lines[1])
	if strings.Contains(line2, "0.0s") || strings.Contains(line2, "0.1s") {
		t.Fatalf("idle footer leaked last-turn duration: %q", line2)
	}
}

// Session name appears in line 1 with bullet separator.
func TestRenderFooterSessionName(t *testing.T) {
	lines := renderFooter(footerData{
		cwd:         "/home/user",
		sessionName: "auth refactor",
	}, 120)
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2", len(lines))
	}
	line1 := stripANSI(lines[0])
	if !strings.Contains(line1, "auth refactor") {
		t.Errorf("session name missing: %q", line1)
	}
	if !strings.Contains(line1, "\u2022") {
		t.Errorf("bullet separator missing: %q", line1)
	}
}

// No session name = no bullet.
func TestRenderFooterNoSessionName(t *testing.T) {
	lines := renderFooter(footerData{cwd: "/tmp"}, 120)
	line1 := stripANSI(lines[0])
	if strings.Contains(line1, "\u2022") {
		t.Errorf("bullet should not appear without session name: %q", line1)
	}
}

// Thinking level shown on line 2 when model supports reasoning.
func TestRenderFooterThinkingLevel(t *testing.T) {
	model := &ai.Model{
		ID:          "claude-sonnet-4",
		DisplayName: "claude-sonnet-4",
		Capabilities: ai.ModelCapabilities{
			ContextWindow: 200_000,
			MaxThinking:   ai.ThinkingHigh,
		},
	}
	lines := renderFooter(footerData{
		model:         model,
		thinkingLevel: "medium",
	}, 120)
	line2 := stripANSI(lines[1])
	if !strings.Contains(line2, "claude-sonnet-4") {
		t.Errorf("line2 missing model: %q", line2)
	}
	if !strings.Contains(line2, "medium") {
		t.Errorf("line2 missing thinking level: %q", line2)
	}
}

// Auto-compact disabled hides the "(auto)" tag.
func TestRenderFooterAutoCompactDisabled(t *testing.T) {
	model := &ai.Model{
		ID:           "gpt-4o",
		DisplayName:  "gpt-4o",
		Capabilities: ai.ModelCapabilities{ContextWindow: 128_000},
	}
	lines := renderFooter(footerData{
		model:              model,
		usage:              footerUsageTotals{input: 1000},
		autoCompactEnabled: false,
	}, 120)
	line2 := stripANSI(lines[1])
	if strings.Contains(line2, "(auto)") {
		t.Errorf("(auto) should be hidden when disabled: %q", line2)
	}
}

func TestStatusLineContextUsageKeepsLastTurn(t *testing.T) {
	s := NewStatusLine(nil, "", nil)
	s.SetTurnContextUsage(&ai.Usage{Input: 100, Output: 50, CacheRead: 10, CacheWrite: 5})
	s.SetTurnContextUsage(&ai.Usage{Input: 200, Output: 75, CacheRead: 20, CacheWrite: 15})
	// contextTokens is the LAST turn's total, not cumulative.
	want := 200 + 75 + 20 + 15
	if s.contextTokens != want {
		t.Fatalf("contextTokens = %d, want %d (last turn only)", s.contextTokens, want)
	}
}

// Cache read/write tokens appear when non-zero.
func TestRenderFooterCacheTokens(t *testing.T) {
	model := &ai.Model{
		ID:           "claude-sonnet-4",
		DisplayName:  "claude-sonnet-4",
		Capabilities: ai.ModelCapabilities{ContextWindow: 200_000},
	}
	lines := renderFooter(footerData{
		model:         model,
		usage:         footerUsageTotals{input: 5000, output: 1000, cacheRead: 3000, cacheWrite: 500},
		contextTokens: 6000, // 5000+1000 for this turn
	}, 120)
	line2 := stripANSI(lines[1])
	if !strings.Contains(line2, "R3.0k") {
		t.Errorf("line2 missing cache read: %q", line2)
	}
	if !strings.Contains(line2, "W500") {
		t.Errorf("line2 missing cache write: %q", line2)
	}
}

// Context percentage uses last-turn tokens, not cumulative totals.
// Upstream footer.ts calls session.getContextUsage() which returns the last
// assistant turn's total tokens / contextWindow.
func TestRenderFooterContextPercentUsesPerTurnTokens(t *testing.T) {
	model := &ai.Model{
		ID:           "claude-opus-4-6",
		DisplayName:  "claude-opus-4-6",
		Capabilities: ai.ModelCapabilities{ContextWindow: 1_000_000},
	}
	// Simulate: cumulative totalIn=1M (many turns), but last turn only used 50K.
	lines := renderFooter(footerData{
		model:         model,
		usage:         footerUsageTotals{input: 1_000_000, output: 10_000, cacheRead: 10_000},
		contextTokens: 50_000, // last turn only
	}, 120)
	line2 := stripANSI(lines[1])
	// Context should show ~5.0% (50K/1M), NOT 101% (1.01M/1M)
	if strings.Contains(line2, "101") || strings.Contains(line2, "100") {
		t.Errorf("context%% uses cumulative instead of per-turn: %q", line2)
	}
	if !strings.Contains(line2, "5.0%") {
		t.Errorf("expected 5.0%% context usage, got: %q", line2)
	}
}

// dim() must use the theme's truecolor escape, not SGR dim (\x1b[2m).
// Upstream footer.ts uses theme.fg("dim", text) which emits the resolved
// dim hex color. For the dark theme this is #666666 = \x1b[38;2;102;102;102m.
func TestDimUsesThemeTruecolor(t *testing.T) {
	previousCapabilities := tui.GetCapabilities()
	t.Cleanup(func() {
		tui.SetCapabilities(previousCapabilities)
		tui.RefreshActiveThemeColorMode()
	})
	tui.SetCapabilities(tui.TerminalCapabilities{TrueColor: true})
	tui.RefreshActiveThemeColorMode()
	got := dim("hello")
	if strings.Contains(got, "\x1b[2m") {
		t.Errorf("dim() must not use SGR dim code; got %q", got)
	}
	if !strings.Contains(got, "\x1b[38;2;") {
		t.Errorf("dim() must use truecolor escape; got %q", got)
	}
	// Must end with fg-only reset \x1b[39m, not full reset \x1b[0m
	if !strings.HasSuffix(got, "\x1b[39m") {
		t.Errorf("dim() must end with fg-only reset \\x1b[39m; got %q", got)
	}
}

// Multi-provider prefix: when providerCount > 1, footer shows "(provider) model".
// Mirrors upstream footer.ts:165-170.
func TestRenderFooterMultiProvider(t *testing.T) {
	prov := ai.NewFauxProvider(ai.FauxConfig{ProviderID: "github-copilot"})
	model := &ai.Model{
		ID:           "gpt-4o",
		DisplayName:  "GPT-4o",
		Provider:     prov,
		Capabilities: ai.ModelCapabilities{ContextWindow: 128_000},
	}
	lines := renderFooter(footerData{
		model:              model,
		providerCount:      2,
		usingSubscription:  true,
		autoCompactEnabled: true,
	}, 120)
	line2 := stripANSI(lines[1])
	if !strings.Contains(line2, "(github-copilot) gpt-4o") {
		t.Errorf("line2 missing multi-provider prefix: %q", line2)
	}
}

// Single provider: no prefix.
func TestRenderFooterSingleProvider(t *testing.T) {
	prov := ai.NewFauxProvider(ai.FauxConfig{ProviderID: "github-copilot"})
	model := &ai.Model{
		ID:           "gpt-4o",
		DisplayName:  "GPT-4o",
		Provider:     prov,
		Capabilities: ai.ModelCapabilities{ContextWindow: 128_000},
	}
	lines := renderFooter(footerData{
		model:              model,
		providerCount:      1,
		usingSubscription:  true,
		autoCompactEnabled: true,
	}, 120)
	line2 := stripANSI(lines[1])
	if strings.Contains(line2, "(github-copilot)") {
		t.Errorf("line2 should not have provider prefix with providerCount=1: %q", line2)
	}
	if !strings.Contains(line2, "gpt-4o") {
		t.Errorf("line2 missing model id: %q", line2)
	}
}

func TestStatusLineProjectedContextUnknownAndRecovered(t *testing.T) {
	model := &ai.Model{Capabilities: ai.ModelCapabilities{ContextWindow: 128000}}
	status := NewStatusLine(model, "", nil)
	status.SetContextUsage(nil, 128000)
	unknown := strings.Join(status.Render(120), "\n")
	if !strings.Contains(unknown, "?/128k") || strings.Contains(unknown, "0.0%/128k") {
		t.Fatalf("unknown context %q", unknown)
	}
	high := 127000
	status.SetContextUsage(&high, 128000)
	status.SetContextUsage(nil, 128000)
	if got := strings.Join(status.Render(120), "\n"); got != unknown {
		t.Fatalf("unknown context retained pressure color: %q", got)
	}
	tokens := 64000
	status.SetContextUsage(&tokens, 128000)
	known := strings.Join(status.Render(120), "\n")
	if !strings.Contains(known, "50.0%/128k") || strings.Contains(known, "?/128k") {
		t.Fatalf("recovered context %q", known)
	}
}

func TestStatusLineModelSwitchUsesCurrentContextWindow(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		status := NewStatusLine(&ai.Model{Capabilities: ai.ModelCapabilities{ContextWindow: 128000}}, "", nil)
		tokens := 64000
		if unknown {
			status.SetContextUsage(nil, 128000)
		} else {
			status.SetContextUsage(&tokens, 128000)
		}
		status.SetModel(&ai.Model{Capabilities: ai.ModelCapabilities{ContextWindow: 256000}})
		want := "25.0%/256k"
		if unknown {
			want = "?/256k"
		}
		if got := strings.Join(status.Render(120), "\n"); !strings.Contains(got, want) {
			t.Fatalf("model switch footer=%q want %q", got, want)
		}
	}
}

// Resolving the footer branch leaves no handle open on the repository or its
// .git directory, so the directory can still be renamed and removed while pig
// runs. Windows refuses both while any handle inside is open.
func TestStatusLineHoldsNoHandleOnTheRepository(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".git", "HEAD"), []byte("ref: refs/heads/trunk\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	status := NewStatusLine(nil, "", nil)
	status.SetCwd(repo)
	if got := status.gitBranch; got != "trunk" {
		t.Fatalf("branch = %q, want trunk", got)
	}
	renamed := repo + "-renamed"
	if err := os.Rename(repo, renamed); err != nil {
		t.Fatalf("rename the repository while its branch is shown: %v", err)
	}
	if err := os.RemoveAll(renamed); err != nil {
		t.Fatalf("remove the repository while its branch is shown: %v", err)
	}
}

// Ports footer-width.test.ts "formatCwdForFooter": the footer abbreviates the
// home directory and its descendants with "~" and the platform separator,
// never a sibling that only shares the home prefix. Upstream reads the home
// from HOME, then USERPROFILE (footer.ts), so a Windows shell that sets HOME
// (MSYS2, Git Bash) abbreviates relative to HOME, not the profile directory.
func TestRenderFooterAbbreviatesHomeLikeUpstream(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "user")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", filepath.Join(root, "profile"))
	sep := string(filepath.Separator)
	for _, tc := range []struct{ cwd, want string }{
		{filepath.Join(root, "user2"), filepath.Join(root, "user2")},
		{home, "~"},
		{filepath.Join(home, "project"), "~" + sep + "project"},
	} {
		line := stripANSI(renderFooter(footerData{cwd: tc.cwd}, 400)[0])
		if strings.TrimSpace(line) != tc.want {
			t.Errorf("footer pwd for %q = %q, want %q", tc.cwd, strings.TrimSpace(line), tc.want)
		}
	}

	// An unset or empty HOME falls back to USERPROFILE, as `||` does.
	t.Setenv("HOME", "")
	profile := filepath.Join(root, "profile")
	if line := stripANSI(renderFooter(footerData{cwd: filepath.Join(profile, "x")}, 400)[0]); strings.TrimSpace(line) != "~"+sep+"x" {
		t.Errorf("footer pwd with empty HOME = %q, want ~%sx", strings.TrimSpace(line), sep)
	}

	// With no session cwd the footer shows ".", which is not resolved against
	// the process's working directory, even when that is inside home.
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", filepath.Dir(wd))
	if line := stripANSI(renderFooter(footerData{}, 400)[0]); strings.TrimSpace(line) != "." {
		t.Errorf("footer pwd without a cwd = %q, want .", strings.TrimSpace(line))
	}
}

// Upstream footer.ts truncates the pwd and extension status lines with
// truncateToWidth, which clips the "..." ellipsis itself when the width is
// narrower than it. A long cwd at width 1 or 2 therefore renders "." or "..",
// never a 3-cell "..." wider than the terminal.
func TestRenderFooterClipsTheEllipsisAtTinyWidths(t *testing.T) {
	cwd := filepath.Join(t.TempDir(), "a-long-project-directory")
	statuses := map[string]string{"ext": "a long extension status"}
	for width, want := range map[int]string{1: ".", 2: "..", 3: "..."} {
		lines := renderFooter(footerData{cwd: cwd, extensionStatuses: statuses}, width)
		if got := stripANSI(lines[0]); got != want {
			t.Errorf("width %d: pwd line = %q, want %q", width, got, want)
		}
		if got := stripANSI(lines[len(lines)-1]); got != want {
			t.Errorf("width %d: status line = %q, want %q", width, got, want)
		}
	}
}
