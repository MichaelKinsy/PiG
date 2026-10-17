package inproc_test

import (
	"context"
	"fmt"
	"math/rand"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

type shortcutScenario struct {
	Table [][2]any         `json:"table"` // [action, keys] in Pi's Object.entries order
	Exts  []shortcutExtDef `json:"exts"`
}

type shortcutExtDef struct {
	Path string   `json:"path"`
	Keys []string `json:"keys"`
}

type shortcutOutcome struct {
	Shortcuts   [][3]string `json:"shortcuts"` // [key, extensionPath, description], in Map order
	Diagnostics [][3]string `json:"diagnostics"`
}

// runner.ts:673-716 (getShortcuts) and :119-138 (buildBuiltinKeybindings) against pinned Pi: which extension shortcuts survive the built-in
// table (reserved actions skip, other built-ins warn and yield), who wins an extension-to-extension conflict, and the diagnostics in order.
// The entries compare in Pi's Map iteration order. Not compared: the table order beyond tui.* before app.* with names sorted
// within each group (a Go map holds the resolved bindings, so that is the order the Runner assumes).
func TestRunnerShortcutsMatchPi(t *testing.T) {
	rng := rand.New(rand.NewSource(20261114))
	keyPool := []string{"ctrl+r", "Ctrl+R", "ctrl+shift+r", "shift+ctrl+r", "ctrl+c", "ctrl+p", "Ctrl+P", "ctrl+x", "ctrl+d", "alt+x", "f5", "escape", "enter", "ctrl+l", "ctrl+o", "ctrl+İ", "ctrl+i̇", "ctrl+ß", "CTRL+SS"}
	actionPool := []string{"app.interrupt", "app.clear", "app.exit", "app.suspend", "app.thinking.cycle", "app.model.select", "app.tools.expand", "app.message.copy", "app.clipboard.pasteImage", "app.session.new", "app.tree.fold", "tui.input.submit", "tui.select.confirm", "tui.select.up", "tui.select.cancel", "tui.input.copy", "tui.editor.deleteToLineEnd", "tui.editor.cursorUp", "tui.editor.undo"}
	scenarios := make([]shortcutScenario, 800)
	for i := range scenarios {
		sc := shortcutScenario{}
		var actions []string
		for _, a := range actionPool {
			if rng.Intn(3) == 0 {
				actions = append(actions, a)
			}
		}
		rank := func(a string) int {
			if strings.HasPrefix(a, "tui.") {
				return 0
			}
			return 1
		}
		slices.SortFunc(actions, func(a, b string) int {
			if rank(a) != rank(b) {
				return rank(a) - rank(b)
			}
			return strings.Compare(a, b)
		})
		for _, a := range actions {
			keys := []string{}
			for range rng.Intn(3) {
				keys = append(keys, keyPool[rng.Intn(len(keyPool))])
			}
			sc.Table = append(sc.Table, [2]any{a, keys})
		}
		for e := range rng.Intn(4) {
			ext := shortcutExtDef{Path: fmt.Sprintf("/ext/%d.ts", e)}
			for range 1 + rng.Intn(4) {
				key := keyPool[rng.Intn(len(keyPool))]
				if !slices.Contains(ext.Keys, key) {
					ext.Keys = append(ext.Keys, key)
				}
			}
			sc.Exts = append(sc.Exts, ext)
		}
		scenarios[i] = sc
	}
	var want []shortcutOutcome
	pioracle.Run(t, `
const { ExtensionRunner } = await load("pi-coding-agent/core/extensions/runner.js");
console.warn = () => {};
const out = [];
for (const sc of input) {
	const extensions = (sc.exts ?? []).map((spec) => ({ path: spec.path, resolvedPath: spec.path, handlers: new Map(), tools: new Map(), commands: new Map(), flags: new Map(), messageRenderers: new Map(), entryRenderers: new Map(),
		shortcuts: new Map((spec.keys ?? []).map((key) => [key, { shortcut: key, description: "d:" + spec.path + ":" + key, handler: async () => {}, extensionPath: spec.path }])) }));
	const runner = new ExtensionRunner(extensions, { flagValues: new Map(), pendingProviderRegistrations: [] }, "/cwd", {}, {});
	const table = {};
	for (const [action, keys] of (sc.table ?? [])) table[action] = keys;
	const shortcuts = runner.getShortcuts(table);
	out.push({
		shortcuts: [...shortcuts.entries()].map(([key, s]) => [key, s.extensionPath, s.description]),
		diagnostics: runner.getShortcutDiagnostics().map((d) => [d.type, d.message, d.path]),
	});
}
emit(out);`, scenarios, &want)

	failures := 0
	for i, sc := range scenarios {
		exts := make([]extension.Extension, len(sc.Exts))
		for ei, spec := range sc.Exts {
			exts[ei] = extension.Extension{Path: spec.Path, ResolvedPath: spec.Path}
			for _, key := range spec.Keys {
				exts[ei].SetRegisteredShortcut(extension.KeyID(key), extension.ExtensionShortcut{Shortcut: extension.KeyID(key), Description: "d:" + spec.Path + ":" + key, Handler: func(context.Context) error { return nil }, ExtensionPath: spec.Path})
			}
		}
		table := map[string][]string{}
		for _, row := range sc.Table {
			table[row[0].(string)] = row[1].([]string)
		}
		runner := inproc.NewRunner(exts, "/cwd")
		got := shortcutOutcome{Shortcuts: [][3]string{}, Diagnostics: [][3]string{}}
		for _, entry := range runner.Shortcuts(table) {
			got.Shortcuts = append(got.Shortcuts, [3]string{string(entry.Key), entry.Shortcut.ExtensionPath, entry.Shortcut.Description})
		}
		for _, d := range runner.ShortcutDiagnostics() {
			got.Diagnostics = append(got.Diagnostics, [3]string{string(d.Type), d.Message, d.Path})
		}
		w := want[i]
		if w.Shortcuts == nil {
			w.Shortcuts = [][3]string{}
		}
		if w.Diagnostics == nil {
			w.Diagnostics = [][3]string{}
		}
		if !reflect.DeepEqual(got, w) {
			failures++
			if failures <= 3 {
				t.Errorf("scenario %d %+v\n Go %+v\n Pi %+v", i, sc, got, w)
			}
		}
	}
	t.Logf("%d scenarios, %d failures", len(scenarios), failures)
}
