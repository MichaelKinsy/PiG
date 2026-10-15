package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"os/exec"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

type keybindingsOracleProbe struct {
	Kitty    bool                `json:"kitty"`
	Bindings map[string][]string `json:"bindings,omitempty"`
}

type keybindingsOracleResult struct {
	Matches   map[string][]int    `json:"matches"`
	Keys      map[string][]string `json:"keys"`
	Conflicts []struct {
		Key         string   `json:"key"`
		Keybindings []string `json:"keybindings"`
	} `json:"conflicts"`
}

func keybindingsOracleCandidates() []string {
	var out []string
	for c := range 0x20 {
		out = append(out, string(rune(c)))
	}
	out = append(out, "\x7f", "\x1b\x1b", "\x1b\r", "\x1b\n", "\x1b\x7f", "\x1b\x08", "\x1b[Z", "\x1b[200~")
	for c := ' '; c <= '~'; c++ {
		out = append(out, string(c), "\x1b"+string(c))
	}
	out = append(out, "é", "ß", "Я", "日")
	finals := []string{"A", "B", "C", "D", "H", "F", "P", "Q", "R", "S"}
	for _, final := range finals {
		out = append(out, "\x1b["+final, "\x1bO"+final)
		for mod := 2; mod <= 8; mod++ {
			out = append(out, fmt.Sprintf("\x1b[1;%d%s", mod, final), fmt.Sprintf("\x1b[1;%d:3%s", mod, final))
		}
	}
	for _, n := range []int{1, 2, 3, 4, 5, 6, 7, 8, 11, 12, 13, 14, 15, 17, 18, 19, 20, 21, 23, 24} {
		out = append(out, fmt.Sprintf("\x1b[%d~", n))
		for mod := 2; mod <= 8; mod++ {
			out = append(out, fmt.Sprintf("\x1b[%d;%d~", n, mod))
		}
	}
	codepoints := []int{9, 13, 27, 32, 127, 8, 10}
	for c := 'a'; c <= 'z'; c++ {
		codepoints = append(codepoints, int(c))
	}
	codepoints = append(codepoints, '0', '1', '9', '/', '[', ']', '\\', '-', '=', ',', '.', ';', '\'', '`', '1', 57344, 57399, 57414)
	for _, cp := range codepoints {
		out = append(out, fmt.Sprintf("\x1b[%du", cp))
		for mod := 2; mod <= 9; mod++ {
			out = append(out, fmt.Sprintf("\x1b[%d;%du", cp, mod), fmt.Sprintf("\x1b[%d;%d:3u", cp, mod), fmt.Sprintf("\x1b[%d;%d:2u", cp, mod))
		}
		out = append(out, fmt.Sprintf("\x1b[%d;5;13~", cp), fmt.Sprintf("\x1b[27;5;%d~", cp), fmt.Sprintf("\x1b[27;2;%d~", cp), fmt.Sprintf("\x1b[%d:%d:%d;5u", cp, cp, cp), "\x1b[1089::99;5u")
	}
	return slices.Compact(out)
}

