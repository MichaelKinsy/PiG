package codingagent

// pi: packages/coding-agent/src/modes/interactive/components/thinking-selector.ts

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui"
)

// useKeybindings installs a coding-agent manager with the given overrides as
// the TUI registry, as upstream's setKeybindings(new KeybindingsManager(...)),
// and restores the previous registry afterwards.
func useKeybindings(t *testing.T, bindings map[string][]KeyID) {
	t.Helper()
	previous := tui.GetTUIKeybindings()
	t.Cleanup(func() { tui.SetTUIKeybindings(previous) })
	km := DefaultKeybindingsManager()
	km.SetUserBindings(bindings)
	km.syncToTUI()
}

// Ports "thinking selector › keeps the current thinking level marked while
// browsing" (coding-agent test/thinking-selector.test.ts). The rows are read through getSelectList
// (packages/coding-agent/src/modes/interactive/components/thinking-selector.ts:151).
func TestThinkingSelectorKeepsCurrentLevelMarkedWhileBrowsing(t *testing.T) {
	useKeybindings(t, nil)
	selector := NewThinkingSelectorComponent("medium", []ai.ThinkingLevel{"medium", "high"}, func(ai.ThinkingLevel) {}, func() {}, nil, "")
	levelRow := func(level string) string {
		for _, line := range selector.GetSelectList().Render(80) {
			if plain := stripANSI(line); strings.Contains(plain, level) {
				return plain
			}
		}
		return ""
	}
	if item, _ := selector.GetSelectList().SelectedItem(); item.Label != "✓ medium" {
		t.Fatalf("selected label = %q, want ✓ medium", item.Label)
	}
	if row := levelRow("medium"); !strings.HasPrefix(row, "→ ✓ medium") {
		t.Fatalf("medium row = %q, want it selected and checked", row)
	}
	selector.HandleInput("\x1b[B")
	if row := levelRow("medium"); !strings.HasPrefix(row, "  ✓ medium") {
		t.Fatalf("medium row after down = %q, want it checked but not selected", row)
	}
	if row := levelRow("high"); !strings.HasPrefix(row, "→   high") {
		t.Fatalf("high row after down = %q, want it selected", row)
	}
}

// Ports "thinking selector › uses the configured save binding".
func TestThinkingSelectorUsesTheConfiguredSaveBinding(t *testing.T) {
	useKeybindings(t, map[string][]KeyID{"app.thinking.save": {"ctrl+r"}})
	var saved []ai.ThinkingLevel
	selector := NewThinkingSelectorComponent("medium", []ai.ThinkingLevel{"medium", "high"}, func(ai.ThinkingLevel) {}, func() {},
		func(level ai.ThinkingLevel) { saved = append(saved, level) }, "")

	if rendered := stripANSI(strings.Join(selector.Render(80), "\n")); !strings.Contains(rendered, "Ctrl+R to set as default") {
		t.Fatalf("render lacks the configured save hint:\n%s", rendered)
	}
	selector.HandleInput("\x13") // ctrl+s, the default save key, is not bound now
	if len(saved) != 0 {
		t.Fatalf("ctrl+s saved %v after app.thinking.save moved to ctrl+r", saved)
	}
	selector.HandleInput("\x12") // ctrl+r
	if !slices.Equal(saved, []ai.ThinkingLevel{"medium"}) {
		t.Fatalf("saved = %v, want [medium]", saved)
	}
}

func TestThinkingSelectorSearchSelectAndCancel(t *testing.T) {
	useKeybindings(t, nil)
	var selected ai.ThinkingLevel
	cancelled := false
	selector := NewThinkingSelectorComponent("off", []ai.ThinkingLevel{"off", "low", "high"},
		func(level ai.ThinkingLevel) { selected = level }, func() { cancelled = true }, nil, "low")

	rendered := stripANSI(strings.Join(selector.Render(80), "\n"))
	for _, want := range []string{"Thinking Level", "Shift+Tab cycles thinking levels in-session", "Light reasoning (~2k tokens) · default", "Enter to select · Ctrl+S to set as default · Escape/Ctrl+C to cancel"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("render lacks %q:\n%s", want, rendered)
		}
	}
	for _, r := range "deep" {
		selector.HandleInput(string(r))
	}
	if item, ok := selector.GetSelectList().SelectedItem(); !ok || item.Value != "high" {
		t.Fatalf("search for deep selects %#v, want high", item)
	}
	selector.HandleInput("\r")
	if selected != "high" {
		t.Fatalf("Enter selected %q, want high", selected)
	}

	selector = NewThinkingSelectorComponent("off", []ai.ThinkingLevel{"off", "low"}, func(ai.ThinkingLevel) {}, func() { cancelled = true }, nil, "")
	selector.HandleInput("\x1b")
	if !cancelled {
		t.Fatal("Escape did not cancel")
	}
}

