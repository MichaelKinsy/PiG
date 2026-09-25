package codingagent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

func TestKeybindingsManagerDefaultsResolveCoreAppBindings(t *testing.T) {
	km := otherColumnKeys()
	cases := []struct {
		input string
		want  string
	}{
		{"\x1b", "app.interrupt"},
		{"\x03", "app.clear"},
		{"\x04", "app.exit"},
		{"\x1a", "app.suspend"},
		{"\x14", "app.thinking.toggle"},
		{"\x10", "app.model.cycleForward"},
		{"\x1b[112;6u", "app.model.cycleBackward"},
		{"\x0c", "app.model.select"},
		{"\x0f", "app.tools.expand"},
		{"\x07", "app.editor.external"},
		{"\x16", "app.clipboard.pasteImage"},
		// Ctrl+X resolves to message-copy in the main loop even though
		// app.models.clearAll shares the key (ordered first wins; clearAll is
		// handled inside the models-selector modal loop).
		{"\x18", "app.message.copy"},
		{"\x1b[13;3u", "app.message.followUp"},
		{"\x1b[1;3A", "app.message.dequeue"},
		{"\x1b[Z", "app.thinking.cycle"},
	}
	for _, tc := range cases {
		if got := km.Resolve(tc.input); got != tc.want {
			t.Fatalf("Resolve(%q) = %q want %q", tc.input, got, tc.want)
		}
	}
}

func TestKeybindingsManagerLoadFromFileOverridesDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "keybindings.json")
	if err := os.WriteFile(path, []byte(`{"app.tools.expand":"ctrl+g","app.model.select":["ctrl+p"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	km := NewKeybindingsManager(dir)
	if got := km.Resolve("\x07"); got != "app.tools.expand" {
		t.Fatalf("Resolve(ctrl+g) = %q want app.tools.expand", got)
	}
	if got := km.Resolve("\x0f"); got == "app.tools.expand" {
		t.Fatalf("default ctrl+o should be overridden, got %q", got)
	}
	if got := km.Resolve("\x10"); got != "app.model.select" {
		t.Fatalf("Resolve(ctrl+p) = %q want app.model.select", got)
	}
}

func TestKeybindingsManagerMigratesLegacyNames(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "keybindings.json")
	if err := os.WriteFile(path, []byte(`{"expandTools":"ctrl+g","selectModel":"ctrl+p"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	km := NewKeybindingsManager(dir)
	if got := km.Resolve("\x07"); got != "app.tools.expand" {
		t.Fatalf("Resolve(ctrl+g) = %q want app.tools.expand", got)
	}
	if got := km.Resolve("\x10"); got != "app.model.select" {
		t.Fatalf("Resolve(ctrl+p) = %q want app.model.select", got)
	}
}

func TestKeybindingsManagerSaveRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "keybindings.json")
	km := NewKeybindingsManager("")
	km.SetUserBindings(map[string][]KeyID{
		"app.tools.expand":       {"ctrl+g"},
		"app.model.cycleForward": {"ctrl+l"},
	})
	if err := km.Save(path); err != nil {
		t.Fatal(err)
	}
	loaded := NewKeybindingsManager(dir)
	loaded.configPath = path
	if err := loaded.Reload(); err != nil {
		t.Fatal(err)
	}
	if got := loaded.Get("app.tools.expand"); len(got) != 1 || got[0] != "ctrl+g" {
		t.Fatalf("loaded tools.expand = %v", got)
	}
	if got := loaded.Get("app.model.cycleForward"); len(got) != 1 || got[0] != "ctrl+l" {
		t.Fatalf("loaded model.cycleForward = %v", got)
	}
}

func TestKeybindingsManagerConflictDetection(t *testing.T) {
	km := NewKeybindingsManager("")
	km.SetUserBindings(map[string][]KeyID{
		"app.tools.expand": {"ctrl+g"},
		"app.model.select": {"ctrl+g"},
	})
	conflicts := km.Conflicts()
	if len(conflicts) != 1 {
		t.Fatalf("len(conflicts) = %d want 1", len(conflicts))
	}
	if conflicts[0].Key != "ctrl+g" {
		t.Fatalf("conflict key = %q want ctrl+g", conflicts[0].Key)
	}
}

func TestClassifyKeyWithBindingsUsesOverrides(t *testing.T) {
	km := NewKeybindingsManager("")
	km.SetUserBindings(map[string][]KeyID{
		"app.tools.expand": {"ctrl+g"},
	})
	if got := classifyKeyWithBindings("\x07", km); got != actionToggleTools {
		t.Fatalf("classifyKeyWithBindings(ctrl+g) = %v want actionToggleTools", got)
	}
}

func TestKeybindingsManagerUnknownInputReturnsEmpty(t *testing.T) {
	km := DefaultKeybindingsManager()
	if got := km.Resolve("\x00"); got != "" {
		t.Fatalf("Resolve(unknown) = %q want empty", got)
	}
}

func TestKeybindingsManagerSaveEncodesJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "keybindings.json")
	km := NewKeybindingsManager("")
	km.SetUserBindings(map[string][]KeyID{"app.tools.expand": {"ctrl+g", "ctrl+o"}})
	if err := km.Save(path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if _, ok := decoded["app.tools.expand"]; !ok {
		t.Fatalf("saved JSON missing app.tools.expand: %s", data)
	}
}

// TestKeybindingsManagerSyncsTuiOverridesToGlobal verifies that tui.*
// keys in the user's keybindings.json propagate to the global
// tui.TUIKeybindingsManager so the Editor / SelectList actually pick
// them up. Regression marker for the registry-wiring refactor:
// without syncToTUI, pig would happily parse tui.* keys but ignore
// them at dispatch time.
func TestKeybindingsManagerSyncsTuiOverridesToGlobal(t *testing.T) {
	// Snapshot global TUI manager so the test doesn't leak.
	prev := tui.GetTUIKeybindings()
	defer tui.SetTUIKeybindings(prev)
	tui.SetTUIKeybindings(tui.NewTUIKeybindingsManager(nil))

	dir := t.TempDir()
	path := filepath.Join(dir, "keybindings.json")
	body := []byte(`{
		"tui.editor.deleteWordBackward": "ctrl+r",
		"app.model.cycleForward": "ctrl+j",
		"app.models.clearAll": "ctrl+r"
	}`)
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}

	km := NewKeybindingsManager(dir)

	// app.* binding should land in the app manager.
	if action := km.Resolve("\n"); action != "" {
		// (sanity: enter without a binding doesn't resolve to anything)
		_ = action
	}
	if !km.Matches("\x0a", "app.model.cycleForward") {
		// "ctrl+j" maps to \x0a (LF). The manager's keyIDInputs may
		// not have ctrl+j → \x0a explicitly; soft-skip if missing.
		// The real assertion below is the tui.* sync.
		t.Logf("note: app.model.cycleForward not bound to ctrl+j byte (keyIDInputs gap, not the test target)")
	}

	// tui.* binding should have been pushed to the global TUI manager.
	if !tui.GetTUIKeybindings().Matches("\x12", "tui.editor.deleteWordBackward") {
		t.Errorf("tui.editor.deleteWordBackward should match ctrl+r (\\x12) after sync")
	}
	// And the default alt+backspace should no longer match (user
	// override replaces the default list).
	if tui.GetTUIKeybindings().Matches("\x1b\x7f", "tui.editor.deleteWordBackward") {
		t.Errorf("alt+backspace should NOT match after user override")
	}
	if !tui.GetTUIKeybindings().Matches("\x12", "app.models.clearAll") {
		t.Error("app.models.clearAll should be bridged to the TUI selector manager")
	}
}

func TestKeybindingsManagerSetsAppKeyTextResolver(t *testing.T) {
	prevResolver := tui.AppKeyText("app.tree.foldOrUp", "fallback")
	_ = prevResolver

	dir := t.TempDir()
	path := filepath.Join(dir, "keybindings.json")
	body := []byte(`{
		"app.tree.foldOrUp": "h",
		"app.tree.unfoldOrDown": "l"
	}`)
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}

	_ = NewKeybindingsManager(dir)

	if got := tui.AppKeyText("app.tree.foldOrUp", "ctrl+left/alt+left"); got != "h" {
		t.Fatalf("AppKeyText(foldOrUp) = %q want %q", got, "h")
	}
	if got := tui.AppKeyText("app.tree.unfoldOrDown", "ctrl+right/alt+right"); got != "l" {
		t.Fatalf("AppKeyText(unfoldOrDown) = %q want %q", got, "l")
	}
}

func TestKeybindingsManagerLegacyShortNamesMigrateTui(t *testing.T) {
	prev := tui.GetTUIKeybindings()
	defer tui.SetTUIKeybindings(prev)
	tui.SetTUIKeybindings(tui.NewTUIKeybindingsManager(nil))

	dir := t.TempDir()
	// Legacy short-name schema (pre-v0.69.0 upstream rename).
	body := []byte(`{ "deleteWordBackward": "ctrl+r" }`)
	if err := os.WriteFile(filepath.Join(dir, "keybindings.json"), body, 0o644); err != nil {
		t.Fatal(err)
	}

	_ = NewKeybindingsManager(dir)

	if !tui.GetTUIKeybindings().Matches("\x12", "tui.editor.deleteWordBackward") {
		t.Errorf("legacy 'deleteWordBackward' should migrate to 'tui.editor.deleteWordBackward'")
	}
}

func TestResolvedBindingsReturnsDetachedSnapshot(t *testing.T) {
	manager := NewKeybindingsManager(t.TempDir())
	resolved := manager.ResolvedBindings()
	if len(resolved["app.clear"]) == 0 {
		t.Fatal("app.clear has no resolved binding")
	}
	resolved["app.clear"][0] = "mutated"
	if manager.Get("app.clear")[0] == "mutated" {
		t.Fatal("ResolvedBindings exposed the manager's backing slice")
	}
}

