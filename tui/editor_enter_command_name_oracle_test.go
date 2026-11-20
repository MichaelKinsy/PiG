package tui_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// enterCommandNameProvider is the provider testdata/editor_enter_command_name.mjs defines for Pi: three commands, one of them with argument completions that start with "/", and file completion under base.
func enterCommandNameProvider(base string) func(*testing.T) tui.AutocompleteProvider {
	return func(t *testing.T) tui.AutocompleteProvider {
		return tui.NewCombinedProvider([]tui.SlashCommand{
			{Name: "model", Description: "Switch model"},
			{Name: "session", Description: "Session info"},
			{Name: "open", Description: "Open a file", GetArgumentCompletions: func(prefix string) []tui.AutocompleteItem {
				if strings.HasPrefix(prefix, "/") {
					return []tui.AutocompleteItem{{Value: "/tmp/alpha", Label: "alpha"}}
				}
				return nil
			}},
		}, base, "")
	}
}

// TestEditorEnterOnAMenuSubmitsOnlyACommandNameLikePi runs Pi main 9a7cfba47's three editor.test.ts cases and their neighbours (a relative path, Enter without Tab,
// a command name after a space or on a later line) through the oracle, which carries that commit, and Pig's editor: the text, the cursor, the submit and change
// callbacks and the rows agree.
func TestEditorEnterOnAMenuSubmitsOnlyACommandNameLikePi(t *testing.T) {
	base := t.TempDir()
	for _, name := range []string{"x/file-one.md", "x/file-two.md", "docs/file-one.md", "docs/file-two.md"} {
		path := filepath.Join(base, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	absolute := base + "/x/fi"
	typed := func(text string) []awaitedAcceptOp {
		var ops []awaitedAcceptOp
		for _, r := range text {
			ops = append(ops, awaitedAcceptOp{Keys: new(string(r))})
		}
		return ops
	}
	keys := func(k string) awaitedAcceptOp { return awaitedAcceptOp{Keys: new(k)} }
	setText := func(s string) awaitedAcceptOp { return awaitedAcceptOp{SetText: new(s)} }
	var probes []awaitedAcceptProbe
	for _, ops := range [][]awaitedAcceptOp{
		{setText("look at " + absolute), keys("\t"), keys("\r")},
		{setText("look at " + absolute), keys("\t"), keys("\x1b[B"), keys("\r")},
		{setText("look at docs/fi"), keys("\t"), keys("\r")},
		{setText("/open /tm"), keys("\t"), keys("\r")},
		append(typed("/open /tm"), keys("\r")),
		append(typed("/open /tm"), keys("\t"), keys("\r")),
		{setText("/mo"), keys("\t"), keys("\r")},
		append(typed("/mo"), keys("\r")),
		append(typed("/ses"), keys("\t"), keys("\r")),
		{setText("  /mo"), keys("\t"), keys("\r")},
		{setText("a\n/mo"), keys("\t"), keys("\r")},
	} {
		probes = append(probes, awaitedAcceptProbe{Base: base, Ops: ops})
	}
	compareAwaitedAcceptProbesWithPi(t, "editor_enter_command_name.mjs", probes, enterCommandNameProvider(base))
}