func newThinkingTestMode(t *testing.T) *InteractiveMode {
	t.Helper()
	dir := t.TempDir()
	settings := NewSettingsManager(dir, dir)
	model := &ai.Model{Capabilities: ai.ModelCapabilities{MaxThinking: ai.ThinkingLevelHigh}}
	thinking := mustNewAgent(agent.AgentOptions{Model: model})
	thinking.SetThinkingLevel(ai.ThinkingMedium) // the Session level the UI shows
	return &InteractiveMode{
		agent:         thinking,
		editor:        tui.NewEditor(),
		chatContainer: tui.NewContainer(),
		thinkingLevel: "medium",
		uiTaskCh:      make(chan func(), 64),
		opts: InteractiveModeOptions{
			Model:           model,
			SettingsManager: settings,
			SessionHandle:   &recordingCompactHandle{agent: thinking, thinkingSettings: settings},
		},
	}
}

// Since upstream 0.86, selecting or cycling a thinking level stays in the
// session; only app.thinking.save writes the global default.
func TestThinkingLevelPersistsOnlyWhenSaved(t *testing.T) {
	m := newThinkingTestMode(t)
	settle := startOwnerLoop(t, m)
	m.cycleThinkingLevel()
	settle()
	if m.thinkingLevel != "high" {
		t.Fatalf("cycle: level = %q, want high", m.thinkingLevel)
	}
	m.selectThinkingLevel("low", false)
	settle()
	if got := m.opts.SettingsManager.Get().DefaultThinkingLevel; got != "" {
		t.Fatalf("cycling and selecting persisted default %q", got)
	}
	if m.agent.ThinkingLevel() != ai.ThinkingLow {
		t.Fatalf("agent level = %q, want low", m.agent.ThinkingLevel())
	}

	m.selectThinkingLevel("minimal", true)
	settle()
	if got := m.opts.SettingsManager.Get().DefaultThinkingLevel; got != "minimal" {
		t.Fatalf("save: default = %q, want minimal", got)
	}
	if m.opts.Settings.DefaultThinkingLevel != "minimal" || m.thinkingLevel != "minimal" {
		t.Fatalf("save: settings %q, level %q; want minimal", m.opts.Settings.DefaultThinkingLevel, m.thinkingLevel)
	}
}

// /thinking without an argument opens the thinking selector, as upstream
// handleThinkingCommand calls showThinkingSelector.
func TestThinkingCommandOpensTheThinkingSelector(t *testing.T) {
	opened := false
	sc := &SlashContext{
		Append:                  func(string) {},
		AvailableThinkingLevels: func() []string { return []string{"off", "low"} },
		SelectThinkingLevel:     func(string) { t.Fatal("/thinking without an argument selected a level") },
		ShowSelectList: func(string, string, []tui.SelectItem, string) (string, bool) {
			t.Fatal("/thinking used the generic select list")
			return "", false
		},
		ShowThinkingSelector: func() { opened = true },
	}
	if err := thinkingHandler(sc); err != nil {
		t.Fatal(err)
	}
	if !opened {
		t.Fatal("/thinking did not open the thinking selector")
	}
}

// handleThinkingCommand (interactive-mode.ts:5155) reports `Unknown thinking level "${searchTerm}". Available levels: ...`: the term is
// interpolated as typed (trimmed with JavaScript's trim), so a quote, a backslash or a non-ASCII letter is not escaped.
func TestThinkingCommandUnknownLevelEchoesTheTermAsTyped(t *testing.T) {
	for _, tc := range []struct{ args, shown string }{
		{`ultra`, `ultra`}, {`a"b\c`, `a"b\c`}, {"naïve ✓", "naïve ✓"}, {"\ufeff\u00a0ultra\u2003", "ultra"}, {`a	b`, `a	b`},
	} {
		sc := &SlashContext{
			Args:                    tc.args,
			Append:                  func(string) {},
			AvailableThinkingLevels: func() []string { return []string{"off", "low"} },
			SelectThinkingLevel:     func(string) { t.Fatal("an unknown level was selected") },
		}
		err := thinkingHandler(sc)
		want := `Unknown thinking level "` + tc.shown + `". Available levels: off, low.`
		if err == nil || err.Error() != want {
			t.Errorf("/thinking %q: %v, want %q", tc.args, err, want)
		}
	}
}