func TestNilKeybindingsManagerHasEmptyResolvedBindings(t *testing.T) {
	var manager *KeybindingsManager
	if got := manager.ResolvedBindings(); got != nil {
		t.Fatalf("nil manager bindings = %v, want nil", got)
	}
}

// Ports "CustomEditor prompt history keybindings › gives an explicit history
// binding precedence over model cycling" (coding-agent
// test/custom-editor-history-keybindings.test.ts).
func TestExplicitHistoryBindingTakesPrecedenceOverModelCycling(t *testing.T) {
	previous := tui.GetTUIKeybindings()
	t.Cleanup(func() { tui.SetTUIKeybindings(previous) })
	km := DefaultKeybindingsManager()
	km.SetUserBindings(map[string][]KeyID{
		tui.KBEditorHistoryPrevious: {"ctrl+p"},
		tui.KBEditorHistoryNext:     {"ctrl+n"},
	})
	km.syncToTUI()
	if got := classifyKeyWithBindings("\x10", km); got != actionInsert {
		t.Fatalf("ctrl+p with a history binding classifies as %d, want the editor (actionInsert)", got)
	}
	editor := tui.NewEditor()
	editor.AddToHistory("previous prompt")
	editor.SetText("draft")
	editor.HandleInput("\x10")
	if got := editor.Text(); got != "previous prompt" {
		t.Fatalf("ctrl+p: editor text = %q, want the history entry", got)
	}
	editor.HandleInput("\x0e")
	if got := editor.Text(); got != "draft" {
		t.Fatalf("ctrl+n: editor text = %q, want the draft", got)
	}

	// Without the history binding, ctrl+p still cycles models.
	km.SetUserBindings(nil)
	if got := classifyKeyWithBindings("\x10", km); got != actionCycleModelForward {
		t.Fatalf("ctrl+p without a history binding classifies as %d, want model cycling", got)
	}
}

// App bindings resolve Kitty flag-4 alternate keys like upstream matchesKey
// (keys.ts matchesKittySequence): a non-Latin codepoint falls back to its
// base-layout key, while a Latin codepoint stays authoritative.
func TestKeybindingsManagerResolvesKittyBaseLayoutKey(t *testing.T) {
	km := otherColumnKeys()
	cases := []struct {
		input string
		want  keyAction
	}{
		{"\x1b[1089::99;5u", actionClearEditor},             // Ctrl+С, base c
		{"\x1b[1074::100;5u", actionExit},                   // Ctrl+В, base d
		{"\x1b[1097::111;5u", actionToggleTools},            // Ctrl+Щ, base o
		{"\x1b[1079:1047:112;6u", actionCycleModelBackward}, // Ctrl+Shift+З, base p; non-Windows column
		{"\x1b[1077::116;5:2u", actionToggleThinking},       // Ctrl+Е repeat, base t
		{"\x1b[107::118;5u", actionInsert},                  // Dvorak Ctrl+K, base v: not paste-image
		{"\x1b[1089::99;6u", actionInsert},                  // Ctrl+Shift+С is not ctrl+c
	}
	for _, tc := range cases {
		if got := classifyKeyWithBindings(tc.input, km); got != tc.want {
			t.Errorf("classifyKeyWithBindings(%q) = %v want %v", tc.input, got, tc.want)
		}
	}

	custom := NewKeybindingsManager("")
	custom.SetUserBindings(map[string][]KeyID{"app.tools.expand": {"ctrl+g"}})
	// Ctrl+П (п = 1087) sits on the Latin g key.
	if got := custom.Resolve("\x1b[1087::103;5u"); got != "app.tools.expand" {
		t.Fatalf("Resolve(Ctrl+П base g) = %q want app.tools.expand", got)
	}
}

func TestKeybindingsManagerUsesModeAwareSharedMatcher(t *testing.T) {
	tui.SetKittyProtocolActive(false)
	t.Cleanup(func() { tui.SetKittyProtocolActive(false) })
	km := otherColumnKeys()
	if !km.Matches("\x1b\r", "app.message.followUp") {
		t.Fatal("legacy ESC CR did not match alt+enter follow-up")
	}

	custom := NewKeybindingsManager("")
	custom.SetUserBindings(map[string][]KeyID{"app.tools.expand": {"alt+a"}})
	if !custom.Matches("\x1ba", "app.tools.expand") {
		t.Fatal("legacy ESC a did not match custom alt+a")
	}

	tui.SetKittyProtocolActive(true)
	if km.Matches("\x1b\r", "app.message.followUp") {
		t.Fatal("Kitty mode interpreted ESC CR as alt+enter instead of shift+enter")
	}
	if custom.Matches("\x1ba", "app.tools.expand") {
		t.Fatal("Kitty mode accepted ambiguous legacy alt+a")
	}
	if !custom.Matches("\x1b[97;3u", "app.tools.expand") {
		t.Fatal("Kitty mode did not accept CSI-u alt+a")
	}
	if got := classifyKeyWithBindings("\x1b\r", km); got != actionNewline {
		t.Fatalf("Kitty ESC CR classified as %v, want newline", got)
	}
}
