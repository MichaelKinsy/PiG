package subprocess_test

import (
	"fmt"
	"net/url"
	"os/exec"
	"path/filepath"
	"testing"
)

// Pi's setCustomEditorComponent installs the editor whatever the provider state: it hands the editor the current provider only when there is one
// (`if (newEditor.setAutocompleteProvider && this.autocompleteProvider)`, interactive-mode.ts), and setupAutocompleteProvider gives the installed editor the
// provider when it first runs, after bindExtensions returns (:2167). An extension installs its editor from session_start, before that. The host answers
// ui.autocomplete.current with null when the mode has no editor to install into, with {} while the first setupAutocompleteProvider has not run, and with the
// provider's reference otherwise: only null refuses the install, and only a reference is handed to the editor.
func TestNodeEditorInstallsBeforeTheHostHasAProvider(t *testing.T) {
	abs := func(rel string) string {
		path, err := filepath.Abs(rel)
		if err != nil {
			t.Fatal(err)
		}
		return (&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String()
	}
	script := fmt.Sprintf(`
import assert from "node:assert/strict";
const { Runtime } = await import(%q);
const pigAgent = await import(%q);

const attempt = (answer) => {
  const runtime = new Runtime("/ext/early.mjs");
  runtime.ready = { width: 60, height: 24, cwd: process.cwd() };
  const sent = [];
  runtime.notify = (method, args) => sent.push({ method, args });
  runtime.call = async () => null;
  runtime.callSync = (method) => {
    assert.equal(method, "ui.autocomplete.current");
    return answer;
  };
  runtime.fireAndForget = () => {};
  const given = [];
  let built = 0;
  runtime.ui.setEditorComponent((tui, theme, keybindings) => {
    built++;
    const editor = new pigAgent.CustomEditor(tui, theme, keybindings);
    const set = editor.setAutocompleteProvider.bind(editor);
    editor.setAutocompleteProvider = (provider) => { given.push(provider); set(provider); };
    return editor;
  });
  return { built, given, installed: sent.some((m) => m.method === "ui.editor.install") };
};

const noEditor = attempt(null);
assert.equal(noEditor.built, 0, "a mode with no editor builds nothing");
assert.equal(noEditor.installed, false);

const early = attempt({});
assert.equal(early.built, 1, "no provider yet: the editor is built");
assert.equal(early.installed, true, "no provider yet: the editor is installed");
assert.deepEqual(early.given, [], "no provider yet: the editor is handed none");

const later = attempt({ id: "base", triggerCharacters: [], hasFileTrigger: false });
assert.equal(later.installed, true);
assert.equal(later.given.length, 1, "a provider that exists is handed over at install");
`, abs("runtime-node/runtime.mjs"), abs("runtime-node/shims/pi-coding-agent.mjs"))
	if output, err := exec.CommandContext(t.Context(), "node", "--input-type=module", "--eval", script).CombinedOutput(); err != nil {
		t.Fatalf("editor install without a provider: %v\n%s", err, output)
	}
}
