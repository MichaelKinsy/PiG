package tui

// pi: packages/coding-agent/src/modes/interactive/components/user-message-selector.ts UserMessageList.handleInput

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// The handler checks up, down, the page keys, confirm and cancel in that order, so a key bound to a page action and to another action keeps the earlier one. Each probe binds one key
// to a page action and to one other action (the keys the page actions default to, so the overlap is the default key of one and the remapped key of the other) over empty, single and paged
// lists, against the pinned Pi.
func TestUserMessageSelectorPageKeyOverlapsMatchPi(t *testing.T) {
	bindings := []map[string][]string{
		{"tui.select.up": {"pageUp"}},
		{"tui.select.down": {"pageDown"}},
		{"tui.select.down": {"pageUp"}},
		{"tui.select.confirm": {"pageUp"}},
		{"tui.select.confirm": {"pageDown"}},
		{"tui.select.cancel": {"pageUp"}},
		{"tui.select.cancel": {"pageDown"}},
		{"tui.select.pageUp": {"enter"}},
		{"tui.select.pageDown": {"escape"}},
		{"tui.select.pageUp": {"up"}, "tui.select.pageDown": {"down"}},
		{"tui.select.pageDown": {"ctrl+y"}, "tui.select.confirm": {"ctrl+y"}, "tui.select.cancel": {"ctrl+y"}},
	}
	scripts := [][]string{
		{"\x1b[6~", "\x1b[6~", "\x1b[5~", "\r"},
		{"\x1b[5~", "\x1b[5~", "\x1b[6~", "\x1b"},
		{"\x1b[A", "\x1b[B", "\x1b[5~", "\x1b[6~", "\r"},
		{"\r", "\x1b", "\x19"},
	}
	var probes []userMessageProbe
	for _, count := range []int{0, 1, 3, 12, 25} {
		messages := make([]string, count)
		for i := range messages {
			messages[i] = fmt.Sprintf("message %d", i)
		}
		for _, binding := range bindings {
			for _, keys := range scripts {
				probes = append(probes, userMessageProbe{Theme: "dark", Width: 60, Messages: messages, Bindings: binding, Keys: keys})
			}
		}
	}
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/user_message_selector.mjs", pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []userMessageResult
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	previousBindings, previousTheme, previousCaps, previousKitty := GetKeybindings(), ActiveTheme(), GetCapabilities(), IsKittyProtocolActive()
	SetKittyProtocolActive(false)
	t.Cleanup(func() {
		SetKeybindings(previousBindings)
		SetCapabilities(previousCaps)
		storeActiveTheme(previousTheme)
		SetKittyProtocolActive(previousKitty)
	})
	selections := 0
	for i, probe := range probes {
		SetCapabilities(TerminalCapabilities{TrueColor: true})
		SetTheme(probe.Theme)
		SetKeybindings(NewKeybindingsManager(TUIKeybindingDefinitionsFor(HostKeybindingPlatform()), probe.Bindings))
		var events []string
		items := make([]UserMessageItem, len(probe.Messages))
		for index, text := range probe.Messages {
			items[index] = UserMessageItem{ID: strconv.Itoa(index), Text: text}
		}
		selector := newUserMessageSelectorComponent(items, func(id string) { events = append(events, "select:"+id) }, func() { events = append(events, "cancel") }, "", func(time.Duration, func()) {})
		got := userMessageResult{Steps: [][]string{{}}, Frames: [][]string{selector.Render(probe.Width)}}
		for _, key := range probe.Keys {
			selector.GetMessageList().HandleInput(key)
			got.Steps = append(got.Steps, append([]string{}, events...))
			got.Frames = append(got.Frames, selector.Render(probe.Width))
		}
		want := expected[i]
		for step := 1; step < len(got.Steps); step++ {
			if len(got.Steps[step]) != len(got.Steps[step-1]) {
				got.Steps, got.Frames = got.Steps[:step+1], got.Frames[:step+1]
				want.Steps, want.Frames = want.Steps[:step+1], want.Frames[:step+1]
				selections++
				break
			}
		}
		if !reflect.DeepEqual(got.Steps, want.Steps) || !reflect.DeepEqual(got.Frames, want.Frames) {
			t.Errorf("probe %d keys=%q bindings=%v n=%d:\nsteps = %q\nPi    = %q\n%s", i, probe.Keys, probe.Bindings, len(probe.Messages), got.Steps, want.Steps, userMessageDifference(got.Frames, want.Frames))
		}
	}
	if selections == 0 {
		t.Fatal("no probe reached a select or cancel")
	}
}
