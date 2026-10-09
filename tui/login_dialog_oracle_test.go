package tui

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

type loginDialogProbe struct {
	Theme  string `json:"theme"`
	Width  int    `json:"width"`
	Manual bool   `json:"manual"`
	//portlint:allow emptydrop the oracle input treats an absent map and an empty map alike, so nothing observable depends on the difference
	Bindings map[string][]string `json:"bindings,omitempty"`
	Keys     []string            `json:"keys"`
}

type loginDialogStep struct {
	Events  []string `json:"events"`
	Aborted bool     `json:"aborted"`
}

type loginDialogResult struct {
	Steps  []loginDialogStep `json:"steps"`
	Frames [][]string        `json:"frames"`
}

// login-dialog.ts handleInput against pinned Pi: the configured tui.select.cancel binding cancels (abort, onComplete, reject the
// pending input), every other key edits the prompt's Input, and the Input's submit (tui.input.submit or LF) resolves the pending
// value and replaces the field with the submitted text.
func TestLoginDialogInputMatchesPi(t *testing.T) {
	scripts := [][]string{
		{},
		{"a", "b", "\r"},
		{"a", "\x1b[D", "x", "\x17", "\r"},
		{"a", "\n"},
		{"a", "\x1b"},
		{"a", "\x03"},
		{"\x1b", "\x1b"},
		{"a", "\x13", "b", "\r"},
		{"\x18", "q", "\r"},
		{"\x1b[200~pasted\x1b[201~", "\r", "z"},
		{"\r", "a"},
	}
	bindings := []map[string][]string{
		nil,
		{KBSelectCancel: {"ctrl+x"}},
		{KBSelectConfirm: {"ctrl+s"}},
		{"tui.input.submit": {"ctrl+s"}},
		{KBSelectCancel: {"ctrl+s"}, "tui.input.submit": {"ctrl+s"}},
	}
	var probes []loginDialogProbe
	for _, theme := range []string{"dark", "light"} {
		for _, manual := range []bool{false, true} {
			for _, b := range bindings {
				for _, keys := range scripts {
					probes = append(probes, loginDialogProbe{Theme: theme, Width: 70, Manual: manual, Bindings: b, Keys: keys})
				}
			}
		}
	}
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/login_dialog.mjs", pigversion.UpstreamVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []loginDialogResult
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	previousBindings, previousTheme, previousCaps := GetKeybindings(), ActiveTheme(), GetCapabilities()
	t.Cleanup(func() {
		SetKeybindings(previousBindings)
		SetCapabilities(previousCaps)
		storeActiveTheme(previousTheme)
	})
	for i, probe := range probes {
		SetCapabilities(TerminalCapabilities{TrueColor: true})
		SetTheme(probe.Theme)
		SetKeybindings(NewKeybindingsManager(TUIKeybindingDefinitionsFor(HostKeybindingPlatform()), probe.Bindings))
		var events []string
		dialog := NewLoginDialogComponent(nil, "anthropic", func(bool, string) { (func() { events = append(events, "complete:false:Login cancelled") })() }, "")
		dialog.SetFocused(true)
		var pending <-chan string
		if probe.Manual {
			pending = dialog.ShowManualInput("Paste code")
		} else {
			pending = dialog.ShowPrompt("Enter key", "sk-...")
		}
		got := loginDialogResult{Steps: []loginDialogStep{}, Frames: [][]string{dialog.Render(probe.Width)}}
		for _, key := range probe.Keys {
			dialog.HandleInput(key)
			if pending != nil {
				select {
				case value, ok := <-pending:
					if ok {
						events = append(events, "submit:"+value)
					} else {
						events = append(events, "reject:Login cancelled")
					}
					pending = nil
				default:
				}
			}
			got.Steps = append(got.Steps, loginDialogStep{Events: append([]string{}, events...), Aborted: dialog.Cancelled()})
			got.Frames = append(got.Frames, dialog.Render(probe.Width))
		}
		if !reflect.DeepEqual(got.Steps, expected[i].Steps) {
			t.Errorf("probe %d %+v: steps = %+v; Pi = %+v", i, probe, got.Steps, expected[i].Steps)
		}
		if !reflect.DeepEqual(got.Frames, expected[i].Frames) {
			t.Errorf("probe %d %+v:\n%s", i, probe, oracleFrameDifference(got.Frames, expected[i].Frames))
		}
	}
}
