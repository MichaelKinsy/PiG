package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os/exec"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

type settingsMouseItem struct {
	ID           string   `json:"id"`
	Label        string   `json:"label"`
	Description  string   `json:"description,omitempty"`
	CurrentValue string   `json:"currentValue"`
	Values       []string `json:"values,omitempty"`
	Submenu      bool     `json:"submenu,omitempty"`
	Navigate     string   `json:"navigate,omitempty"`
}

type settingsMouseStep struct {
	Key        *string `json:"key,omitempty"`
	Type       string  `json:"type,omitempty"`
	Button     string  `json:"button,omitempty"`
	X          int     `json:"x"`
	Y          int     `json:"y"`
	WheelDelta int     `json:"wheelDelta,omitempty"`
	ClickCount int     `json:"clickCount,omitempty"`
}

type settingsMouseScenario struct {
	Width      int                 `json:"width"`
	MaxVisible int                 `json:"maxVisible"`
	Search     bool                `json:"search"`
	Items      []settingsMouseItem `json:"items"`
	Steps      []settingsMouseStep `json:"steps"`
}

// The widths start at 30: below that the empty-state text and wrapped descriptions are rows wider than the pane, which Pig clips and Pi emits (D66).
func settingsMouseScenarios() []settingsMouseScenario {
	rng := rand.New(rand.NewPCG(31, 4))
	pick := func(items []string) string { return items[rng.IntN(len(items))] }
	keys := []string{"\x1b[B", "\x1b[A", "\r", " ", "\x1b", "a", "b", "x", "\x7f"}
	var scenarios []settingsMouseScenario
	for range 1200 {
		scenario := settingsMouseScenario{Width: []int{30, 40, 60, 80}[rng.IntN(4)], MaxVisible: 1 + rng.IntN(6), Search: rng.IntN(2) == 0, Items: []settingsMouseItem{}}
		for i := range rng.IntN(9) {
			item := settingsMouseItem{ID: fmt.Sprintf("id%d", i), Label: pick([]string{"Alpha", "beta", "Gamma setting", "日本語", "A longer setting label"}), CurrentValue: pick([]string{"on", "off", "x", "a longer value"})}
			if rng.IntN(2) == 0 {
				item.Description = pick([]string{"does a thing", "a considerably longer description of the setting", "日本語"})
			}
			switch rng.IntN(3) {
			case 0:
				item.Values = []string{"on", "off"}
				if rng.IntN(2) == 0 {
					item.Values = append(item.Values, "auto")
				}
			case 1:
				item.Submenu = true
				if rng.IntN(2) == 0 {
					item.Navigate = fmt.Sprintf("id%d", rng.IntN(max(1, 6)))
				}
			}
			scenario.Items = append(scenario.Items, item)
		}
		for range 2 + rng.IntN(14) {
			if rng.IntN(4) == 0 {
				k := pick(keys)
				scenario.Steps = append(scenario.Steps, settingsMouseStep{Key: &k})
				continue
			}
			step := settingsMouseStep{Type: pick([]string{"click", "click", "press", "release", "drag", "move", "wheel"}), Button: pick([]string{"left", "left", "left", "right", "none"}), X: rng.IntN(scenario.Width), Y: rng.IntN(14) - 1}
			if step.Type == "wheel" {
				step.WheelDelta = []int{-2, -1, 1, 3, 0}[rng.IntN(5)]
			}
			if step.Type == "click" {
				step.ClickCount = 1 + rng.IntN(2)
			}
			scenario.Steps = append(scenario.Steps, step)
		}
		scenarios = append(scenarios, scenario)
	}
	return scenarios
}

