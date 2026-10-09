package codingagent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
	"github.com/MichaelKinsy/PiG/tui"
)

type submenuProbe struct {
	Kind           string            `json:"kind"`
	Theme          string            `json:"theme"`
	Width          int               `json:"width"`
	Description    string            `json:"description"`
	Count          int               `json:"count"`
	Current        string            `json:"current"`
	Searchable     bool              `json:"searchable"`
	Preselect      bool              `json:"preselect"`
	SearchableStep int               `json:"searchableStep"`
	StepCount      int               `json:"stepCount"`
	Start          int               `json:"start"`
	Initial        map[string]string `json:"initial"`
	Loop           bool              `json:"loop"`
	//portlint:allow emptydrop the oracle input treats an absent map and an empty map alike, so nothing observable depends on the difference
	Bindings map[string][]string `json:"bindings,omitempty"`
	Keys     []string            `json:"keys"`
}

type submenuResult struct {
	Steps  [][]string `json:"steps"`
	Frames [][]string `json:"frames"`
}

func submenuOracleItems(n int, tag string) []tui.SelectItem {
	items := make([]tui.SelectItem, n)
	for i := range items {
		items[i] = tui.SelectItem{Value: fmt.Sprintf("%s%d", tag, i), Label: fmt.Sprintf("Option %s %d %s", tag, i, []string{"alpha", "beta", "gamma"}[i%3])}
		if i%2 == 1 {
			who := "x"
			if i%3 == 0 {
				who = "y"
			}
			items[i].Description = fmt.Sprintf("desc %s %d", who, i)
		}
	}
	return items
}

