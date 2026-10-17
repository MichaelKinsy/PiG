package codingagent

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui"
)

// Ports of packages/coding-agent/test/suite/regressions/7629-select-page-keys.test.ts of Pi PR #7680 (Pi #7629, D155) for the
// components in this package. Every case remaps the selection page keys (REMAPPED_PAGE_KEYS); the selectors in package tui are in
// tui/select_page_keys_pr7680_test.go.

var remappedPageKeys = map[string][]KeyID{
	"tui.editor.pageUp":   {"alt+v"},
	"tui.editor.pageDown": {"ctrl+v"},
	"tui.select.pageUp":   {"alt+v"},
	"tui.select.pageDown": {"ctrl+v"},
}

func useRemappedPageKeyManager(t *testing.T) *KeybindingsManager {
	t.Helper()
	previous := tui.GetTUIKeybindings()
	t.Cleanup(func() { tui.SetKeybindings(previous) })
	return NewKeybindingsManagerFromBindings(remappedPageKeys, "")
}

// "prioritizes autocomplete page keys over the image paste shortcut": ctrl+v is also app.clipboard.pasteImage.
func TestPR7680AutocompletePageKeysWinOverTheImagePasteShortcut(t *testing.T) {
	km := useRemappedPageKeyManager(t)
	editor := tui.NewEditor()
	custom := wrapCustomEditor(editor, km, editor.HandleInput, CustomEditorOptions{})
	commands := make([]tui.SlashCommand, 12)
	for i := range commands {
		commands[i] = tui.SlashCommand{Name: fmt.Sprintf("command-%d", i)}
	}
	custom.SetAutocomplete(tui.NewSlashOnlyProvider(commands))
	pasted := 0
	custom.OnPasteImage = func() { pasted++ }
	var submitted string
	editor.OnSubmit = func(text string) { submitted = text }
	custom.HandleInput("/")
	if !editor.AutocompleteOpen() {
		t.Fatal("autocomplete did not open")
	}
	custom.HandleInput("\x16")
	custom.HandleInput("\r")
	if pasted != 0 {
		t.Fatalf("ctrl+v ran the image paste %d times", pasted)
	}
	if submitted != "/command-5" {
		t.Fatalf("submitted %q, want /command-5", submitted)
	}
}

// "keeps the session selection valid when paging before sessions load".
func TestPR7680SessionSelectionStaysValidWhenPagingBeforeTheLoad(t *testing.T) {
	km := useRemappedPageKeyManager(t)
	release := make(chan struct{})
	loader := func(ctx context.Context, _ SessionListProgress) ([]SessionInfo, error) {
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return []SessionInfo{scopeSession("session")}, nil
	}
	var selected []string
	selector := NewSessionSelectorComponent(loader, func(context.Context, SessionListProgress) ([]SessionInfo, error) { return nil, nil },
		func(path string) { selected = append(selected, path) }, func() {}, func() {}, func() {}, &SessionSelectorOptions{Keybindings: km})
	defer selector.close()
	selector.GetSessionList().HandleInput("\x16")
	close(release)
	for deadline := time.Now().Add(5 * time.Second); len(selector.filtered) == 0 && time.Now().Before(deadline); time.Sleep(time.Millisecond) {
		selector.drainLoadUpdates()
	}
	selector.GetSessionList().HandleInput("\r")
	if len(selected) != 1 || selected[0] != "/tmp/session.jsonl" {
		t.Fatalf("onSelect calls = %v, want [/tmp/session.jsonl]", selected)
	}
}

// The trust case of "pages through standalone selectors with remapped keys" and the first-time-setup and thinking cases of
// "pages through SelectList-backed selectors".
func TestPR7680PackageSelectorsPageWithRemappedKeys(t *testing.T) {
	useRemappedPageKeyManager(t)
	var trust *TrustSelection
	project := t.TempDir()
	trustSelector := NewTrustSelectorComponent(TrustSelectorOptions{Cwd: project, OnSelect: func(s TrustSelection) { trust = &s }, OnCancel: func() {}})
	trustSelector.HandleInput("\x16")
	trustSelector.HandleInput("\r")
	if trust == nil || trust.Trusted || len(trust.Updates) != 1 || trust.Updates[0].Path != CanonicalizePath(project) || trust.Updates[0].Decision == nil || *trust.Updates[0].Decision {
		t.Errorf("trust selection = %+v, want untrusted with %s decided false", trust, project)
	}

	var previews []string
	setup := NewFirstTimeSetupComponent(FirstTimeSetupOptions{OnThemePreview: func(name string) { previews = append(previews, name) }})
	setup.HandleInput("\x16")
	if len(previews) == 0 || previews[len(previews)-1] != "light" {
		t.Errorf("theme previews = %v, want light last", previews)
	}

	var thinking ai.ThinkingLevel
	levels := []ai.ThinkingLevel{"off", "minimal", "low", "medium", "high", "xhigh", "max"}
	thinkingSelector := NewThinkingSelectorComponent("off", levels, func(level ai.ThinkingLevel) { thinking = level }, func() {}, nil, "")
	thinkingSelector.GetSelectList().HandleInput("\x16")
	thinkingSelector.GetSelectList().HandleInput("\r")
	if thinking != "max" {
		t.Errorf("thinking selector selected %q, want max", thinking)
	}
}