// thinking-selector.ts:21-29 LEVEL_DESCRIPTIONS: every level carries its upstream description, and only the saved default gains the
// " · default" suffix (:62-65).
func TestThinkingSelectorDescribesEveryLevel(t *testing.T) {
	useKeybindings(t, nil)
	levels := []ai.ThinkingLevel{"off", "minimal", "low", "medium", "high", "xhigh", "max"}
	want := map[string]string{
		"off": "No reasoning", "minimal": "Very brief reasoning (~1k tokens)", "low": "Light reasoning (~2k tokens)",
		"medium": "Moderate reasoning (~8k tokens)", "high": "Deep reasoning (~16k tokens)",
		"xhigh": "Extra-high reasoning (~32k tokens)", "max": "Maximum reasoning",
	}
	selector := NewThinkingSelectorComponent("xhigh", levels, func(ai.ThinkingLevel) {}, func() {}, nil, "medium")
	rendered := stripANSI(strings.Join(selector.Render(100), "\n"))
	for _, level := range levels {
		description := want[string(level)]
		if level == "medium" {
			description += " · default"
		}
		found := false
		for line := range strings.SplitSeq(rendered, "\n") {
			if strings.Contains(line, " "+string(level)+" ") && strings.Contains(line, description) {
				found = true
			}
		}
		if !found {
			t.Errorf("no row for %q with %q:\n%s", level, description, rendered)
		}
	}
	if strings.Count(rendered, " · default") != 1 {
		t.Errorf("default suffix count = %d, want 1", strings.Count(rendered, " · default"))
	}
	if !strings.Contains(rendered, "✓ xhigh") || strings.Count(rendered, "✓") != 1 {
		t.Errorf("the current level is not the only checked row:\n%s", rendered)
	}
}

// thinking-selector.ts:114-122 applyFilter rebuilds the list with the previously highlighted level preselected when it survives the filter.
func TestThinkingSelectorKeepsTheHighlightedLevelWhileFiltering(t *testing.T) {
	useKeybindings(t, nil)
	selector := NewThinkingSelectorComponent("medium", []ai.ThinkingLevel{"off", "minimal", "low", "medium", "high"}, func(ai.ThinkingLevel) {}, func() {}, nil, "")
	if item, ok := selector.GetSelectList().SelectedItem(); !ok || item.Value != "medium" {
		t.Fatalf("initial selection = %#v", item)
	}
	selector.HandleInput("m")
	rendered := stripANSI(strings.Join(selector.GetSelectList().Render(80), "\n"))
	for _, kept := range []string{"minimal", "medium"} {
		if !strings.Contains(rendered, kept) {
			t.Fatalf("filtered list lost %s:\n%s", kept, rendered)
		}
	}
	for _, dropped := range []string{"off", "low", "high"} {
		if strings.Contains(rendered, " "+dropped+" ") {
			t.Fatalf("filtered list kept %s:\n%s", dropped, rendered)
		}
	}
	if item, ok := selector.GetSelectList().SelectedItem(); !ok || item.Value != "medium" {
		t.Fatalf("selection after filtering = %#v, want medium kept", item)
	}
}

// upstream: thinking-selector.ts handleInput (:105-124). With no level matching the query the save key and Enter reach no
// item, so neither callback runs; without an onSelectAsDefault the save key is not claimed.
func TestThinkingSelectorBoundaryTransitions(t *testing.T) {
	useKeybindings(t, nil)
	var saved, selected []ai.ThinkingLevel
	selector := NewThinkingSelectorComponent("off", []ai.ThinkingLevel{"off", "low"},
		func(level ai.ThinkingLevel) { selected = append(selected, level) }, func() {},
		func(level ai.ThinkingLevel) { saved = append(saved, level) }, "")
	for _, r := range "zzzz" {
		selector.HandleInput(string(r))
	}
	selector.HandleInput("\x13") // ctrl+s: the default save key
	selector.HandleInput("\r")
	if len(saved) != 0 || len(selected) != 0 {
		t.Fatalf("empty list: saved %v selected %v, want neither callback", saved, selected)
	}

	// Without onSelectAsDefault the save key is not claimed: it falls to the search input, which ignores a control
	// character, so nothing is selected, cancelled or typed.
	cancelled := false
	selector = NewThinkingSelectorComponent("off", []ai.ThinkingLevel{"off", "low"},
		func(level ai.ThinkingLevel) { selected = append(selected, level) }, func() { cancelled = true }, nil, "")
	selector.HandleInput("\x13")
	if len(selected) != 0 || cancelled || selector.searchInput.Text() != "" {
		t.Fatalf("save key without a default handler: selected %v cancelled %v query %q", selected, cancelled, selector.searchInput.Text())
	}
	// A printable save key shows that the key really reaches the search input (thinking-selector.ts:106 guards the save branch
	// with `&& this.onSelectAsDefault`): it is typed into the query instead of being swallowed.
	useKeybindings(t, map[string][]KeyID{"app.thinking.save": {"s"}})
	selector = NewThinkingSelectorComponent("off", []ai.ThinkingLevel{"off", "low"}, func(ai.ThinkingLevel) {}, func() {}, nil, "")
	selector.HandleInput("s")
	if got := selector.searchInput.Text(); got != "s" {
		t.Fatalf("printable save key without a default handler: query %q, want %q", got, "s")
	}
}

