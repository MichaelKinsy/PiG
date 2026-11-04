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
	if !tui.CapturesViewportInput(probe) {
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

// trust-selector.ts has no select page keys and does not set capturesViewportInput: PageUp and PageDown leave the
// selection where it is and the fullscreen viewport keeps the keys.
func TestTrustSelectorIgnoresSelectionPageKeys(t *testing.T) {
	selector := NewTrustSelectorComponent(TrustSelectorOptions{Cwd: t.TempDir()})
	if len(selector.trustOptions) < 2 {
		t.Fatalf("trust options = %d, want a list to move in", len(selector.trustOptions))
	}
	if tui.CapturesViewportInput(selector) {
		t.Fatal("the trust selector must not capture viewport input")
	}
	selector.HandleInput("j")
	selector.HandleInput(selectPageDownKey)
	if selector.selectedIndex != 1 {
		t.Fatalf("page down: index = %d, want it to stay at 1", selector.selectedIndex)
	}
	selector.HandleInput(selectPageUpKey)
	if selector.selectedIndex != 1 {
		t.Fatalf("page up: index = %d, want it to stay at 1", selector.selectedIndex)
	}
}

// first-time-setup.ts has no select page keys and does not set capturesViewportInput.
func TestFirstTimeSetupIgnoresSelectionPageKeys(t *testing.T) {
	setup := NewFirstTimeSetupComponent(FirstTimeSetupOptions{})
	if tui.CapturesViewportInput(setup) {
		t.Fatal("first-time setup must not capture viewport input")
	}
	setup.HandleInput("j")
	setup.HandleInput(selectPageDownKey)
	if setup.themeIndex != 1 {
		t.Fatalf("theme step page down: index = %d, want it to stay at 1", setup.themeIndex)
	}
	setup.HandleInput(selectPageUpKey)
	if setup.themeIndex != 1 {
		t.Fatalf("theme step page up: index = %d, want it to stay at 1", setup.themeIndex)
	}
	setup.step = firstTimeSetupStepAnalytics
	setup.HandleInput(selectPageDownKey)
	if setup.analyticsIndex != 0 {
		t.Fatalf("analytics step page down: index = %d, want it to stay at 0", setup.analyticsIndex)
	}
}

func TestSessionSelectorCapturesSelectionPageKeys(t *testing.T) {
	if !tui.CapturesViewportInput(&SessionSelectorComponent{}) {
		t.Fatal("the session selector must capture the viewport keys")
	}
	if tui.CapturesViewportInput(&SessionSelectorComponent{renameMode: true}) {
		t.Fatal("the session selector must not capture the viewport keys while renaming")
	}
}