// keybindings.ts KeybindingsManager.matches against pinned pi-tui: every TUI action against raw terminal input (control bytes, alt
// chords, CSI/SS3 cursor and function keys with modifiers and release events, CSI-u with alternates, modifyOtherKeys) under default
// and user-overridden bindings, with and without the Kitty protocol; the matched set, the resolved keys and the conflicts agree.
func TestKeybindingsManagerMatchesPi(t *testing.T) {
	candidates := keybindingsOracleCandidates()
	bindingSets := []map[string][]string{
		nil,
		{"tui.select.confirm": {"ctrl+j", "shift+enter"}, "tui.editor.undo": {"ctrl+z"}},
		{"tui.editor.cursorLeft": {"ctrl+b", "left"}, "tui.editor.cursorRight": {"ctrl+b"}, "tui.select.cancel": {}},
		{"tui.input.tab": {"ctrl+i", "alt+tab"}, "tui.input.submit": {"enter", "ctrl+enter"}, "tui.input.newLine": {"shift+enter", "ctrl+j"}},
		{"tui.select.up": {"alt+k", "ctrl+shift+up"}, "tui.select.down": {"alt+j"}, "tui.editor.pageUp": {"f5"}, "tui.editor.pageDown": {"ctrl+f5"}},
		{"tui.editor.deleteWordBackward": {"alt+backspace", "ctrl+w", "ctrl+h"}, "tui.editor.deleteCharBackward": {"backspace", "ctrl+h"}},
		{"not.an.action": {"ctrl+x"}, "tui.select.confirm": {"ctrl+x"}, "tui.editor.yank": {"ctrl+x"}},
		{"tui.editor.cursorLineStart": {"home", "ctrl+a", "super+left"}, "tui.editor.cursorLineEnd": {"end", "ctrl+e", "super+right"}},
		{"tui.altScreen.search": {"ctrl+shift+f", "f3"}, "tui.altScreen.top": {"ctrl+home", "g"}},
	}
	var probes []keybindingsOracleProbe
	for _, kitty := range []bool{false, true} {
		for _, b := range bindingSets {
			probes = append(probes, keybindingsOracleProbe{Kitty: kitty, Bindings: b})
		}
	}
	input, err := json.Marshal(map[string]any{"probes": probes, "candidates": candidates})
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/keybindings_matches.mjs", pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected struct {
		Actions []string                  `json:"actions"`
		Results []keybindingsOracleResult `json:"results"`
	}
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	previousKitty := IsKittyProtocolActive()
	t.Cleanup(func() { SetKittyProtocolActive(previousKitty) })
	failures, hits := 0, 0
	for index, probe := range probes {
		SetKittyProtocolActive(probe.Kitty)
		user := KeybindingsConfig{}
		maps.Copy(user, probe.Bindings)
		manager := NewTUIKeybindingsManager(user)
		want := expected.Results[index]
		for _, action := range expected.Actions {
			var got []int
			for i, data := range candidates {
				if manager.Matches(data, action) {
					got = append(got, i)
				}
			}
			if got == nil {
				got = []int{}
			}
			hits += len(got)
			if !reflect.DeepEqual(got, want.Matches[action]) {
				failures++
				if failures <= 6 {
					diff := func(a, b []int) []string {
						var out []string
						for _, i := range a {
							if !slices.Contains(b, i) {
								out = append(out, fmt.Sprintf("%q", candidates[i]))
							}
						}
						return out
					}
					t.Errorf("kitty=%v bindings=%v %s: only Pig %v; only Pi %v", probe.Kitty, probe.Bindings, action, diff(got, want.Matches[action]), diff(want.Matches[action], got))
				}
			}
			if keys := manager.GetKeys(action); !slices.Equal(keys, want.Keys[action]) && (len(keys) != 0 || len(want.Keys[action]) != 0) {
				failures++
				if failures <= 6 {
					t.Errorf("kitty=%v bindings=%v %s keys: Pig %v, Pi %v", probe.Kitty, probe.Bindings, action, keys, want.Keys[action])
				}
			}
		}
		var conflicts []struct {
			Key         string   `json:"key"`
			Keybindings []string `json:"keybindings"`
		}
		for _, c := range manager.GetConflicts() {
			conflicts = append(conflicts, struct {
				Key         string   `json:"key"`
				Keybindings []string `json:"keybindings"`
			}{c.Key, c.Keybindings})
		}
		// Pi lists conflicts in the order of the user's JSON object keys; Pig decodes them into a map, so it reports them sorted by key and
		// action. The set of conflicts must agree.
		slices.SortFunc(want.Conflicts, func(a, b struct {
			Key         string   `json:"key"`
			Keybindings []string `json:"keybindings"`
		}) int {
			return strings.Compare(a.Key, b.Key)
		})
		for i := range want.Conflicts {
			slices.Sort(want.Conflicts[i].Keybindings)
		}
		slices.SortFunc(conflicts, func(a, b struct {
			Key         string   `json:"key"`
			Keybindings []string `json:"keybindings"`
		}) int {
			return strings.Compare(a.Key, b.Key)
		})
		if len(conflicts) != len(want.Conflicts) || (len(conflicts) > 0 && !reflect.DeepEqual(conflicts, want.Conflicts)) {
			failures++
			if failures <= 6 {
				t.Errorf("kitty=%v bindings=%v conflicts: Pig %v, Pi %v", probe.Kitty, probe.Bindings, conflicts, want.Conflicts)
			}
		}
	}
	if hits == 0 {
		t.Fatal("no candidate matched any action")
	}
	t.Logf("%d candidates x %d actions x %d probes, %d failures, %d Pi matches", len(candidates), len(expected.Actions), len(probes), failures, hits)
}
