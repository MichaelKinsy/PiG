package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

type selectListProbe struct {
	Items      []selectListOracleItem `json:"items"`
	MaxVisible int                    `json:"maxVisible"`
	Width      int                    `json:"width"`
	Filter     string                 `json:"filter,omitempty"`
	// Layout holds the SelectListLayoutOptions widths; zero is unset in Go, so the probes only use positive widths or leave one out.
	Layout selectListOracleLayout `json:"layout"`
	//portlint:allow emptydrop the oracle input treats an absent map and an empty map alike, so nothing observable depends on the difference
	Bindings map[string][]string `json:"bindings,omitempty"`
	Keys     []string            `json:"keys"`
}

type selectListOracleLayout struct {
	Min int `json:"minPrimaryColumnWidth,omitempty"`
	Max int `json:"maxPrimaryColumnWidth,omitempty"`
}

type selectListOracleItem struct {
	Value       string `json:"value"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

type selectListResult struct {
	Steps  [][]string `json:"steps"`
	Frames [][]string `json:"frames"`
	// Selected is getSelectedItem()?.value after the initial state and after each key; null is "no item" (select-list.ts:269-272).
	Selected []*string `json:"selected"`
}

// select-list.ts handleInput and getSelectedItem against the pinned pi-tui: up/down wrap (an empty list included) and report selection changes only for
// an existing item, confirm selects, cancel cancels, every other key (page, home, end, text) is ignored; frames follow each key.
// Pi source: packages/tui/src/components/select-list.ts
// mutation-checked: zeroing the results of SelectList.SetFilter fails it
// mutation-checked: dropping the reads and writes of SelectList.OnCancel, SelectList.OnSelect, SelectList.OnSelectionChange fails it
func TestSelectListInputMatchesPi(t *testing.T) {
	mk := func(n int, described bool) []selectListOracleItem {
		items := []selectListOracleItem{}
		for i := range n {
			item := selectListOracleItem{Value: fmt.Sprintf("v%02d", i), Label: fmt.Sprintf("Label %d", i)}
			if described {
				item.Description = fmt.Sprintf("description of item %d", i)
			}
			items = append(items, item)
		}
		return items
	}
	scripts := [][]string{
		{},
		{"\x1b[B", "\x1b[B", "\x1b[A", "\r"},
		{"\x1b[A", "\x1b[A", "\r"},
		{"\x1b[A", "\x1b[B", "\x1b[B"},
		{"\x1b[6~", "\x1b[5~", "\x1b[H", "\x1b[F", "x", "\r"},
		{"\x1b", "\x1b"},
		{"\x03", "\x1b[B", "\x03"},
		{"\x1b[B", "\x1b[B", "\x1b[B", "\x1b[B", "\x1b[B", "\x1b[B", "\x1b[B", "\x1b[B"},
		{"\n", "\r", "\r"},
		{"j", "k", "\x0e", "\x10", "\r"},
		{"\x1b[A", "\x1b[A", "\x1b[A", "\x1b[A", "\x1b[A", "\x1b[A", "\x1b[A", "\x1b[A", "\x1b[A"},
	}
	bindings := []map[string][]string{
		nil,
		{"tui.select.up": {"k"}, "tui.select.down": {"j"}},
		{"tui.select.confirm": {"ctrl+y"}, "tui.select.cancel": {"ctrl+y"}},
		{"tui.select.up": {"down"}},
		{"tui.select.down": {"ctrl+n", "down"}, "tui.select.up": {"ctrl+p", "up"}, "tui.select.cancel": {"x"}},
	}
	// Rows that stress renderItem and the primary column: labels longer than the width, double-width labels, a description with line breaks and tabs, an
	// empty description, a label that equals its value, and ANSI inside the label.
	varied := []selectListOracleItem{
		{Value: "a", Label: "short", Description: "line one\nline two\r\n\tindented"},
		{Value: "b", Label: "a label that is much longer than the thirty column probe width", Description: "tail"},
		{Value: "c", Label: "日本語のラベル", Description: "全角の説明文がここにあります"},
		{Value: "d", Label: "", Description: "label falls back to the value"},
		{Value: "e", Label: "\x1b[31mred\x1b[0m label", Description: ""},
		{Value: "f", Label: "ok", Description: "   padded   description   "},
	}
	sets := []struct {
		items  []selectListOracleItem
		max    int
		filter string
	}{
		{varied, 6, ""},
		{varied, 2, ""},
		{mk(0, false), 5, ""},
		{mk(1, true), 5, ""},
		{mk(3, true), 5, ""},
		{mk(12, true), 5, ""},
		{mk(12, false), 3, "v0"},
		{mk(12, true), 3, "zz"},
		{mk(12, true), 1, ""},
	}
	layouts := []selectListOracleLayout{{}, {Min: 12, Max: 20}, {Min: 20, Max: 12}, {Min: 6}, {Max: 8}, {Min: 60, Max: 70}}
	var probes []selectListProbe
	for _, set := range sets {
		for _, b := range bindings {
			for _, keys := range scripts {
				for _, width := range []int{30, 80} {
					probes = append(probes, selectListProbe{Items: set.items, MaxVisible: set.max, Width: width, Filter: set.filter, Bindings: b, Keys: keys})
				}
			}
		}
		// Layout widths change only the frames, so they run against the keys that move the window.
		for _, layout := range layouts[1:] {
			for _, width := range []int{20, 30, 80} {
				probes = append(probes, selectListProbe{Items: set.items, MaxVisible: set.max, Width: width, Filter: set.filter, Layout: layout, Keys: scripts[1]})
			}
		}
	}
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/select_list.mjs", pigversion.UpstreamVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []selectListResult
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	previous, previousKitty := GetKeybindings(), IsKittyProtocolActive()
	// Pi runs without the Kitty keyboard protocol, which changes how LF and other legacy keys match; pin it against a leaked global.
	SetKittyProtocolActive(false)
	t.Cleanup(func() { SetKeybindings(previous); SetKittyProtocolActive(previousKitty) })
	tag := func(name string) func(string) string {
		return func(text string) string { return "<" + name + ">" + text + "</" + name + ">" }
	}
	theme := SelectListTheme{SelectedPrefix: tag("sp"), SelectedText: tag("st"), Description: tag("d"), ScrollInfo: tag("si"), NoMatch: tag("nm")}
	failures := 0
	for i, probe := range probes {
		restoreKeybindingsAfterTest(t)
		SetKeybindings(NewKeybindingsManager(TUIKeybindingDefinitionsFor(HostKeybindingPlatform()), probe.Bindings))
		var items []SelectItem
		for _, it := range probe.Items {
			items = append(items, SelectItem(it))
		}
		list := NewSelectList(items, probe.MaxVisible, theme, SelectListLayoutOptions{MinPrimaryColumnWidth: probe.Layout.Min, MaxPrimaryColumnWidth: probe.Layout.Max})
		var events []string
		list.OnSelect = func(item SelectItem) { events = append(events, "select:"+item.Value) }
		list.OnCancel = func() { events = append(events, "cancel") }
		list.OnSelectionChange = func(item SelectItem) { events = append(events, "change:"+item.Value) }
		if probe.Filter != "" {
			list.SetFilter(probe.Filter)
		}
		selected := func() *string {
			if item, ok := list.SelectedItem(); ok {
				return &item.Value
			}
			return nil
		}
		got := selectListResult{Steps: [][]string{{}}, Frames: [][]string{list.Render(probe.Width)}, Selected: []*string{selected()}}
		for _, key := range probe.Keys {
			list.HandleInput(key)
			got.Steps = append(got.Steps, append([]string{}, events...))
			got.Frames = append(got.Frames, list.Render(probe.Width))
			got.Selected = append(got.Selected, selected())
		}
		if !reflect.DeepEqual(got.Steps, expected[i].Steps) || !reflect.DeepEqual(got.Frames, expected[i].Frames) || !reflect.DeepEqual(got.Selected, expected[i].Selected) {
			failures++
			if failures <= 6 {
				t.Errorf("probe %d keys=%q bindings=%v items=%d max=%d filter=%q:\nsteps = %q\nPi    = %q\n%s", i, probe.Keys, probe.Bindings, len(probe.Items), probe.MaxVisible, probe.Filter, got.Steps, expected[i].Steps, selectListFrameDifference(got.Frames, expected[i].Frames))
			}
		}
	}
	if failures > 6 {
		t.Errorf("%d of %d probes differ from Pi", failures, len(probes))
	}
}

func selectListFrameDifference(got, want [][]string) string {
	for f := range min(len(got), len(want)) {
		for l := range max(len(got[f]), len(want[f])) {
			var g, w string
			if l < len(got[f]) {
				g = got[f][l]
			}
			if l < len(want[f]) {
				w = want[f][l]
			}
			if g != w {
				return fmt.Sprintf("frame %d line %d:\n  pig %q\n  Pi  %q", f, l, g, w)
			}
		}
	}
	return fmt.Sprintf("frames equal (%d vs %d)", len(got), len(want))
}