func runSettingsMouseWithPig(scenario settingsMouseScenario) mouseOracleRecord {
	SetCapabilities(TerminalCapabilities{TrueColor: true})
	SetKeybindings(NewKeybindingsManager(TUIKeybindingDefinitionsFor(HostKeybindingPlatform()), nil))
	wrap := func(tag string) func(string) string {
		return func(text string) string { return "<" + tag + ">" + text + "</" + tag + ">" }
	}
	sgr := func(open, closeCode string) func(string) string {
		return func(text string) string { return "\x1b[" + open + "m" + text + "\x1b[" + closeCode + "m" }
	}
	theme := SettingsListTheme{
		Label: func(text string, selected bool) string {
			if selected {
				return sgr("1;36", "22;39")(text)
			}
			return sgr("37", "39")(text)
		},
		Value: func(text string, selected bool) string {
			if selected {
				return sgr("1;33", "22;39")(text)
			}
			return sgr("90", "39")(text)
		},
		Description: sgr("2", "22"), Cursor: "→ ", Hint: sgr("3", "23"),
	}
	selectTheme := SelectListTheme{SelectedPrefix: wrap("sp"), SelectedText: wrap("st"), Description: wrap("d"), ScrollInfo: wrap("si"), NoMatch: wrap("nm")}
	var calls []string
	items := make([]SettingItem, len(scenario.Items))
	for i, item := range scenario.Items {
		items[i] = SettingItem{ID: item.ID, Label: item.Label, Description: item.Description, CurrentValue: item.CurrentValue, Values: item.Values}
		if item.Submenu {
			items[i].Submenu = func(_ string, done func(*string, *SubmenuDoneOptions)) Component {
				entries := []SelectItem{{Value: "one", Label: "one"}, {Value: "two", Label: "two"}, {Value: "three", Label: "three"}, {Value: "four", Label: "four"}}
				list := NewSelectList(entries, 2, selectTheme)
				list.OnSelect = func(selected SelectItem) {
					calls = append(calls, "submenu-select:"+selected.Value)
					var options *SubmenuDoneOptions
					if item.Navigate != "" {
						options = &SubmenuDoneOptions{NavigateTo: item.Navigate}
					}
					value := selected.Value
					done(&value, options)
				}
				list.OnCancel = func() { done(nil, nil) }
				return list
			}
		}
	}
	list := NewSettingsList(items, scenario.MaxVisible, theme, func(id, value string) { calls = append(calls, "change:"+id+"="+value) }, func() { calls = append(calls, "cancel") }, SettingsListOptions{EnableSearch: scenario.Search})
	frame := list.Render(scenario.Width)
	record := mouseOracleRecord{Frames: [][]string{frame}}
	for _, step := range scenario.Steps {
		calls = nil
		out := mouseOracleOut{}
		if step.Key != nil {
			list.HandleInput(*step.Key)
		} else if result := list.HandleMouse(TuiMouseEvent{Type: TuiMouseEventType(step.Type), Button: TuiMouseButton(step.Button), X: step.X, Y: step.Y, ScreenX: step.X, ScreenY: step.Y, Width: scenario.Width, Height: len(frame), WheelDelta: step.WheelDelta, ClickCount: step.ClickCount}); result != nil {
			flag := func(b bool) *bool {
				if !b {
					return nil
				}
				return &b
			}
			out.Result = &mouseOracleResult{Handled: flag(result.Handled), Capture: flag(result.Capture), Focus: flag(result.Focus), Render: result.Render}
		}
		out.Calls = append([]string{}, calls...)
		record.Outs = append(record.Outs, out)
		frame = list.Render(scenario.Width)
		record.Frames = append(record.Frames, frame)
	}
	return record
}

// SettingsList.handleMouse (components/settings-list.ts) against pinned Pi over 1200 seeded scenarios: wheel moves the selection, press selects a row and click activates it (cycling
// the value or opening a submenu), the search row and the blank row under it, rows past the end, other buttons and event types, and an open submenu receiving the events with focus forced
// on; interleaved keys open and close submenus and filter by the search input. Result flags, the change, cancel and submenu callbacks and every frame must agree.
func TestSettingsListMouseMatchesPi(t *testing.T) {
	scenarios := settingsMouseScenarios()
	input, err := json.Marshal(scenarios)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/settings_mouse.mjs", pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []mouseOracleRecord
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	previousCaps, previousKeys := GetCapabilities(), GetKeybindings()
	t.Cleanup(func() {
		SetCapabilities(previousCaps)
		SetKeybindings(previousKeys)
	})
	failures := 0
	for i, scenario := range scenarios {
		got := runSettingsMouseWithPig(scenario)
		if reflect.DeepEqual(got, expected[i]) {
			continue
		}
		if failures++; failures > 4 {
			continue
		}
		report := fmt.Sprintf("scenario %d (width %d, max visible %d, search %v)", i, scenario.Width, scenario.MaxVisible, scenario.Search)
		if !reflect.DeepEqual(got.Frames[0], expected[i].Frames[0]) {
			report += fmt.Sprintf("\n  first frame\n  Pig %q\n  Pi  %q", got.Frames[0], expected[i].Frames[0])
		}
		for k := range got.Outs {
			g, _ := json.Marshal(got.Outs[k])
			e, _ := json.Marshal(expected[i].Outs[k])
			if !bytes.Equal(g, e) {
				step, _ := json.Marshal(scenario.Steps[k])
				report += fmt.Sprintf("\n  step %d %s\n  Pig %s\n  Pi  %s", k, step, g, e)
				break
			}
			if !reflect.DeepEqual(got.Frames[k+1], expected[i].Frames[k+1]) {
				report += fmt.Sprintf("\n  frame after step %d\n  Pig %q\n  Pi  %q", k, got.Frames[k+1], expected[i].Frames[k+1])
				break
			}
		}
		t.Error(report)
	}
	if failures > 4 {
		t.Errorf("%d of %d scenarios differ from Pi", failures, len(scenarios))
	}
}
