package codingagent

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
	"github.com/MichaelKinsy/PiG/tui"
)

type trustOracleSaved struct {
	Path     string `json:"path"`
	Decision bool   `json:"decision"`
}

type trustOracleProbe struct {
	Theme   string            `json:"theme"`
	Width   int               `json:"width"`
	Cwd     string            `json:"cwd"`
	Saved   *trustOracleSaved `json:"saved"`
	Trusted bool              `json:"trusted"`
	//portlint:allow emptydrop the oracle input treats an absent map and an empty map alike, so nothing observable depends on the difference
	Bindings map[string][]string `json:"bindings,omitempty"`
	Keys     []string            `json:"keys"`
}

type trustOracleResult struct {
	Steps  [][]string `json:"steps"`
	Frames [][]string `json:"frames"`
}

// trust-selector.ts handleInput against pinned Pi: up/down and k/j clamp (no wrap), confirm or LF selects the highlighted option's
// trust and updates, cancel cancels, other keys are ignored; the saved decision picks the initial row and the check mark.
func TestTrustSelectorInputMatchesPi(t *testing.T) {
	scripts := [][]string{
		{},
		{"\x1b[B", "\x1b[B", "\x1b[B", "\x1b[B", "\x1b[B", "\r"},
		{"\x1b[A", "\x1b[A", "\x1b[A", "\n"},
		{"j", "j", "k", "\r"},
		{"k", "k", "\x1b"},
		{"\x03"},
		{"x", "\x1b[6~", "\x1b[H", "\t", "\r"},
		{"\x1b[B", "\r", "\r"},
		{"\x1b[B", "\x1b[B", "\n"},
		{"\x0e", "\x10", "\r"},
	}
	bindings := []map[string][]string{
		nil,
		{"tui.select.up": {"w"}, "tui.select.down": {"s"}},
		{"tui.select.confirm": {"ctrl+y"}, "tui.select.cancel": {"q"}},
		{"tui.select.down": {"k"}, "tui.select.up": {"j"}},
		{"tui.select.cancel": {"enter"}},
	}
	saves := []*trustOracleSaved{
		nil,
		{Path: "/work/proj/sub", Decision: true},
		{Path: "/work/proj/sub", Decision: false},
		{Path: "/work/proj", Decision: true},
		{Path: "/work", Decision: false},
		{Path: "/elsewhere", Decision: true},
	}
	var probes []trustOracleProbe
	for _, theme := range []string{"dark", "light"} {
		for _, cwd := range []string{"/work/proj/sub", "/"} {
			for _, saved := range saves {
				for _, trusted := range []bool{true, false} {
					for _, b := range bindings {
						for _, keys := range scripts {
							probes = append(probes, trustOracleProbe{Theme: theme, Width: 80, Cwd: cwd, Saved: saved, Trusted: trusted, Bindings: b, Keys: keys})
						}
					}
				}
			}
		}
	}
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/trust_selector.mjs", pigversion.UpstreamVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []trustOracleResult
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	previousTheme, previousCaps, previousKeys, previousKitty := tui.ActiveTheme(), tui.GetCapabilities(), tui.GetTUIKeybindings(), tui.IsKittyProtocolActive()
	tui.SetKittyProtocolActive(false)
	t.Cleanup(func() {
		tui.SetCapabilities(previousCaps)
		tui.SetTheme(previousTheme.Name)
		tui.SetTUIKeybindings(previousKeys)
		tui.SetKittyProtocolActive(previousKitty)
	})
	selectionJSON := func(selection TrustSelection) string {
		updates := []map[string]any{}
		for _, u := range selection.Updates {
			var decision any
			if u.Decision != nil {
				decision = *u.Decision
			}
			updates = append(updates, map[string]any{"path": u.Path, "decision": decision})
		}
		out, _ := json.Marshal(map[string]any{"trusted": selection.Trusted, "updates": updates})
		return string(out)
	}
	canonical := func(events []string) [][]any {
		out := make([][]any, len(events))
		for i, event := range events {
			kind, payload, _ := strings.Cut(event, ":")
			var value any
			if payload != "" {
				_ = json.Unmarshal([]byte(payload), &value)
			}
			out[i] = []any{kind, value}
		}
		return out
	}
	failures := 0
	for i, probe := range probes {
		tui.SetCapabilities(tui.TerminalCapabilities{TrueColor: true})
		tui.SetTheme(probe.Theme)
		bindingMap := map[string][]KeyID{}
		for action, keys := range probe.Bindings {
			for _, key := range keys {
				bindingMap[action] = append(bindingMap[action], KeyID(key))
			}
		}
		km := DefaultKeybindingsManager()
		km.SetUserBindings(bindingMap)
		km.syncToTUI()
		var events []string
		var saved *ProjectTrustStoreEntry
		if probe.Saved != nil {
			saved = &ProjectTrustStoreEntry{Path: probe.Saved.Path, Decision: probe.Saved.Decision}
		}
		selector := NewTrustSelectorComponent(TrustSelectorOptions{Cwd: probe.Cwd, SavedDecision: saved, ProjectTrusted: probe.Trusted,
			OnSelect: func(s TrustSelection) { events = append(events, "select:"+selectionJSON(s)) },
			OnCancel: func() { events = append(events, "cancel") }})
		got := trustOracleResult{Steps: [][]string{{}}, Frames: [][]string{selector.Render(probe.Width)}}
		for _, key := range probe.Keys {
			selector.HandleInput(key)
			got.Steps = append(got.Steps, append([]string{}, events...))
			got.Frames = append(got.Frames, selector.Render(probe.Width))
		}
		stepsEqual := len(got.Steps) == len(expected[i].Steps)
		for s := range got.Steps {
			stepsEqual = stepsEqual && reflect.DeepEqual(canonical(got.Steps[s]), canonical(expected[i].Steps[s]))
		}
		if !stepsEqual || !reflect.DeepEqual(got.Frames, expected[i].Frames) {
			failures++
			if failures <= 6 {
				t.Errorf("probe %d keys=%q bindings=%v cwd=%q saved=%v trusted=%v:\nsteps = %q\nPi    = %q\n%s", i, probe.Keys, probe.Bindings, probe.Cwd, probe.Saved, probe.Trusted, got.Steps, expected[i].Steps, firstFrameDifference(got.Frames, expected[i].Frames))
			}
		}
	}
	if failures > 6 {
		t.Errorf("%d of %d probes differ from Pi", failures, len(probes))
	}
}
