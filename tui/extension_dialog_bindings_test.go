package tui

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"reflect"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

type dialogProbe struct {
	Kind      string              `json:"kind"`
	Title     string              `json:"title,omitempty"`
	Timeout   int                 `json:"timeout,omitempty"`
	Theme     string              `json:"theme"`
	TrueColor bool                `json:"trueColor"`
	Width     int                 `json:"width"`
	Bindings  map[string][]string `json:"bindings"`
	Options   []string            `json:"options"`
	Keys      []string            `json:"keys"`
	//portlint:allow emptydrop the oracle input treats an absent provider list and an empty one alike
	Providers     []map[string]any `json:"providers,omitempty"`
	InitialSearch string           `json:"initialSearch,omitempty"`
}

type dialogProbeState struct {
	Done      bool   `json:"done"`
	Cancelled bool   `json:"cancelled"`
	Value     string `json:"value"`
	Toggles   int    `json:"toggles"`
}

type dialogProbeResult struct {
	States []dialogProbeState `json:"states"`
	Frames [][]string         `json:"frames"`
}

// Pi 0.87.1 extension-selector.ts:74-124 and extension-input.ts:84-104:
// resolve hints/actions through the manager, expand before navigation before
// confirm before cancel, never select an empty option, and intercept select
// confirm before delegating to Input (whose independent submit has no callback).
func TestExtensionDialogsMatchPiBindingsAndPalette(t *testing.T) {
	cases := []dialogProbe{
		{Kind: "select", Options: []string{"first", "second"}, Keys: []string{"j", "j", "k", "\r"}},
		{Kind: "select", Options: []string{}, Keys: []string{"j", "\r", "k", "\r", "\x1b"}},
		{Kind: "select", Options: []string{"", "second"}, Keys: []string{"\r", "j", "\r"}},
		{Kind: "select", Options: []string{"first", "second"}, Bindings: map[string][]string{"app.tools.expand": {"ctrl+e"}}, Keys: []string{"\x0f", "\x05", "\x1b[101;5u", "\r"}},
		{Kind: "select", Options: []string{"first"}, Bindings: map[string][]string{"app.tools.expand": {}}, Keys: []string{"\x0f", "\r"}},
		{Kind: "select", Options: []string{"first"}, Bindings: map[string][]string{"app.tools.expand": {"ctrl+e"}, KBSelectCancel: {"ctrl+e"}}, Keys: []string{"\x05", "\r"}},
		{Kind: "select", Options: []string{"first", "second"}, Bindings: map[string][]string{KBSelectDown: {"ctrl+x"}, KBSelectCancel: {"ctrl+x"}}, Keys: []string{"\x18", "\r"}},
		{Kind: "select", Options: []string{"first"}, Bindings: map[string][]string{KBSelectConfirm: {"ctrl+s", "alt+s"}, KBSelectCancel: {"ctrl+s", "ctrl+q"}}, Keys: []string{"\x13"}},
		{Kind: "input", Keys: []string{"hello", "\r"}},
		{Kind: "input", Bindings: map[string][]string{KBSelectConfirm: {"ctrl+s", "alt+s"}, KBSelectCancel: {"ctrl+q"}}, Keys: []string{"hello", "\r", "\x13"}},
		{Kind: "input", Bindings: map[string][]string{KBInputSubmit: {"ctrl+s"}}, Keys: []string{"hello", "\x13", "\r"}},
		{Kind: "input", Bindings: map[string][]string{KBSelectConfirm: {}, KBSelectCancel: {"ctrl+q"}}, Keys: []string{"hello", "\r", "\x1b[113;5u"}},
		{Kind: "input", Bindings: map[string][]string{KBSelectConfirm: {"ctrl+s"}, KBSelectCancel: {"ctrl+s"}}, Keys: []string{"hello", "\x13"}},
		{Kind: "input", Bindings: map[string][]string{KBSelectConfirm: {}}, Keys: []string{"hello", "\n"}},
	}
	var probes []dialogProbe
	for _, theme := range []string{"dark", "light"} {
		for _, width := range []int{40, 100} {
			for _, probe := range cases {
				probe.Theme, probe.Width = theme, width
				for _, trueColor := range []bool{false, true} {
					probe.TrueColor = trueColor
					probes = append(probes, probe)
				}
			}
		}
	}
	expected := piDialogOracle(t, probes)
	previousBindings, previousTheme, previousCaps := GetKeybindings(), ActiveTheme(), GetCapabilities()
	t.Cleanup(func() {
		SetKeybindings(previousBindings)
		SetCapabilities(previousCaps)
		storeActiveTheme(previousTheme)
	})
	for i, probe := range probes {
		SetCapabilities(TerminalCapabilities{TrueColor: probe.TrueColor})
		SetTheme(probe.Theme)
		definitions := TUIKeybindingDefinitionsFor(HostKeybindingPlatform())
		definitions["app.tools.expand"] = TUIKeybindingDef{DefaultKeys: []string{"ctrl+o"}}
		restoreKeybindingsAfterTest(t)
		SetKeybindings(NewKeybindingsManager(definitions, probe.Bindings))
		toggles := 0
		selector := NewExtensionSelectorComponent("Rigidity probe", probe.Options, nil, nil, ExtensionSelectorOptions{OnToggleToolsExpanded: func() { toggles++ }})
		field := NewExtensionInputComponent("Rigidity probe", "", nil, nil)
		render, handle := selector.Render, selector.HandleInput
		done, cancelled, value := selector.Done, selector.Cancelled, selector.SelectedValue
		if probe.Kind == "input" {
			render, handle = field.Render, field.HandleInput
			done, cancelled, value = field.Done, field.Cancelled, field.Text
		}
		// Compare every owned content row, including ANSI; DynamicBorder owns the outer rows.
		renderContent := func() []string {
			rows := render(probe.Width)
			return rows[2 : len(rows)-2]
		}
		got := dialogProbeResult{Frames: [][]string{renderContent()}}
		for _, key := range probe.Keys {
			handle(key)
			state := dialogProbeState{Done: done(), Cancelled: cancelled(), Toggles: toggles}
			if state.Done && !state.Cancelled {
				state.Value = value()
			}
			got.States = append(got.States, state)
			if state.Done {
				break
			}
			got.Frames = append(got.Frames, renderContent())
		}
		if !reflect.DeepEqual(got.States, expected[i].States) {
			t.Errorf("probe %d %+v: states = %+v; Pi = %+v", i, probe, got.States, expected[i].States)
		}
		if !reflect.DeepEqual(got.Frames, expected[i].Frames) {
			t.Errorf("probe %d %+v: frames = %q; Pi = %q", i, probe, got.Frames, expected[i].Frames)
		}
	}
}

