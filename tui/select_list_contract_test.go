package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"reflect"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

var selectListHandleInputActions = []string{"tui.select.up", "tui.select.down", "tui.select.pageUp", "tui.select.pageDown", "tui.select.confirm", "tui.select.cancel"}

// TestSelectListSharedKeysResolveInPiOrder binds every pair of the actions SelectList.handleInput resolves to one key and sends that key against Pi's SelectList,
// over an empty list, a single item, a list that fits one page and lists longer than a page with the selection at the start, in the middle and at the end.
// select-list.ts checks up, down, the page keys, confirm and cancel in that order, so the first action in that order wins; callbacks, selected item and rendered rows are compared after every key.
func TestSelectListSharedKeysResolveInPiOrder(t *testing.T) {
	list := func(n int) []selectListOracleItem {
		items := []selectListOracleItem{}
		for i := range n {
			items = append(items, selectListOracleItem{Value: fmt.Sprintf("v%02d", i), Label: fmt.Sprintf("Label %d", i)})
		}
		return items
	}
	down, up := "\x1b[B", "\x1b[A"
	positions := [][]string{{}, slices.Repeat([]string{down}, 3), slices.Repeat([]string{down}, 6), slices.Repeat([]string{down}, 11), {up}}
	var probes []selectListProbe
	for _, shared := range []struct{ id, data string }{{"ctrl+q", "\x11"}, {"enter", "\r"}, {"down", down}, {"escape", "\x1b"}} {
		for first := range selectListHandleInputActions {
			for second := first + 1; second < len(selectListHandleInputActions); second++ {
				bindings := map[string][]string{selectListHandleInputActions[first]: {shared.id}, selectListHandleInputActions[second]: {shared.id}}
				for _, count := range []int{0, 1, 3, 5, 12} {
					for _, position := range positions {
						keys := append(slices.Clone(position), shared.data, shared.data)
						probes = append(probes, selectListProbe{Items: list(count), MaxVisible: 5, Width: 40, Bindings: bindings, Keys: keys})
					}
				}
			}
		}
	}
	compareSelectListProbesWithPi(t, probes)
}

// compareSelectListProbesWithPi runs each probe through Pi's SelectList and Pig's and requires the callbacks, selected item and rendered rows to agree after every key.
func compareSelectListProbesWithPi(t *testing.T, probes []selectListProbe) {
	t.Helper()
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/select_list.mjs", pigversion.UpstreamPackageVersion)
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
	SetKittyProtocolActive(false)
	t.Cleanup(func() { SetKeybindings(previous); SetKittyProtocolActive(previousKitty) })
	tag := func(name string) func(string) string {
		return func(text string) string { return "<" + name + ">" + text + "</" + name + ">" }
	}
	theme := SelectListTheme{SelectedPrefix: tag("sp"), SelectedText: tag("st"), Description: tag("d"), ScrollInfo: tag("si"), NoMatch: tag("nm")}
	failures := 0
	for i, probe := range probes {
		SetKeybindings(NewKeybindingsManager(TUIKeybindingDefinitionsFor(HostKeybindingPlatform()), probe.Bindings))
		var items []SelectItem
		for _, it := range probe.Items {
			items = append(items, SelectItem(it))
		}
		list := NewSelectList(items, probe.MaxVisible, theme, SelectListLayoutOptions{})
		var events []string
		list.OnSelect = func(item SelectItem) { events = append(events, "select:"+item.Value) }
		list.OnCancel = func() { events = append(events, "cancel") }
		list.OnSelectionChange = func(item SelectItem) { events = append(events, "change:"+item.Value) }
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
				t.Errorf("probe %d keys=%q bindings=%v items=%d:\nsteps = %q\nPi    = %q\n%s", i, probe.Keys, probe.Bindings, len(probe.Items), got.Steps, expected[i].Steps, selectListFrameDifference(got.Frames, expected[i].Frames))
			}
		}
	}
	if failures > 6 {
		t.Errorf("%d of %d probes differ from Pi", failures, len(probes))
	}
	t.Logf("%d probes", len(probes))
}
