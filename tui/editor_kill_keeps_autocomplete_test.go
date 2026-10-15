package tui

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// killKeepsAutocompleteCases drive each kill and word-delete command (Ctrl+U, Ctrl+K, Ctrl+W, Alt+Backspace, Alt+D) to a buffer for which a re-query
// returns no suggestion, so a refresh closes the list that Pi leaves open. Ctrl+A moves the cursor without updating the list in Pi (moveToLineStart).
var killKeepsAutocompleteCases = []struct {
	name string
	keys []string
}{
	{"delete to line start", []string{"\x15"}},
	{"delete to line end", []string{"\x01", "\x0b"}},
	{"delete word backward ctrl+w", []string{"\x17", "\x17"}},
	{"delete word backward alt+backspace", []string{"\x1b\x7f", "\x1b\x7f"}},
	{"delete word forward", []string{"\x01", "\x1bd"}},
}

// Upstream's kill and word-delete commands (deleteToStartOfLine, deleteToEndOfLine, deleteWordBackwards, deleteWordForward in editor.ts) change
// the text and call onChange only: an open autocomplete list stays until the next character, backspace, Delete or cursor move updates it
// (updateAutocomplete is called from insertCharacter, handleBackspace, handleForwardDelete and moveCursor). PiG refreshed the list after each
// of them, so Ctrl+U on a completed slash command closed the popup that Pi leaves open. Found by the skills journey (final frame diff).
func TestKillCommandsLeaveTheOpenAutocompleteList(t *testing.T) {
	for _, tc := range killKeepsAutocompleteCases {
		t.Run(tc.name, func(t *testing.T) {
			e := NewEditor()
			e.SetAutocomplete(NewSlashOnlyProvider([]SlashCommand{{Name: "review", Description: "Review a file"}}))
			for _, ch := range "/rev" {
				e.HandleInput(string(ch))
			}
			if !e.AutocompleteOpen() {
				t.Fatal("typing a slash command prefix did not open the list")
			}
			for _, key := range tc.keys {
				e.HandleInput(key)
			}
			if !e.AutocompleteOpen() {
				t.Fatalf("%s closed the autocomplete list (text %q); Pi leaves it open", tc.name, e.Text())
			}
		})
	}
}

// The same key sequences against the pinned Pi Editor (testdata/editor_autocomplete_render.mjs): text and rendered rows, popup included, match after
// every key.
func TestKillCommandsKeepAutocompleteMatchesPi(t *testing.T) {
	commands := []acCommand{{Name: "review", Description: "Review a file"}, {Name: "reload"}}
	var probes []acProbe
	for _, tc := range killKeepsAutocompleteCases {
		probe := acProbe{Theme: "dark", Commands: commands}
		for _, key := range append([]string{"/", "r", "e", "v"}, tc.keys...) {
			probe.Ops = append(probe.Ops, acOp{Keys: &key, Width: 40})
		}
		probes = append(probes, probe)
	}
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/editor_autocomplete_render.mjs", pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected [][]acFrame
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	previousCaps, previousTheme, previousKeys := GetCapabilities(), ActiveTheme(), GetKeybindings()
	t.Cleanup(func() {
		SetCapabilities(previousCaps)
		storeActiveTheme(previousTheme)
		SetKeybindings(previousKeys)
	})
	for i, probe := range probes {
		got := runACProbe(probe)
		last := len(expected[i]) - 1
		if len(expected[i][last].Rows) <= 3 {
			t.Fatalf("%s: the Pi oracle shows no popup after the last key, so the probe does not exercise the open list: %q", killKeepsAutocompleteCases[i].name, expected[i][last].Rows)
		}
		if !reflect.DeepEqual(got, expected[i]) {
			t.Errorf("%s:\n  Pig %q\n  Pi  %q", killKeepsAutocompleteCases[i].name, got, expected[i])
		}
	}
}