// Pi 0.87.1 countdown-timer.ts and the timeout branches of
// extension-selector.ts:56-63 and extension-input.ts:60-67: the title shows
// `${title} (${s}s)` from ceil(timeout/1000), each interval callback lowers
// it, and the callback that reaches zero cancels the dialog. "<tick>" runs one
// interval callback; the Go side runs NewCountdownTimer's own ticker in a
// synctest bubble with a dispatch that hands each second to the test.
func TestExtensionDialogCountdownMatchesPi(t *testing.T) {
	cases := []dialogProbe{
		{Kind: "select", Options: []string{"first", "second"}, Timeout: 1500, Keys: []string{"<tick>", "j", "<tick>"}},
		{Kind: "select", Options: []string{"first", "second"}, Timeout: 3000, Keys: []string{"<tick>", "j", "\r"}},
		{Kind: "select", Title: "Timed\nSure?", Options: []string{"Yes", "No"}, Timeout: 1000, Keys: []string{"<tick>"}},
		{Kind: "select", Options: []string{"first"}, Timeout: 999, Keys: []string{"\x1b"}},
		{Kind: "input", Timeout: 2000, Keys: []string{"abc", "<tick>", "d", "<tick>"}},
		{Kind: "input", Timeout: 1, Keys: []string{"abc", "\r"}},
	}
	var probes []dialogProbe
	for _, theme := range []string{"dark", "light"} {
		for _, width := range []int{12, 100} {
			for _, probe := range cases {
				probe.Theme, probe.Width, probe.TrueColor = theme, width, true
				probes = append(probes, probe)
			}
		}
	}
	expected := piDialogOracle(t, probes)
	previousBindings, previousTheme, previousCaps := GetKeybindings(), ActiveTheme(), GetCapabilities()
	restoreKeybindingsAfterTest(t)
	t.Cleanup(func() {
		SetKeybindings(previousBindings)
		SetCapabilities(previousCaps)
		storeActiveTheme(previousTheme)
	})
	for i, probe := range probes {
		SetCapabilities(TerminalCapabilities{TrueColor: probe.TrueColor})
		SetTheme(probe.Theme)
		var got dialogProbeResult
		synctest.Test(t, func(t *testing.T) {
			title := probe.Title
			if title == "" {
				title = "Rigidity probe"
			}
			selector := NewExtensionSelectorComponent(title, probe.Options, nil, nil)
			field := NewExtensionInputComponent(title, "", nil, nil)
			render, handle := selector.Render, selector.HandleInput
			done, cancelled, value := selector.Done, selector.Cancelled, selector.SelectedValue
			tick, expire := selector.SetCountdown, selector.Cancel
			if probe.Kind == "input" {
				render, handle = field.Render, field.HandleInput
				done, cancelled, value = field.Done, field.Cancelled, field.Text
				tick, expire = field.SetCountdown, field.Cancel
			}
			seconds := make(chan func(), 1)
			timer := NewCountdownTimer(time.Duration(probe.Timeout)*time.Millisecond, func(second func()) { seconds <- second }, tick, expire)
			defer timer.Dispose()
			renderContent := func() []string {
				rows := render(probe.Width)
				return rows[2 : len(rows)-2]
			}
			got.Frames = [][]string{renderContent()}
			for _, key := range probe.Keys {
				if key == "<tick>" {
					time.Sleep(time.Second)
					synctest.Wait()
					(<-seconds)()
				} else {
					handle(key)
				}
				state := dialogProbeState{Done: done(), Cancelled: cancelled()}
				if state.Done && !state.Cancelled {
					state.Value = value()
				}
				got.States = append(got.States, state)
				if state.Done {
					break
				}
				got.Frames = append(got.Frames, renderContent())
			}
		})
		if !reflect.DeepEqual(got, expected[i]) {
			t.Errorf("probe %d %+v:\n states %+v; Pi %+v\n frames %q\n Pi     %q", i, probe, got.States, expected[i].States, got.Frames, expected[i].Frames)
		}
	}
}

// piDialogOracle runs probes through Pi's own dialog components.
func piDialogOracle(t *testing.T, probes []dialogProbe) []dialogProbeResult {
	t.Helper()
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/extension_dialogs.mjs", pigversion.UpstreamVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []dialogProbeResult
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	if len(expected) != len(probes) {
		t.Fatalf("oracle returned %d results for %d inputs", len(expected), len(probes))
	}
	return expected
}

func BenchmarkExtensionDialogRemapping(b *testing.B) {
	previous := GetKeybindings()
	SetKeybindings(NewTUIKeybindingsManager(map[string][]string{KBSelectConfirm: {"ctrl+s"}}))
	b.Cleanup(func() { SetKeybindings(previous) })
	options := []string{"first", "second", "third"}
	b.ReportAllocs()
	for b.Loop() {
		selector := NewExtensionSelectorComponent("Pick a task", options, nil, nil)
		selector.HandleInput("j")
		selector.Render(100)
		selector.HandleInput("\x13")
		input := NewExtensionInputComponent("Task name", "", nil, nil)
		input.HandleInput("first")
		input.HandleInput("\r")
		input.HandleInput("-second")
		input.Render(100)
		input.HandleInput("\x13")
	}
}
