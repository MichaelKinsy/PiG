package codingagent

// pi: packages/coding-agent/src/core/keybindings.ts
// pi: packages/coding-agent/src/modes/interactive/components/custom-editor.ts

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// customEditorProbe is a CustomEditor over a plain editor; superKeys records the keys that fall through to the editor's own handling.
type customEditorProbe struct {
	*CustomEditor
	superKeys []string
}

func newCustomEditorProbe(t *testing.T, keybindingsJSON string) *customEditorProbe {
	t.Helper()
	restoreTUIKeybindings(t)
	dir := t.TempDir()
	if keybindingsJSON != "" {
		if err := os.WriteFile(filepath.Join(dir, "keybindings.json"), []byte(keybindingsJSON), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	km := NewKeybindingsManager(dir)
	tui.SetKeybindings(km.merged)
	editor := tui.NewEditor()
	probe := &customEditorProbe{}
	probe.CustomEditor = wrapCustomEditor(editor, km, func(data string) {
		probe.superKeys = append(probe.superKeys, data)
		editor.HandleInput(data)
	}, CustomEditorOptions{})
	return probe
}

// packages/coding-agent/test/custom-editor-history-keybindings.test.ts:12 "gives an explicit history binding precedence over model cycling":
// custom-editor.ts:129-135 hands an explicit tui.editor.historyPrevious/historyNext key to the editor before any app action.
func TestCustomEditorExplicitHistoryBindingBeatsModelCycling(t *testing.T) {
	probe := newCustomEditorProbe(t, `{"tui.editor.historyPrevious":["ctrl+p"],"tui.editor.historyNext":["ctrl+n"]}`)
	modelCycles := 0
	probe.OnAction("app.model.cycleForward", func() { modelCycles++ })
	probe.AddToHistory("previous prompt")
	probe.SetText("draft")

	probe.HandleInput("\x10") // Ctrl+P
	if got := probe.Text(); got != "previous prompt" || modelCycles != 0 {
		t.Fatalf("after Ctrl+P text = %q, model cycles = %d, want the previous prompt and no cycle", got, modelCycles)
	}
	probe.HandleInput("\x0e") // Ctrl+N
	if got := probe.Text(); got != "draft" {
		t.Fatalf("after Ctrl+N text = %q, want draft", got)
	}
}

// custom-editor.ts:49-52 and :139-144: every registered app action other than interrupt and exit runs its handler for a matching key, and the
// editor does not see the key; onAction replaces the handler of an action registered before (Map.set).
func TestCustomEditorRunsTheRegisteredAppActionAndReplacesIt(t *testing.T) {
	probe := newCustomEditorProbe(t, "")
	var calls []string
	probe.OnAction("app.model.cycleForward", func() { calls = append(calls, "first") })
	probe.OnAction("app.model.cycleForward", func() { calls = append(calls, "second") })

	probe.HandleInput("\x10") // Ctrl+P is app.model.cycleForward by default
	if !slices.Equal(calls, []string{"second"}) || len(probe.superKeys) != 0 {
		t.Fatalf("calls = %v, editor keys = %q, want only the replacing handler and no editor key", calls, probe.superKeys)
	}
	if _, ok := probe.ActionHandler("app.model.cycleForward"); !ok {
		t.Fatal("ActionHandler lost the registered action")
	}
}

// custom-editor.ts:103-116: Escape runs onEscape, else the app.interrupt handler, and falls to the editor when autocomplete is showing or no handler exists.
func TestCustomEditorEscapePrefersOnEscapeThenTheInterruptActionThenTheEditor(t *testing.T) {
	probe := newCustomEditorProbe(t, "")
	probe.HandleInput("\x1b")
	if !slices.Equal(probe.superKeys, []string{"\x1b"}) {
		t.Fatalf("with no handler Escape went to %q, want the editor", probe.superKeys)
	}

	var calls []string
	probe.OnAction("app.interrupt", func() { calls = append(calls, "action") })
	probe.HandleInput("\x1b")
	probe.OnEscape = func() { calls = append(calls, "onEscape") }
	probe.HandleInput("\x1b")
	if !slices.Equal(calls, []string{"action", "onEscape"}) || len(probe.superKeys) != 1 {
		t.Fatalf("calls = %v, editor keys = %q, want action then onEscape and no more editor keys", calls, probe.superKeys)
	}
}

// custom-editor.ts:106-116: while autocomplete is showing, Escape skips onEscape and the app.interrupt handler and goes to the editor, which
// closes the autocomplete.
func TestCustomEditorEscapeOverAutocompleteGoesToTheEditor(t *testing.T) {
	probe := newCustomEditorProbe(t, "")
	var calls []string
	probe.OnAction("app.interrupt", func() { calls = append(calls, "action") })
	probe.OnEscape = func() { calls = append(calls, "onEscape") }
	probe.SetAutocomplete(tui.NewSlashOnlyProvider([]tui.SlashCommand{{Name: "settings"}}))
	probe.HandleInput("/")
	if !probe.AutocompleteOpen() {
		t.Fatal("slash input did not open autocomplete")
	}
	probe.HandleInput("\x1b")
	if len(calls) != 0 || probe.AutocompleteOpen() || !slices.Equal(probe.superKeys, []string{"/", "\x1b"}) {
		t.Fatalf("calls = %v, autocomplete open = %v, editor keys = %q; want no handler, closed, and Escape to the editor",
			calls, probe.AutocompleteOpen(), probe.superKeys)
	}
}

// custom-editor.ts:139-144: the registered-action loop skips app.interrupt and app.exit, so Ctrl+D with text reaches the editor even when an
// app.exit handler is registered through onAction.
func TestCustomEditorActionLoopSkipsInterruptAndExit(t *testing.T) {
	probe := newCustomEditorProbe(t, "")
	exits := 0
	probe.OnAction("app.exit", func() { exits++ })
	probe.SetText("draft")
	probe.HandleInput("\x04")
	if exits != 0 || !slices.Equal(probe.superKeys, []string{"\x04"}) {
		t.Fatalf("Ctrl+D with text and an app.exit action: exits = %d, editor keys = %q; want no exit and the key to the editor", exits, probe.superKeys)
	}
	probe.SetText("")
	probe.HandleInput("\x04")
	if exits != 1 {
		t.Fatalf("Ctrl+D on an empty editor ran the app.exit action %d times, want 1", exits)
	}
}

// custom-editor.ts:118-126: Ctrl+D exits only while the editor is empty (onCtrlD, else the app.exit handler) and otherwise reaches the editor.
func TestCustomEditorExitOnlyWhenTheEditorIsEmpty(t *testing.T) {
	probe := newCustomEditorProbe(t, "")
	exits := 0
	probe.OnCtrlD = func() { exits++ }

	probe.HandleInput("\x04")
	if exits != 1 {
		t.Fatalf("Ctrl+D on an empty editor ran onCtrlD %d times, want 1", exits)
	}
	probe.SetText("draft")
	probe.HandleInput("\x04")
	if exits != 1 || !slices.Equal(probe.superKeys, []string{"\x04"}) {
		t.Fatalf("Ctrl+D with text: exits = %d, editor keys = %q, want no exit and the key to the editor", exits, probe.superKeys)
	}
}

// custom-editor.ts:89-100: an extension shortcut that reports itself handled stops the key before every app action, and the clipboard image paste
// runs onPasteImage without reaching the editor.
func TestCustomEditorExtensionShortcutThenPasteImageComeFirst(t *testing.T) {
	probe := newCustomEditorProbe(t, "")
	var calls []string
	probe.OnAction("app.model.cycleForward", func() { calls = append(calls, "cycle") })
	probe.OnPasteImage = func() { calls = append(calls, "paste") }
	probe.OnExtensionShortcut = func(data string) bool {
		calls = append(calls, "shortcut:"+data)
		return data == "\x10"
	}

	// keybindings.ts:143 binds app.clipboard.pasteImage to alt+v with Windows keybindings and to ctrl+v elsewhere.
	pasteKey := "\x16" // Ctrl+V
	if runtime.GOOS == "windows" {
		pasteKey = "\x1bv" // Alt+V
	}
	probe.HandleInput("\x10")   // consumed by the extension shortcut before app.model.cycleForward
	probe.HandleInput(pasteKey) // not an extension shortcut, so the image paste runs
	if !slices.Equal(calls, []string{"shortcut:\x10", "shortcut:" + pasteKey, "paste"}) || len(probe.superKeys) != 0 {
		t.Fatalf("calls = %q, editor keys = %q", calls, probe.superKeys)
	}
}

// core/keybindings.ts:60 AppKeybinding is keyof AppKeybindings: one id per property of the interface, and each id has a definition in the manager.
func TestAppKeybindingIsTheClosedSetOfAppKeybindingDefinitions(t *testing.T) {
	seen := map[AppKeybinding]bool{}
	for _, id := range appKeybindingIDs {
		if seen[id] {
			t.Errorf("%q listed twice", id)
		}
		seen[id] = true
		if _, ok := appKeybindingDefinitions[string(id)]; !ok {
			t.Errorf("%q has no keybinding definition", id)
		}
	}
	if len(seen) != 43 {
		t.Errorf("%d app keybindings, want the 43 properties of upstream AppKeybindings", len(seen))
	}
	for id := range appKeybindingDefinitions {
		if !seen[AppKeybinding(id)] {
			t.Errorf("definition %q is not an AppKeybinding", id)
		}
	}
}

// custom-editor.ts:25 `this.embedWorkingStatus = options?.embedWorkingStatus ?? false`: the editor embeds the working status only when asked.
func TestNewCustomEditorEmbedsWorkingStatusOnlyWhenOptioned(t *testing.T) {
	for _, want := range []bool{false, true} {
		editor := tui.NewEditor()
		editor.EmbedWorkingStatus = !want
		wrapCustomEditor(editor, nil, func(string) {}, CustomEditorOptions{EmbedWorkingStatus: want})
		if editor.EmbedWorkingStatus != want {
			t.Fatalf("EmbedWorkingStatus = %v, want %v", editor.EmbedWorkingStatus, want)
		}
	}
}

// custom-editor.ts:24 passes the EditorOptions through to the Editor: paddingX and autocompleteMaxVisible apply when given and are left alone otherwise.
func TestNewCustomEditorAppliesTheEditorOptions(t *testing.T) {
	editor := tui.NewEditor()
	padding, visible := 3, 9
	wrapCustomEditor(editor, nil, func(string) {}, CustomEditorOptions{PaddingX: &padding, AutocompleteMaxVisible: &visible})
	if editor.PaddingX() != 3 || editor.AutocompleteMaxVisible() != 9 {
		t.Fatalf("paddingX=%d autocompleteMaxVisible=%d, want 3 and 9", editor.PaddingX(), editor.AutocompleteMaxVisible())
	}
	left := tui.NewEditor()
	was, wasVisible := left.PaddingX(), left.AutocompleteMaxVisible()
	wrapCustomEditor(left, nil, func(string) {}, CustomEditorOptions{})
	if left.PaddingX() != was || left.AutocompleteMaxVisible() != wasVisible {
		t.Fatalf("unset options changed the editor: paddingX %d->%d, autocompleteMaxVisible %d->%d", was, left.PaddingX(), wasVisible, left.AutocompleteMaxVisible())
	}
}

// custom-editor.ts:24-27: `new CustomEditor(tui, theme, keybindings, options)` builds the Editor itself with the theme and options, so a key no app action takes reaches that editor's own input handling (super.handleInput).
func TestNewCustomEditorBuildsItsEditorAndRoutesUnhandledKeysToIt(t *testing.T) {
	border := func(text string) string { return "[" + text + "]" }
	padding := 2
	custom := NewCustomEditor(nil, tui.EditorTheme{BorderColor: border}, nil, CustomEditorOptions{PaddingX: &padding, EmbedWorkingStatus: true})
	if custom.Editor == nil || custom.PaddingX() != 2 || !custom.EmbedWorkingStatus {
		t.Fatalf("editor = %+v: want one built with paddingX 2 and embedWorkingStatus", custom.Editor)
	}
	if custom.BorderColor("x") != "[x]" {
		t.Fatal("the editor does not use the given theme's border color")
	}
	custom.HandleInput("a")
	custom.HandleInput("b")
	if got := custom.Text(); got != "ab" {
		t.Fatalf("text after typing through the CustomEditor = %q, want ab (the editor's own handleInput)", got)
	}
}

// custom-editor.ts:17, 85 and 138-144: actionHandlers is a Map, so handleInput visits the registered actions in insertion order and the first one
// whose binding matches runs; Map.set on an action that is already registered replaces its handler and keeps its place. With two actions bound to
// one key, the order of first registration decides which handler runs.
func TestCustomEditorActionHandlersRunInRegistrationOrderAndKeepAReplacedPlace(t *testing.T) {
	const bindings = `{"app.model.cycleForward":["ctrl+x"],"app.model.cycleBackward":["ctrl+x"]}`
	run := func(register func(probe *customEditorProbe, record func(string) func())) []string {
		probe := newCustomEditorProbe(t, bindings)
		var calls []string
		register(probe, func(name string) func() { return func() { calls = append(calls, name) } })
		probe.HandleInput("\x18") // Ctrl+X
		if len(probe.superKeys) != 0 {
			t.Fatalf("the editor saw %q although an app action matched", probe.superKeys)
		}
		return calls
	}
	if got := run(func(p *customEditorProbe, record func(string) func()) {
		p.OnAction("app.model.cycleForward", record("forward"))
		p.OnAction("app.model.cycleBackward", record("backward"))
	}); !slices.Equal(got, []string{"forward"}) {
		t.Fatalf("forward registered first: ran %v, want only the first-registered handler", got)
	}
	if got := run(func(p *customEditorProbe, record func(string) func()) {
		p.OnAction("app.model.cycleBackward", record("backward"))
		p.OnAction("app.model.cycleForward", record("forward"))
	}); !slices.Equal(got, []string{"backward"}) {
		t.Fatalf("backward registered first: ran %v, want only the first-registered handler", got)
	}
	if got := run(func(p *customEditorProbe, record func(string) func()) {
		p.OnAction("app.model.cycleForward", record("forward-old"))
		p.OnAction("app.model.cycleBackward", record("backward"))
		p.OnAction("app.model.cycleForward", record("forward-new"))
	}); !slices.Equal(got, []string{"forward-new"}) {
		t.Fatalf("re-registered action: ran %v, want its replacement in its original place", got)
	}
}

// custom-editor.ts:118-126: an app.exit key on an empty editor returns whether or not a handler exists (`if (handler) handler(); return;`), so with neither onCtrlD nor an
// app.exit handler the key still does not reach another app action bound to the same key, nor the editor.
// mutation-checked: returning only when a handler ran (falling through to the other actions and the editor) fails it.
func TestCustomEditorExitOnAnEmptyEditorWithoutAHandlerConsumesTheKey(t *testing.T) {
	probe := newCustomEditorProbe(t, `{"app.exit":"ctrl+d","app.model.cycleForward":"ctrl+d"}`)
	cycles := 0
	probe.OnAction("app.model.cycleForward", func() { cycles++ })
	probe.HandleInput("\x04")
	if cycles != 0 || len(probe.superKeys) != 0 {
		t.Fatalf("app.exit with no handler on an empty editor: other action ran %d times, editor keys %q; want the key consumed", cycles, probe.superKeys)
	}
}
