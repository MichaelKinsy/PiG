package codingagent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
	"github.com/MichaelKinsy/PiG/tui"
)

type appKeybindingsConflict struct {
	Key         string   `json:"key"`
	Keybindings []string `json:"keybindings"`
}

func appKeybindingsCandidates() []string {
	var out []string
	for c := range 0x20 {
		out = append(out, string(rune(c)))
	}
	out = append(out, "\x7f", "\x1b\x1b", "\x1b\r", "\x1b\x7f", "\x1b[Z", "\x1b[200~")
	for c := ' '; c <= '~'; c++ {
		out = append(out, string(c), "\x1b"+string(c))
	}
	for _, final := range []string{"A", "B", "C", "D", "H", "F", "P", "Q"} {
		out = append(out, "\x1b["+final, "\x1bO"+final)
		for mod := 2; mod <= 8; mod++ {
			out = append(out, fmt.Sprintf("\x1b[1;%d%s", mod, final), fmt.Sprintf("\x1b[1;%d:3%s", mod, final))
		}
	}
	for _, n := range []int{2, 3, 5, 6, 15, 17} {
		out = append(out, fmt.Sprintf("\x1b[%d~", n))
		for mod := 2; mod <= 8; mod++ {
			out = append(out, fmt.Sprintf("\x1b[%d;%d~", n, mod))
		}
	}
	codepoints := []int{9, 13, 27, 32, 127}
	for c := 'a'; c <= 'z'; c++ {
		codepoints = append(codepoints, int(c))
	}
	codepoints = append(codepoints, '/', '[', ']', '-', '=', ',', '.', '1', 57344)
	for _, cp := range codepoints {
		out = append(out, fmt.Sprintf("\x1b[%du", cp))
		for mod := 2; mod <= 9; mod++ {
			out = append(out, fmt.Sprintf("\x1b[%d;%du", cp, mod), fmt.Sprintf("\x1b[%d;%d:3u", cp, mod))
		}
		out = append(out, fmt.Sprintf("\x1b[27;5;%d~", cp), fmt.Sprintf("\x1b[27;2;%d~", cp))
	}
	return slices.Compact(out)
}

