package codingagent

import (
	"fmt"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

const (
	selectPageUpKey   = "\x1b[5~"
	selectPageDownKey = "\x1b[6~"
)

// custom-editor.ts (Pi PR #7680): with the autocomplete list open, a selection page key reaches the editor before any app
// action, even one the user bound to the same key.
func TestCustomEditorGivesSelectionPageKeysToAnOpenAutocomplete(t *testing.T) {
	probe := newCustomEditorProbe(t, `{"app.clipboard.pasteImage":["pageDown"]}`)
	commands := make([]tui.SlashCommand, 12)
	for i := range commands {
		commands[i] = tui.SlashCommand{Name: fmt.Sprintf("cmd%02d", i), Description: "command"}
	}
	probe.SetAutocomplete(tui.NewSlashOnlyProvider(commands))
	pasted := 0
	probe.OnPasteImage = func() { pasted++ }

	probe.HandleInput(selectPageDownKey)
	if pasted != 1 {
		t.Fatalf("with no autocomplete open, a key bound to the paste action pasted %d times, want 1", pasted)
	}
	probe.HandleInput("/")
	if !tui.CapturesSelectPageInput(probe) {
		t.Fatal("an editor with an open autocomplete list must capture the selection page keys")
	}
	probe.HandleInput(selectPageDownKey)
	if pasted != 1 {
		t.Fatalf("PageDown with the autocomplete open ran the paste action (%d pastes)", pasted)
	}
	if probe.Text() != "/" {
		t.Fatalf("text = %q, want the page key not to edit", probe.Text())
	}
	// The key reaches the editor, whose open list pages by one visible page: the first page's worth down selects the sixth command.
	if want := []string{"/", selectPageDownKey}; !slices.Equal(probe.superKeys, want) {
		t.Fatalf("editor keys = %q, want %q: the page key must be handed to the editor, not dropped", probe.superKeys, want)
	}
	probe.AutocompleteAccept()
	if want := fmt.Sprintf("/cmd%02d ", probe.AutocompleteMaxVisible()); probe.Text() != want {
		t.Fatalf("accepting after one page down gave %q, want %q", probe.Text(), want)
	}
}

func TestTrustSelectorPageKeysMoveToTheEnds(t *testing.T) {
	selector := NewTrustSelectorComponent(TrustSelectorOptions{Cwd: t.TempDir()})
	if len(selector.trustOptions) < 2 {
		t.Fatalf("trust options = %d, want a list to move in", len(selector.trustOptions))
	}
	if !tui.CapturesSelectPageInput(selector) {
		t.Fatal("the trust selector must capture the selection page keys")
	}
	selector.HandleInput(selectPageDownKey)
	if want := len(selector.trustOptions) - 1; selector.selectedIndex != want {
		t.Fatalf("page down: index = %d, want the last option %d", selector.selectedIndex, want)
	}
	selector.HandleInput(selectPageUpKey)
	if selector.selectedIndex != 0 {
		t.Fatalf("page up: index = %d, want the first option", selector.selectedIndex)
	}
}

func TestFirstTimeSetupPageKeysMoveToTheEnds(t *testing.T) {
	setup := NewFirstTimeSetupComponent(FirstTimeSetupOptions{})
	if !tui.CapturesSelectPageInput(setup) {
		t.Fatal("first-time setup must capture the selection page keys")
	}
	setup.HandleInput(selectPageDownKey)
	if want := len(firstTimeSetupThemeOptions) - 1; setup.themeIndex != want {
		t.Fatalf("theme step page down: index = %d, want %d", setup.themeIndex, want)
	}
	setup.HandleInput(selectPageUpKey)
	if setup.themeIndex != 0 {
		t.Fatalf("theme step page up: index = %d, want 0", setup.themeIndex)
	}
	setup.step = firstTimeSetupStepAnalytics
	setup.HandleInput(selectPageDownKey)
	if want := len(firstTimeSetupAnalyticsOptions) - 1; setup.analyticsIndex != want {
		t.Fatalf("analytics step page down: index = %d, want %d", setup.analyticsIndex, want)
	}
}

func TestSessionSelectorCapturesSelectionPageKeys(t *testing.T) {
	if !tui.CapturesSelectPageInput(&SessionSelectorComponent{}) {
		t.Fatal("the session selector must capture the selection page keys")
	}
}
