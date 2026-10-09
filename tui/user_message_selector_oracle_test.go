package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

type userMessageProbe struct {
	Theme    string   `json:"theme"`
	Width    int      `json:"width"`
	Messages []string `json:"messages"`
	//portlint:allow emptydrop the oracle input treats an absent map and an empty map alike, so nothing observable depends on the difference
	Bindings map[string][]string `json:"bindings,omitempty"`
	Keys     []string            `json:"keys"`
}

type userMessageResult struct {
	Steps  [][]string `json:"steps"`
	Frames [][]string `json:"frames"`
}

// user-message-selector.ts handleInput against pinned Pi: up and down (wrapping) are checked before confirm and cancel, so a key
// bound to two of them keeps the earlier action; confirm selects the highlighted message, cancel cancels, other keys are ignored.
func TestUserMessageSelectorInputMatchesPi(t *testing.T) {
	messages := func(n int) []string {
		out := []string{}
		for i := range n {
			out = append(out, fmt.Sprintf("user message number %d about topic %d", i, i%3))
		}
		return out
	}
	scripts := [][]string{
		{},
		{"\x1b[A", "\x1b[A", "\r"},
		{"\x1b[B", "\x1b[B", "\x1b[B", "\r"},
		{"\x1b"},
		{"\x03"},
		{"x", "\x1b[6~", "\x1b[H", "\r"},
		{"\x1b[A", "\x1b[A", "\x1b[A", "\x1b[A", "\x1b[A", "\x1b[A", "\x1b[A", "\x1b[A", "\x1b[A", "\x1b[A", "\x1b[A", "\x1b[A", "\x1b[A"},
		{"\x0e", "\x0e", "\x10", "\r", "\x1b"},
		{"\n", "\r"},
		{"\x19", "\x19"},
	}
	bindings := []map[string][]string{
		nil,
		{"tui.select.up": {"enter"}},
		{"tui.select.down": {"escape"}, "tui.select.cancel": {"escape"}},
		{"tui.select.confirm": {"down"}, "tui.select.up": {"ctrl+p"}, "tui.select.down": {"ctrl+n"}},
		{"tui.select.cancel": {"up"}},
		{"tui.select.confirm": {"ctrl+y"}, "tui.select.cancel": {"ctrl+y"}},
		{"tui.select.confirm": {"ctrl+y"}, "tui.select.up": {"ctrl+y"}},
	}
	var probes []userMessageProbe
	for _, theme := range []string{"dark", "light"} {
		for _, n := range []int{0, 1, 3, 12} {
			for _, b := range bindings {
				for _, keys := range scripts {
					for _, width := range []int{80, 24} {
						probes = append(probes, userMessageProbe{Theme: theme, Width: width, Messages: messages(n), Bindings: b, Keys: keys})
					}
				}
			}
		}
	}
	// Widths stay above the metadata row so the D66 row bound does not apply. Message text Pi normalizes and truncates for the row: newlines, tabs, runs of spaces, wide characters, escapes, and text far wider than the row.
	special := []string{"first line\nsecond line\n\nfourth", "\ttabbed\ttext  with   spaces", strings.Repeat("a very long message ", 12), "日本語のメッセージ🙂 with wide cells", "\x1b[31mred\x1b[0m escape text", "   ", "", "line one\r\nline two", "x"}
	for _, theme := range []string{"dark", "light"} {
		for _, width := range []int{80, 40, 24} {
			for _, keys := range scripts[:6] {
				probes = append(probes, userMessageProbe{Theme: theme, Width: width, Messages: special, Keys: keys})
				probes = append(probes, userMessageProbe{Theme: theme, Width: width, Messages: special[:3], Keys: keys})
			}
		}
	}
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/user_message_selector.mjs", pigversion.UpstreamVersion)
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
	failures := 0
	for i, probe := range probes {
		SetCapabilities(TerminalCapabilities{TrueColor: true})
		SetTheme(probe.Theme)
		SetKeybindings(NewKeybindingsManager(TUIKeybindingDefinitionsFor(HostKeybindingPlatform()), probe.Bindings))
		// The empty selector's 100ms auto-cancel runs on a timer goroutine after the scripted keys; the oracle process exits before
		// Pi's timer fires, so the timer's cancel is recorded only while the keys run.
		var mu sync.Mutex
		var events []string
		scripted := true
		record := func(event string) {
			mu.Lock()
			defer mu.Unlock()
			if scripted {
				events = append(events, event)
			}
		}
		items := make([]UserMessageItem, len(probe.Messages))
		for index, text := range probe.Messages {
			items[index] = UserMessageItem{ID: strconv.Itoa(index), Text: text}
		}
		selector := NewUserMessageSelectorComponent(items, func(id string) { record("select:" + id) }, func() { record("cancel") }, "")
		got := userMessageResult{Steps: [][]string{{}}, Frames: [][]string{selector.Render(probe.Width)}}
		for _, key := range probe.Keys {
			selector.GetMessageList().HandleInput(key)
			mu.Lock()
			got.Steps = append(got.Steps, append([]string{}, events...))
			mu.Unlock()
			got.Frames = append(got.Frames, selector.Render(probe.Width))
		}
		mu.Lock()
		scripted = false
		mu.Unlock()
		want := expected[i]
		// Pig bounds every row to the width (D66) and closes colors with SGR 0; trailing blanks and style sequences are ignored.
		if !reflect.DeepEqual(got.Steps, want.Steps) || !reflect.DeepEqual(userMessageText(got.Frames), userMessageText(want.Frames)) {
			failures++
			if failures <= 6 {
				t.Errorf("probe %d keys=%q bindings=%v n=%d width=%d:\nsteps = %q\nPi    = %q\n%s", i, probe.Keys, probe.Bindings, len(probe.Messages), probe.Width, got.Steps, want.Steps, userMessageDifference(userMessageText(got.Frames), userMessageText(want.Frames)))
			}
		}
	}
	if failures > 6 {
		t.Errorf("%d of %d probes differ from Pi", failures, len(probes))
	}
}

// userMessageText returns the frames unchanged: rows are compared byte for byte, colours included.
func userMessageText(frames [][]string) [][]string { return frames }

func userMessageDifference(got, want [][]string) string {
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
	return "frames equal"
}
