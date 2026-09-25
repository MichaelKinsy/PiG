package codingagent

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

func sessionSelectorInputBindings(t testing.TB) *KeybindingsManager {
	t.Helper()
	previous := tui.GetTUIKeybindings()
	t.Cleanup(func() { tui.SetKeybindings(previous) })
	bindings := &KeybindingsManager{
		definitions: appKeybindingDefinitions,
		ordered:     appKeybindingOrder,
		platform:    tui.HostKeybindingPlatform(),
	}
	bindings.rebuild()
	bindings.syncToTUI()
	return bindings
}

// Pi's SessionList owns a focused Input, rather than a truncated query string.
// Its cursor marker keeps teardown's space off the full-width bottom border.
func TestSessionSelectorSearchInputMatchesPi(t *testing.T) {
	for _, tc := range []struct {
		name, before, after string
		keys                []string
	}{
		{name: "empty"},
		{name: "ordinary", keys: []string{"abc"}, before: "abc"},
		{name: "edit-at-cursor", keys: []string{"abc", "\x1b[D", "X"}, before: "abX", after: "c"},
		{name: "unicode", keys: []string{"界🙂", "\x1b[D"}, before: "界", after: "🙂"},
		{name: "scroll-at-end", keys: []string{"abcdefghijklmnopqrst"}, before: "nopqrst"},
		{name: "large-query", keys: []string{strings.Repeat("x", 4096)}, before: "xxxxxxx"},
		{name: "home", keys: []string{"abc", "\x1b[H"}, after: "abc"},
		{name: "end", keys: []string{"abc", "\x1b[H", "\x1b[F"}, before: "abc"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			loader := func() ([]SessionInfo, error) { return nil, nil }
			selector := newSessionSelector(loader, loader, nil, nil, "", sessionSelectorInputBindings(t))
			for _, key := range tc.keys {
				selector.HandleInput(key)
			}
			atCursor, after := " ", ""
			if tc.after != "" {
				runes := []rune(tc.after)
				atCursor, after = string(runes[0]), string(runes[1:])
			}
			want := "> " + tc.before + widthx.CursorMarker + "\x1b[7m" + atCursor + "\x1b[27m" + after
			want += strings.Repeat(" ", 10-widthx.VisibleWidth(want))
			if got := selector.renderList(10)[0]; got != want {
				t.Fatalf("search row = %q, want Pi Input %q", got, want)
			}
		})
	}
}

func TestSessionSelectorNoninvasiveDeleteEditsQueryBeforeDeletingSession(t *testing.T) {
	loader := func() ([]SessionInfo, error) {
		return []SessionInfo{{Path: "/session.jsonl", Name: "alpha beta"}}, nil
	}
	bindings := sessionSelectorInputBindings(t)
	// Bind both actions to the same unambiguous Kitty key. SessionList must
	// forward it to Input with a query, and delete only with an empty query.
	bindings.SetUserBindings(map[string][]KeyID{"tui.editor.deleteWordBackward": {"ctrl+backspace"}})
	bindings.syncToTUI()
	selector := newSessionSelector(loader, loader, nil, nil, "", bindings)
	selector.HandleInput("alpha beta")
	selector.HandleInput("\x1b[127;5u")
	if selector.confirmDelete != "" {
		t.Fatalf("Ctrl+Backspace opened delete confirmation with a nonempty query: %q", selector.confirmDelete)
	}
	want := "> alpha " + widthx.CursorMarker + "\x1b[7m \x1b[27m "
	if got := selector.renderList(10)[0]; got != want {
		t.Fatalf("Ctrl+Backspace query row = %q, want %q", got, want)
	}
	selector.HandleInput("\x15") // Ctrl+U clears the remaining query.
	selector.HandleInput("\x1b[127;5u")
	if selector.confirmDelete != "/session.jsonl" {
		t.Fatalf("Ctrl+Backspace did not confirm session deletion with an empty query: %q", selector.confirmDelete)
	}
}

func BenchmarkStartupSessionSelectorRender(b *testing.B) {
	loader := func() ([]SessionInfo, error) { return nil, nil }
	selector := newSessionSelector(loader, loader, nil, nil, "", sessionSelectorInputBindings(b))
	selector.showRenameHint = false
	b.ReportAllocs()
	for b.Loop() {
		selector.Render(100)
	}
}

func TestStartupSessionCancelParksFromSearchCursor(t *testing.T) {
	previousInput := takeStartupInput()
	t.Cleanup(func() {
		takeStartupInput()
		retainStartupInput(previousInput)
	})
	loader := func() ([]SessionInfo, error) { return nil, nil }
	selector := newSessionSelector(loader, loader, nil, nil, "", sessionSelectorInputBindings(t))
	selector.showRenameHint = false
	terminal := &fakeStartupTerminal{}
	var output bytes.Buffer
	ui := tui.NewWithOutput(&output, 100, 35)
	done := make(chan error, 1)
	go func() {
		_, err := runStartupComponentWith(selector, StartupUIOptions{Settings: Settings{Theme: "dark"}}, false, ui, terminal, nil)
		done <- err
	}()
	terminal.send(t, "\x1b")
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !selector.Cancelled() {
		t.Fatal("Escape did not cancel the startup session picker")
	}
	state := ui.CaptureRenderState()
	searchRow := -1
	for i, line := range selector.Render(100) {
		if strings.HasPrefix(line, "> ") {
			searchRow = i
			break
		}
	}
	if searchRow < 0 || state.HardwareCursorRow != searchRow {
		t.Fatalf("hardware cursor row = %d, want search row %d (not the full-width bottom border)", state.HardwareCursorRow, searchRow)
	}
	want := fmt.Sprintf(" \x1b[%dB\r\n\x1b[?25h", len(state.PrevLines)-searchRow)
	if !strings.HasSuffix(output.String(), want) {
		t.Fatalf("stop bytes do not park below previousLines from the input: want suffix %q, got %q", want, output.String())
	}
}

// Upstream SessionSelectorComponent.exitRenameMode
// (packages/coding-agent/src/modes/interactive/components/session-selector.ts:908)
// rebuilds the layout around the existing SessionList and never assigns its
// search Input; Escape (:699) and a successful save (:937) both take that path.
// Query text and cursor must therefore survive the temporary rename panel.
func TestSessionSelectorRenameExitKeepsSearchInputState(t *testing.T) {
	for _, tc := range []struct{ name, exit string }{{"cancel", "\x1b"}, {"save", "\r"}} {
		t.Run(tc.name, func(t *testing.T) {
			loader := func() ([]SessionInfo, error) {
				return []SessionInfo{{Path: "/session.jsonl", Name: "alpha beta"}}, nil
			}
			s := newSessionSelector(loader, loader, func(string, string) error { return nil }, nil, "", sessionSelectorInputBindings(t))
			s.HandleInput("alpha")
			s.HandleInput("\x1b[D")
			before := s.renderList(30)[0]
			s.HandleInput("\x12")
			if !s.renameMode {
				t.Fatal("rename did not start")
			}
			s.HandleInput(tc.exit)
			if s.renameMode {
				t.Fatal("rename did not finish")
			}
			if after := s.renderList(30)[0]; before != after {
				t.Errorf("search Input changed across %s: before=%q after=%q", tc.name, before, after)
			}
			s.HandleInput("X")
			if got := s.searchInput.Text(); got != "alphXa" {
				t.Errorf("insert after %s = %q, want alphXa", tc.name, got)
			}
		})
	}
}
