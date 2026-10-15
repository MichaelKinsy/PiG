package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os/exec"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

type tuiKeybindingsConfigCase struct {
	First  map[string]any `json:"first"`
	Second map[string]any `json:"second"`
}

type tuiKeybindingsSnapshot struct {
	Resolved    map[string]any `json:"resolved"`
	Definitions map[string]struct {
		Description string `json:"description"`
		DefaultKeys any    `json:"defaultKeys"`
	} `json:"definitions"`
	Keys      map[string][]string `json:"keys"`
	Matches   map[string][]bool   `json:"matches"`
	Conflicts []struct {
		Key         string   `json:"key"`
		Keybindings []string `json:"keybindings"`
	} `json:"conflicts"`
	User map[string]any `json:"user"`
}

type tuiKeybindingsOracleResult struct {
	Before tuiKeybindingsSnapshot `json:"before"`
	After  tuiKeybindingsSnapshot `json:"after"`
	Inputs []string               `json:"inputs"`
}

func tuiKeybindingsCases() []tuiKeybindingsConfigCase {
	rng := rand.New(rand.NewPCG(19, 6))
	pick := func(items []string) string { return items[rng.IntN(len(items))] }
	ids := []string{"tui.editor.cursorUp", "tui.editor.cursorDown", "tui.editor.cursorLeft", "tui.editor.cursorRight", "tui.editor.cursorWordLeft", "tui.editor.deleteCharBackward", "tui.editor.deleteWordBackward", "tui.editor.deleteToLineStart", "tui.editor.yank", "tui.editor.undo", "tui.input.submit", "tui.input.tab", "tui.select.up", "tui.select.down", "tui.select.confirm", "tui.select.cancel", "tui.altScreen.search", "tui.unknown.action", "app.model.select", ""}
	keys := []string{"ctrl+a", "ctrl+y", "ctrl+l", "CTRL+L", "Ctrl+Y", " ctrl+x", "escape", "enter", "tab", "shift+tab", "up", "down", "alt+left", "ctrl+left", "backspace", "delete", "home", "end", "pageUp", "a", "", "ctrl+shift+p", "f5", "ctrl+u", "bogus+key"}
	build := func() map[string]any {
		config := map[string]any{}
		for range rng.IntN(7) {
			id := pick(ids)
			switch rng.IntN(4) {
			case 0:
				config[id] = pick(keys)
			case 1:
				config[id] = []string{}
			default:
				list := []string{}
				for range 1 + rng.IntN(3) {
					list = append(list, pick(keys))
				}
				config[id] = list
			}
		}
		return config
	}
	var cases []tuiKeybindingsConfigCase
	for range 400 {
		cases = append(cases, tuiKeybindingsConfigCase{First: build(), Second: build()})
	}
	return cases
}

func tuiKeybindingsToUser(config map[string]any) KeybindingsConfig {
	user := KeybindingsConfig{}
	for id, value := range config {
		switch v := value.(type) {
		case string:
			user[id] = []string{v}
		case []string:
			user[id] = slices.Clone(v)
		}
	}
	return user
}

func tuiKeybindingsSnapshotOf(manager *TUIKeybindingsManager, inputs []string) tuiKeybindingsSnapshot {
	snapshot := tuiKeybindingsSnapshot{Resolved: map[string]any{}, Keys: map[string][]string{}, Matches: map[string][]bool{}, User: map[string]any{}}
	snapshot.Definitions = map[string]struct {
		Description string `json:"description"`
		DefaultKeys any    `json:"defaultKeys"`
	}{}
	asValue := func(keys []string) any {
		if len(keys) == 1 {
			return keys[0]
		}
		return append([]string{}, keys...)
	}
	for id, keys := range manager.GetResolvedBindings() {
		snapshot.Resolved[id] = asValue(keys)
		definition, _ := manager.GetDefinition(id)
		snapshot.Definitions[id] = struct {
			Description string `json:"description"`
			DefaultKeys any    `json:"defaultKeys"`
		}{definition.Description, asValue(definition.DefaultKeys)}
		got := manager.GetKeys(id)
		if got == nil {
			got = []string{}
		}
		snapshot.Keys[id] = got
		for _, data := range inputs {
			snapshot.Matches[id] = append(snapshot.Matches[id], manager.Matches(data, id))
		}
	}
	for _, conflict := range manager.GetConflicts() {
		snapshot.Conflicts = append(snapshot.Conflicts, struct {
			Key         string   `json:"key"`
			Keybindings []string `json:"keybindings"`
		}{conflict.Key, conflict.Keybindings})
	}
	for id, keys := range manager.GetUserBindings() {
		snapshot.User[id] = append([]string{}, keys...)
	}
	if snapshot.Conflicts == nil {
		snapshot.Conflicts = []struct {
			Key         string   `json:"key"`
			Keybindings []string `json:"keybindings"`
		}{}
	}
	return snapshot
}

