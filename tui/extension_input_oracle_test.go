package tui

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

type extensionInputProbe struct {
	Theme       string              `json:"theme"`
	Width       int                 `json:"width"`
	Title       string              `json:"title"`
	Description string              `json:"description,omitempty"`
	Initial     string              `json:"initial,omitempty"`
	Focused     bool                `json:"focused"`
	Bindings    map[string][]string `json:"bindings,omitempty"`
	Keys        []string            `json:"keys"`
	Kitty       bool                `json:"kitty,omitempty"`
}

// extension-input.ts (ExtensionInputComponent) against pinned Pi: every frame and the submit and cancel callbacks after each key, with default
// and rebound select keys, an initial value, a description and a focused or unfocused input. Not compared: the countdown (needs a TUI), and an
// input the host never focuses (Pig's wrapper draws its cursor as the editor slot's occupant, Pi's draws none until focused).
func TestExtensionInputMatchesPi(t *testing.T) {
	scripts := [][]string{
		nil,
		{"a", "b", "c"},
		{"a", "\r"},
		{"\r"},
		{"\n"},
		{"\x1b"},
		{"\x03"},
		{"h", "i", "\x7f", "\x7f", "\x7f"},
		{"w", "o", "r", "d", " ", "x", "\x17", "\x15", "z"},
		{"a", "b", "\x1b[D", "\x1b[D", "X", "\x1b[C", "\x1b[3~"},
		{"\x1b[200~pasted text\x1b[201~", "\r"},
		{"界", "🙂", "\x1b[D", "\x7f", "\r"},
		{"\x01", "q", "\x05", "r", "\x0b", "\x19"},
		{"\x13", "a", "\x13"},
		{"\x18", "a", "\x18"},
		{"a", "\x1b", "b"},
		{"\x1b[H", "\x1b[F", "k", "\r"},
		{"\x1bb", "\x1bf", "\x1bd", "\x1b\x7f"},
		{"a b c d e f g h i j k l m n o p q r s t u v w x y z a b c d e f g h", "\r"},
	}
	bindings := []map[string][]string{
		nil,
		{"tui.select.confirm": {"ctrl+s"}},
		{"tui.select.cancel": {"ctrl+x"}},
		{"tui.select.confirm": {}, "tui.select.cancel": {}},
		{"tui.input.submit": {"ctrl+s"}, "tui.editor.deleteWordBackward": {"ctrl+b"}},
	}
	titles := []string{"Name?", "界🙂 a long title that wraps at the narrower widths", ""}
	descriptions := []string{"", "Describe it", "multi\nline"}
	initials := []string{"", "preset", "界🙂 preset with a long value that does not fit the narrow widths"}
	var probes []extensionInputProbe
	for _, theme := range []string{"dark", "light"} {
		for ti, title := range titles {
			for bi, b := range bindings {
				for si, keys := range scripts {
					if (ti+bi+si)%2 != 0 && si != 4 { // the lone-LF script runs under every binding set: an explicit LF confirms even when select.confirm is rebound
						continue
					}
					for _, width := range []int{60, 20, 8, 4} {
						probes = append(probes, extensionInputProbe{Theme: theme, Width: width, Title: title, Description: descriptions[(bi+si)%len(descriptions)], Initial: initials[(ti+si)%len(initials)], Focused: (si+width)%3 != 0, Bindings: b, Keys: keys})
					}
				}
			}
		}
	}
	// The Kitty keyboard protocol: CSI-u forms of enter, escape, ctrl+j, ctrl+c, shift+enter and plain letters, which the legacy table reads differently (LF is ctrl+j, not enter).
	for _, keys := range [][]string{{"\x1b[13u"}, {"\x1b[27u"}, {"\x1b[106;5u"}, {"\x1b[99;5u"}, {"\x1b[13;2u"}, {"\n"}, {"\r"}, {"\x1b[97u", "\x1b[107u", "\x1b[106u", "\x1b[1;1A", "\x1b[1;1B", "\x1b[1;5A", "\x1b[13u"}, {"\x1b[B", "\x1b[13;1:1u"}} {
		for _, b := range bindings[:2] {
			for _, width := range []int{70, 12} {
				probes = append(probes, extensionInputProbe{Theme: "dark", Width: width, Title: "Name?", Initial: "preset", Focused: true, Bindings: b, Keys: keys, Kitty: true})
			}
		}
	}
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/extension_input.mjs", pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []showImagesResult
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	previousTheme, previousCaps := ActiveTheme(), GetCapabilities()
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
		SetKittyProtocolActive(probe.Kitty)
		SetCapabilities(TerminalCapabilities{TrueColor: true})
		SetTheme(probe.Theme)
		SetKeybindings(NewKeybindingsManager(TUIKeybindingDefinitionsFor(HostKeybindingPlatform()), probe.Bindings))
		var events []string
		component := NewExtensionInputComponent(probe.Title, "ignored",
			func(value string) { events = append(events, "submit:"+value) },
			func() { events = append(events, "cancel") },
			ExtensionInputOptions{Description: probe.Description, InitialValue: probe.Initial})
		component.SetFocused(probe.Focused)
		got := showImagesResult{Steps: [][]string{}, Frames: [][]string{component.Render(probe.Width)}}
		// The host removes the component as soon as a callback runs, so Pi's never sees a key after one; Pig's host polls Done and ignores later keys.
		keys := probe.Keys
		if completed := firstCompletedStep(expected[i].Steps); completed >= 0 {
			keys = keys[:completed+1]
			expected[i].Steps, expected[i].Frames = expected[i].Steps[:completed+1], expected[i].Frames[:completed+2]
		}
		for _, key := range keys {
			component.HandleInput(key)
			got.Steps = append(got.Steps, append([]string{}, events...))
			got.Frames = append(got.Frames, component.Render(probe.Width))
		}
		if !reflect.DeepEqual(got.Steps, expected[i].Steps) || !reflect.DeepEqual(got.Frames, expected[i].Frames) {
			if failures++; failures <= 3 {
				t.Errorf("probe %d %+v:\n steps Pig %q Pi %q\n frames Pig %q\n Pi  %q", i, probe, got.Steps, expected[i].Steps, got.Frames, expected[i].Frames)
			}
		}
	}
	t.Logf("%d probes, %d differ", len(probes), failures)
	if failures > 3 {
		t.Errorf("%d of %d probes differ from Pi", failures, len(probes))
	}
}

// firstCompletedStep is the index of the first key after which a callback had run, or -1.
func firstCompletedStep(steps [][]string) int {
	for i, events := range steps {
		if len(events) > 0 {
			return i
		}
	}
	return -1
}
