package tui

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

type hintProbe struct {
	Theme       string              `json:"theme"`
	Key         string              `json:"key"`
	Action      string              `json:"action"`
	Description string              `json:"description"`
	Bindings    map[string][]string `json:"bindings,omitempty"`
}

type hintResult struct {
	Format     string `json:"format"`
	FormatCap  string `json:"formatCap"`
	Raw        string `json:"raw"`
	KeyText    string `json:"keyText"`
	KeyDisplay string `json:"keyDisplay"`
	Hint       string `json:"hint"`
}

// keybinding-hints.ts formatKeyText (split on "/" and "+", optional capitalization of the first UTF-16 unit of every part), rawKeyHint,
// keyText, keyDisplayText and keyHint against pinned Pi, with default, rebound, unbound and unusual key texts.
func TestKeybindingHintsMatchPi(t *testing.T) {
	keys := []string{"ctrl+c", "alt+enter", "ctrl+shift+p", "escape", "up/down", "ctrl+left/alt+left", "", "+", "ctrl++", "a//b", "é", "ctrl+é", "ß", "ǆ", "ﬁ", "😀", "😀+x", "𐐨", "ctrl+𐐨", "İ", "ΐ", "ALT+x", "Alt", "space", "shift+tab/ctrl+i"}
	// The app.* actions belong to the coding-agent manager, which this package does not register.
	actions := []string{"tui.select.confirm", "tui.editor.cursorLeft", "tui.input.submit", "no.such.action"}
	bindings := []map[string][]string{
		nil,
		{"tui.select.confirm": {}},
		{"tui.select.confirm": {"ctrl+é", "ß"}},
		{"tui.input.submit": {"ctrl+c", "alt+x", "ǆ"}},
		{"tui.editor.cursorLeft": {"😀"}},
	}
	var probes []hintProbe
	for _, theme := range []string{"dark", "light"} {
		for _, key := range keys {
			probes = append(probes, hintProbe{Theme: theme, Key: key, Action: "tui.select.confirm", Description: "to confirm"})
		}
		for _, action := range actions {
			for _, b := range bindings {
				probes = append(probes, hintProbe{Theme: theme, Key: "ctrl+x", Action: action, Description: "do it", Bindings: b})
			}
		}
	}
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/keybinding_hints.mjs", pigversion.UpstreamVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []hintResult
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	previousTheme, previousCaps := ActiveTheme(), GetCapabilities()
	// Pi runs without the Kitty keyboard protocol, so LF is Enter; an earlier test may have left it active.
	previousKitty := IsKittyProtocolActive()
	SetKittyProtocolActive(false)
	restoreKeybindingsAfterTest(t)
	t.Cleanup(func() {
		SetKittyProtocolActive(previousKitty)
		SetCapabilities(previousCaps)
		SetTheme(previousTheme.Name)
	})
	failures := 0
	for i, probe := range probes {
		SetCapabilities(TerminalCapabilities{TrueColor: true})
		SetTheme(probe.Theme)
		SetKeybindings(NewKeybindingsManager(TUIKeybindingDefinitionsFor(HostKeybindingPlatform()), probe.Bindings))
		got := hintResult{
			Format:     FormatKeyText(probe.Key, false),
			FormatCap:  FormatKeyText(probe.Key, true),
			Raw:        RawKeyHint(probe.Key, probe.Description),
			KeyText:    ActionKeyText(probe.Action),
			KeyDisplay: ActionKeyDisplayText(probe.Action),
			Hint:       KeyHint(ActionKeyText(probe.Action), probe.Description),
		}
		if got != expected[i] {
			if failures++; failures <= 6 {
				t.Errorf("probe %d %+v:\n  Pig %+v\n  Pi  %+v", i, probe, got, expected[i])
			}
		}
	}
	if failures > 6 {
		t.Errorf("%d of %d probes differ from Pi", failures, len(probes))
	}
}