// settings-submenu.ts against pinned Pi: SelectSubmenu (optional fuzzy search, nav keys go to the list, other keys edit the search
// and rebuild the list; selection-change, select and cancel callbacks) and SteppedSubmenu (Esc goes back a step, the last step
// completes then cancels or loops, startAtStep/initialContext, context-dependent options and preselect). Every frame is compared byte for byte, colours included.
func TestSettingsSubmenusMatchPi(t *testing.T) {
	selectScripts := [][]string{
		{},
		{"\x1b[B", "\r"},
		{"\x1b[A", "\x1b[A", "\r"},
		{"\x1b[B", "\x1b[B", "\x1b[B", "\x1b[B", "\x1b[B", "\x1b[B", "\x1b[B", "\x1b[B", "\x1b[B", "\x1b[B", "\x1b[B", "\x1b[B", "\x1b[B", "\r"},
		{"\x1b[6~", "\x1b[5~", "\r"},
		{"\x1b"},
		{"b", "e", "\x1b[B", "\r"},
		{"a", "l", "p", "\x7f", "\x7f", "\x7f", "\x7f", "\r"},
		{"z", "z", "z", "\r", "\x1b[A", "\x1b[B"},
		{"d", "e", "s", "c", " ", "y", "\r"},
		{"\x1b[B", "x", "\x1b[A", "\x1b[A", "\n"},
		{"\x1b[B", "\x1b", "\x1b[B"},
		{"\x19", "\x19"},
	}
	steppedScripts := [][]string{
		{},
		{"\r", "\r", "\r"},
		{"\r", "\r", "\r", "\r", "\r", "\r"},
		{"\x1b"},
		{"\r", "\x1b", "\x1b", "\x1b"},
		{"\x1b[B", "\r", "\x1b[B", "\x1b[B", "\r", "\x1b[A", "\r"},
		{"\r", "\r", "\x1b", "\x1b[B", "\r", "\r"},
		{"b", "e", "\r", "\x1b[A", "\r", "g", "\r"},
		{"\r", "b", "e", "\r", "\r", "\x1b", "\x1b", "\r"},
	}
	bindings := []map[string][]string{
		nil,
		{"tui.select.up": {"w"}, "tui.select.down": {"s"}},
		{"tui.select.cancel": {"q"}, "tui.select.confirm": {"ctrl+y"}},
		{"tui.select.cancel": {"down"}, "tui.select.up": {"escape"}},
		{"tui.select.confirm": {"up"}, "tui.select.cancel": {"ctrl+y"}, "tui.select.down": {"ctrl+y"}},
		{"tui.select.confirm": {"ctrl+y"}, "tui.select.cancel": {"ctrl+y"}},
	}
	var probes []submenuProbe
	for _, theme := range []string{"dark", "light"} {
		for _, b := range bindings {
			for _, count := range []int{0, 1, 3, 12} {
				for _, searchable := range []bool{false, true} {
					for _, current := range []string{"", "v2"} {
						for _, desc := range []string{"", "Choose carefully"} {
							for _, keys := range selectScripts {
								probes = append(probes, submenuProbe{Kind: "select", Theme: theme, Width: 70, Description: desc, Count: count, Current: current, Searchable: searchable, Bindings: b, Keys: keys, SearchableStep: -1})
							}
						}
					}
				}
			}
			for _, count := range []int{1, 3, 12} {
				for _, stepCount := range []int{1, 3} {
					for _, start := range []int{0, 1} {
						if start >= stepCount {
							continue
						}
						for _, loop := range []bool{false, true} {
							for _, searchableStep := range []int{-1, 1} {
								for _, preselect := range []bool{false, true} {
									var initial map[string]string
									if start == 1 {
										initial = map[string]string{"k0": "seed"}
									}
									for _, keys := range steppedScripts {
										probes = append(probes, submenuProbe{Kind: "stepped", Theme: theme, Width: 70, Count: count, StepCount: stepCount, Start: start, Initial: initial, Loop: loop, SearchableStep: searchableStep, Preselect: preselect, Bindings: b, Keys: keys})
									}
								}
							}
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
	cmd := exec.CommandContext(t.Context(), "node", "testdata/settings_submenu.mjs", pigversion.UpstreamVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []submenuResult
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	previousTheme, previousCaps, previousKitty := tui.ActiveTheme(), tui.GetCapabilities(), tui.IsKittyProtocolActive()
	tui.SetKittyProtocolActive(false)
	t.Cleanup(func() {
		tui.SetCapabilities(previousCaps)
		tui.SetTheme(previousTheme.Name)
		tui.SetKittyProtocolActive(previousKitty)
	})
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
		useKeybindings(t, bindingMap)
		var events []string
		var component tui.Component
		var handle func(string)
		if probe.Kind == "select" {
			submenu := tui.NewSelectSubmenu("Pick one", probe.Description, submenuOracleItems(probe.Count, "v"), probe.Current, tui.SelectSubmenuOptions{Searchable: probe.Searchable})
			step := newSelectSubmenuStep(submenu,
				func(value string) { events = append(events, "select:"+value) },
				func() { events = append(events, "cancel") },
				func(value string) { events = append(events, "change:"+value) })
			component, handle = step, step.HandleInput
		} else {
			var steps []SteppedSubmenuStep
			for s := range probe.StepCount {
				steps = append(steps, SteppedSubmenuStep{
					Key:         fmt.Sprintf("k%d", s),
					Title:       func(ctx map[string]string) string { return fmt.Sprintf("Title %d %d", s, len(ctx)) },
					Description: func(ctx map[string]string) string { return fmt.Sprintf("Desc %d %s", s, submenuContextJSON(ctx)) },
					Options: func(ctx map[string]string) []tui.SelectItem {
						return submenuOracleItems(probe.Count+s, fmt.Sprintf("s%d%d_", s, len(ctx)))
					},
					Preselect: func(ctx map[string]string) string {
						if probe.Preselect {
							return fmt.Sprintf("s%d%d_1", s, len(ctx))
						}
						return ""
					},
					Layout: tui.SelectSubmenuOptions{Searchable: probe.SearchableStep == s},
				})
			}
			menu := NewSteppedSubmenu(steps,
				func(ctx map[string]string) { events = append(events, "complete:"+submenuContextJSON(ctx)) },
				func() { events = append(events, "cancel") },
				SteppedSubmenuOptions{StartAtStep: probe.Start, InitialContext: probe.Initial, Loop: probe.Loop})
			component, handle = menu, menu.HandleInput
		}
		got := submenuResult{Steps: [][]string{{}}, Frames: [][]string{component.Render(probe.Width)}}
		for _, key := range probe.Keys {
			handle(key)
			got.Steps = append(got.Steps, append([]string{}, events...))
			got.Frames = append(got.Frames, component.Render(probe.Width))
			// The settings host closes a submenu on its first select or cancel callback (a stepped menu's completion is followed by a
			// cancel unless it loops); keys after that are unreachable, and Pig's Done state would only repeat the callback.
			if len(events) > 0 && (events[len(events)-1] == "cancel" || (probe.Kind == "select" && strings.HasPrefix(events[len(events)-1], "select:"))) {
				break
			}
		}
		expected[i].Steps, expected[i].Frames = expected[i].Steps[:len(got.Steps)], expected[i].Frames[:len(got.Frames)]
		stepsEqual := reflect.DeepEqual(canonicalSubmenuEvents(got.Steps), canonicalSubmenuEvents(expected[i].Steps))
		if !stepsEqual || !reflect.DeepEqual(got.Frames, expected[i].Frames) {
			failures++
			if failures <= 6 {
				t.Errorf("probe %d %+v:\nsteps = %q\nPi    = %q\n%s", i, probe, got.Steps, expected[i].Steps, firstFrameDifference(got.Frames, expected[i].Frames))
			}
		}
	}
	if failures > 6 {
		t.Errorf("%d of %d probes differ from Pi", failures, len(probes))
	}
}

// submenuContextJSON matches JSON.stringify of a string record: insertion order, which a Go map lacks, so keys sort (k0, k1, k2
// are inserted in that order by every probe except a looped restart, which also inserts in ascending order).
func submenuContextJSON(ctx map[string]string) string {
	out, _ := json.Marshal(ctx)
	return string(out)
}

func canonicalSubmenuEvents(steps [][]string) [][]string {
	out := make([][]string, len(steps))
	for i, events := range steps {
		out[i] = make([]string, len(events))
		for j, event := range events {
			out[i][j] = strings.ReplaceAll(event, " ", "")
		}
	}
	return out
}
