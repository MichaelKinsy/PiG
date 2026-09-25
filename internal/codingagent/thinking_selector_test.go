package codingagent

import (
	"slices"
	"strings"
	"testing"

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
// browsing" (coding-agent test/thinking-selector.test.ts).
func TestThinkingSelectorKeepsCurrentLevelMarkedWhileBrowsing(t *testing.T) {
	useKeybindings(t, nil)
	selector := NewThinkingSelectorComponent("medium", []string{"medium", "high"}, func(string) {}, func() {}, nil, "")
	levelRow := func(level string) string {
		for _, line := range selector.GetSelectList().Render(80) {
			if plain := stripANSI(line); strings.Contains(plain, level) {
				return plain
			}
		}
		return ""
	}
	if item, _ := selector.selectedItem(); item.Label != "✓ medium" {
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
	var saved []string
	selector := NewThinkingSelectorComponent("medium", []string{"medium", "high"}, func(string) {}, func() {},
		func(level string) { saved = append(saved, level) }, "")

	if rendered := stripANSI(strings.Join(selector.Render(80), "\n")); !strings.Contains(rendered, "Ctrl+R to set as default") {
		t.Fatalf("render lacks the configured save hint:\n%s", rendered)
	}
	selector.HandleInput("\x13") // ctrl+s, the default save key, is not bound now
	if len(saved) != 0 {
		t.Fatalf("ctrl+s saved %v after app.thinking.save moved to ctrl+r", saved)
	}
	selector.HandleInput("\x12") // ctrl+r
	if !slices.Equal(saved, []string{"medium"}) {
		t.Fatalf("saved = %v, want [medium]", saved)
	}
}

func TestThinkingSelectorSearchSelectAndCancel(t *testing.T) {
	useKeybindings(t, nil)
	var selected string
	cancelled := false
	selector := NewThinkingSelectorComponent("off", []string{"off", "low", "high"},
		func(level string) { selected = level }, func() { cancelled = true }, nil, "low")

	rendered := stripANSI(strings.Join(selector.Render(80), "\n"))
	for _, want := range []string{"Thinking Level", "Shift+Tab cycles thinking levels in-session", "Light reasoning (~2k tokens) · default", "Enter to select · Ctrl+S to set as default · Escape/Ctrl+C to cancel"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("render lacks %q:\n%s", want, rendered)
		}
	}
	for _, r := range "deep" {
		selector.HandleInput(string(r))
	}
	if item, ok := selector.selectedItem(); !ok || item.Value != "high" {
		t.Fatalf("search for deep selects %#v, want high", item)
	}
	selector.HandleInput("\r")
	if selected != "high" {
		t.Fatalf("Enter selected %q, want high", selected)
	}

	selector = NewThinkingSelectorComponent("off", []string{"off", "low"}, func(string) {}, func() { cancelled = true }, nil, "")
	selector.HandleInput("\x1b")
	if !cancelled {
		t.Fatal("Escape did not cancel")
	}
}

func newThinkingTestMode(t *testing.T) *InteractiveMode {
	t.Helper()
	dir := t.TempDir()
	return &InteractiveMode{
		agent:         agent.NewAgent(agent.AgentOptions{}),
		editor:        tui.NewEditor(),
		chatContainer: tui.NewContainer(),
		thinkingLevel: "medium",
		opts: InteractiveOptions{
			Model:           &ai.Model{Capabilities: ai.ModelCapabilities{MaxThinking: ai.ThinkingHigh}},
			SettingsManager: NewSettingsManager(dir, dir),
		},
	}
}

// Since upstream 0.86, selecting or cycling a thinking level stays in the
// session; only app.thinking.save writes the global default.
func TestThinkingLevelPersistsOnlyWhenSaved(t *testing.T) {
	m := newThinkingTestMode(t)
	m.cycleThinkingLevel()
	if m.thinkingLevel != "high" {
		t.Fatalf("cycle: level = %q, want high", m.thinkingLevel)
	}
	m.selectThinkingLevel("low", false)
	if got := m.opts.SettingsManager.Get().DefaultThinkingLevel; got != "" {
		t.Fatalf("cycling and selecting persisted default %q", got)
	}
	if m.agent.ThinkingLevel() != ai.ThinkingLow {
		t.Fatalf("agent level = %q, want low", m.agent.ThinkingLevel())
	}

	m.selectThinkingLevel("minimal", true)
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