// KeybindingsManager of pi-tui (keybindings.ts) with the tui.* table against pinned Pi over 400 seeded pairs of user configs (the second applied with setUserBindings): single keys, arrays, empty arrays, duplicates, mixed case, spaces, unknown ids and keys; getResolvedBindings, getDefinition, getKeys, getConflicts, getUserBindings and which of 38 inputs match each keybinding must agree before and after.
func TestTUIKeybindingsManagerMatchesPi(t *testing.T) {
	cases := tuiKeybindingsCases()
	input, err := json.Marshal(cases)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/tui_keybindings.mjs", pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []tuiKeybindingsOracleResult
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	failures := 0
	for i, c := range cases {
		// The oracle is pi-tui's TUI_KEYBINDINGS, which has no per-platform defaults; coding-agent's Windows and WSL overrides are not part of it.
		manager := NewKeybindingsManager(TUIKeybindingDefinitionsFor(KeybindingPlatformLinux), tuiKeybindingsToUser(c.First))
		got := tuiKeybindingsOracleResult{Before: tuiKeybindingsSnapshotOf(manager, expected[i].Inputs)}
		manager.SetUserBindings(tuiKeybindingsToUser(c.Second))
		got.After = tuiKeybindingsSnapshotOf(manager, expected[i].Inputs)
		raw, _ := json.Marshal(got)
		var normalized tuiKeybindingsOracleResult
		_ = json.Unmarshal(raw, &normalized)
		normalized.Inputs = expected[i].Inputs
		// Pi lists conflicts in the order the user config claimed their keys and the claimants in config order; a Go map holds no such order (Pig lists by key and by id), so both sides are sorted.
		for _, snapshot := range []*tuiKeybindingsSnapshot{&normalized.Before, &normalized.After, &expected[i].Before, &expected[i].After} {
			for j := range snapshot.Conflicts {
				slices.Sort(snapshot.Conflicts[j].Keybindings)
			}
			slices.SortFunc(snapshot.Conflicts, func(a, b struct {
				Key         string   `json:"key"`
				Keybindings []string `json:"keybindings"`
			}) int {
				return strings.Compare(a.Key, b.Key)
			})
		}
		if reflect.DeepEqual(normalized, expected[i]) {
			continue
		}
		if failures++; failures > 4 {
			continue
		}
		report := fmt.Sprintf("case %d first %v second %v", i, c.First, c.Second)
		for _, part := range []struct {
			name      string
			got, want tuiKeybindingsSnapshot
		}{{"before", normalized.Before, expected[i].Before}, {"after", normalized.After, expected[i].After}} {
			for _, field := range []struct {
				name      string
				got, want any
			}{{"resolved", part.got.Resolved, part.want.Resolved}, {"definitions", part.got.Definitions, part.want.Definitions}, {"keys", part.got.Keys, part.want.Keys}, {"matches", part.got.Matches, part.want.Matches}, {"conflicts", part.got.Conflicts, part.want.Conflicts}, {"user", part.got.User, part.want.User}} {
				if !reflect.DeepEqual(field.got, field.want) {
					g, _ := json.Marshal(field.got)
					e, _ := json.Marshal(field.want)
					report += fmt.Sprintf("\n  %s %s\n  Pig %.600s\n  Pi  %.600s", part.name, field.name, g, e)
				}
			}
		}
		t.Error(report)
	}
	if failures > 4 {
		t.Errorf("%d of %d cases differ from Pi", failures, len(cases))
	}
}