// core/keybindings.ts KeybindingsManager (the merged tui.* and app.* table) against pinned Pi: every action against raw terminal input
// under default and user-overridden bindings, with and without the Kitty protocol; the matched set, the resolved keys and the set of
// conflicts agree. Pi orders conflicts by the user's JSON key order, which Pig's map decoding does not keep, so conflicts compare sorted.
func TestAppKeybindingsManagerMatchesPi(t *testing.T) {
	candidates := appKeybindingsCandidates()
	bindingSets := []map[string][]string{
		nil,
		{"app.interrupt": {"ctrl+c"}, "app.clear": {"ctrl+c", "ctrl+l"}, "app.exit": {}},
		{"app.model.select": {"ctrl+m", "alt+m"}, "app.thinking.cycle": {"shift+tab", "ctrl+t"}, "app.tools.expand": {"ctrl+o", "f2"}},
		{"app.message.followUp": {"alt+enter", "ctrl+enter"}, "app.message.dequeue": {"alt+up"}, "tui.input.submit": {"enter", "ctrl+enter"}},
		{"app.session.new": {"ctrl+n"}, "app.session.tree": {"ctrl+n"}, "app.session.fork": {"ctrl+n", "ctrl+f"}, "tui.editor.cursorRight": {"ctrl+f"}},
		{"app.clipboard.pasteImage": {"ctrl+v", "alt+v", "f4"}, "app.editor.external": {"ctrl+g", "ctrl+e"}, "tui.editor.cursorLineEnd": {"end", "ctrl+e"}},
		{"not.an.action": {"ctrl+x"}, "app.exit": {"ctrl+x"}, "app.suspend": {"ctrl+x"}},
		{"app.models.save": {"enter"}, "app.models.reorderUp": {"alt+up", "ctrl+up"}, "app.models.reorderDown": {"alt+down"}, "app.tree.foldOrUp": {"left", "ctrl+left"}},
	}
	var probes []keybindingsProbe
	for _, kitty := range []bool{false, true} {
		for _, b := range bindingSets {
			probes = append(probes, keybindingsProbe{Kitty: kitty, Bindings: b})
		}
	}
	input, err := json.Marshal(map[string]any{"probes": probes, "candidates": candidates})
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/app_keybindings.mjs", pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected struct {
		Actions []string `json:"actions"`
		Results []struct {
			Matches   map[string][]int         `json:"matches"`
			Keys      map[string][]string      `json:"keys"`
			Conflicts []appKeybindingsConflict `json:"conflicts"`
		} `json:"results"`
	}
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	previousTUI, previousKitty := tui.GetTUIKeybindings(), tui.IsKittyProtocolActive()
	t.Cleanup(func() { tui.SetTUIKeybindings(previousTUI); tui.SetKittyProtocolActive(previousKitty) })
	failures, hits := 0, 0
	report := func(format string, args ...any) {
		failures++
		if failures <= 6 {
			t.Errorf(format, args...)
		}
	}
	for index, probe := range probes {
		tui.SetKittyProtocolActive(probe.Kitty)
		user := map[string][]KeyID{}
		for action, keys := range probe.Bindings {
			user[action] = []KeyID{}
			for _, key := range keys {
				user[action] = append(user[action], KeyID(key))
			}
		}
		manager := DefaultKeybindingsManager()
		manager.SetUserBindings(user)
		manager.syncToTUI() // tui.* actions live in the TUI manager, which the app manager feeds with the merged table and the user overrides
		matches := func(data, action string) bool {
			if strings.HasPrefix(action, "app.") {
				return manager.Matches(data, action)
			}
			return tui.GetTUIKeybindings().Matches(data, action)
		}
		keysOf := func(action string) []string {
			if strings.HasPrefix(action, "app.") {
				var keys []string
				for _, key := range manager.GetKeys(action) {
					keys = append(keys, string(key))
				}
				return keys
			}
			return tui.GetTUIKeybindings().GetKeys(action)
		}
		want := expected.Results[index]
		for _, action := range expected.Actions {
			var got []int
			for i, data := range candidates {
				if matches(data, action) {
					got = append(got, i)
				}
			}
			if got == nil {
				got = []int{}
			}
			hits += len(got)
			if !reflect.DeepEqual(got, want.Matches[action]) {
				var onlyPig, onlyPi []string
				for _, i := range got {
					if !slices.Contains(want.Matches[action], i) {
						onlyPig = append(onlyPig, fmt.Sprintf("%q", candidates[i]))
					}
				}
				for _, i := range want.Matches[action] {
					if !slices.Contains(got, i) {
						onlyPi = append(onlyPi, fmt.Sprintf("%q", candidates[i]))
					}
				}
				report("kitty=%v bindings=%v %s: only Pig %v; only Pi %v", probe.Kitty, probe.Bindings, action, onlyPig, onlyPi)
			}
			keys := keysOf(action)
			if !slices.Equal(keys, want.Keys[action]) && (len(keys) != 0 || len(want.Keys[action]) != 0) {
				report("kitty=%v bindings=%v %s keys: Pig %v, Pi %v", probe.Kitty, probe.Bindings, action, keys, want.Keys[action])
			}
		}
		var conflicts []appKeybindingsConflict
		for _, c := range manager.GetConflicts() {
			conflicts = append(conflicts, appKeybindingsConflict{Key: string(c.Key), Keybindings: c.Keybindings})
		}
		sortConflicts := func(list []appKeybindingsConflict) {
			for i := range list {
				slices.Sort(list[i].Keybindings)
			}
			slices.SortFunc(list, func(a, b appKeybindingsConflict) int { return strings.Compare(a.Key, b.Key) })
		}
		sortConflicts(conflicts)
		sortConflicts(want.Conflicts)
		if len(conflicts) != len(want.Conflicts) || (len(conflicts) > 0 && !reflect.DeepEqual(conflicts, want.Conflicts)) {
			report("kitty=%v bindings=%v conflicts: Pig %v, Pi %v", probe.Kitty, probe.Bindings, conflicts, want.Conflicts)
		}
	}
	if hits == 0 {
		t.Fatal("no candidate matched any action")
	}
	t.Logf("%d candidates x %d actions x %d probes, %d failures, %d Pi matches", len(candidates), len(expected.Actions), len(probes), failures, hits)
}

type keybindingsProbe struct {
	Kitty    bool                `json:"kitty"`
	Bindings map[string][]string `json:"bindings,omitempty"`
}