// startOwnerLoop runs the main loop's posted tasks on one goroutine, as inputLoop does, and returns a function that waits until every
// background request made so far has finished and the loop has run what it posted.
func startOwnerLoop(t *testing.T, m *InteractiveMode) (settle func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	m.runCtx = ctx
	loopDone := make(chan struct{})
	go m.drainLoop(ctx, loopDone)
	t.Cleanup(func() { cancel(); <-loopDone })
	return func() {
		t.Helper()
		m.backgroundTasks.Wait()
		reached := make(chan struct{})
		m.runOnMain(ctx, func() { close(reached) })
		<-reached
	}
}

// blockingThinkingHandle runs the state mutation on the owner loop and then holds the thinking_level_select notification until released, as an
// extension handler's synchronous prefix may.
type blockingThinkingHandle struct {
	*recordingCompactHandle
	order    chan ai.ModelThinkingLevel
	released chan struct{}
}

func (h *blockingThinkingHandle) SetThinkingLevelOnMain(level ai.ModelThinkingLevel, options ModelMutationOptions, dispatch func(func() error) error) error {
	if err := dispatch(func() error { return h.SetThinkingLevel(level, options) }); err != nil {
		return err
	}
	h.order <- level
	<-h.released
	return nil
}

// Pi's interactive paths call session.setThinkingLevel, whose thinking_level_select emission is not awaited (agent-session.ts:2629): a
// notification that is held by an extension handler must not stall the input loop, requests made meanwhile run after it in order, and the
// UI shows the level the Session settled on (clamped), not the one requested.
// mutation-checked: running the Session call on the loop goroutine stalls the probe task; unordered requests reorder the levels;
// showing the requested level fails the clamp assertion.
func TestSelectThinkingLevelDoesNotHoldTheInputLoopOnTheNotification(t *testing.T) {
	m := newThinkingTestMode(t)
	base := m.opts.SessionHandle.(*recordingCompactHandle)
	handle := &blockingThinkingHandle{recordingCompactHandle: base, order: make(chan ai.ModelThinkingLevel, 4), released: make(chan struct{})}
	m.opts.SessionHandle = handle
	settle := startOwnerLoop(t, m)

	m.selectThinkingLevel("low", false)
	if got := <-handle.order; got != ai.ThinkingLow {
		t.Fatalf("first request = %q", got)
	}
	m.selectThinkingLevel("xhigh", false) // the model supports up to high: the Session clamps it
	probe := make(chan struct{})
	m.runOnMain(m.runCtx, func() { close(probe) })
	select {
	case <-probe:
	case <-time.After(5 * time.Second):
		t.Fatal("the input loop was held by a thinking_level_select notification")
	}
	select {
	case got := <-handle.order:
		t.Fatalf("request %q ran before the earlier notification finished", got)
	case <-time.After(100 * time.Millisecond):
	}
	close(handle.released)
	if got := <-handle.order; got != ai.ThinkingXHigh {
		t.Fatalf("second request = %q", got)
	}
	settle()
	if m.thinkingLevel != "high" || m.agent.ThinkingLevel() != ai.ThinkingHigh {
		t.Fatalf("UI level %q, agent level %q; want the clamped high", m.thinkingLevel, m.agent.ThinkingLevel())
	}
}

// Pi's cycleThinkingLevel reads the Session's level when it runs (agent-session.ts cycleThinkingLevel), so two cycles made before the first
// settles advance twice: medium → high → off on a model whose highest level is high.
func TestCycleThinkingLevelTwiceBeforeSettlingAdvancesTwice(t *testing.T) {
	m := newThinkingTestMode(t)
	settle := startOwnerLoop(t, m)
	m.cycleThinkingLevel()
	m.cycleThinkingLevel()
	settle()
	if m.thinkingLevel != "off" || m.agent.ThinkingLevel() != ai.ThinkingOff {
		t.Fatalf("two cycles from medium: ui %q, agent %q; want off", m.thinkingLevel, m.agent.ThinkingLevel())
	}
}
