package codingagent

// pi: packages/coding-agent/src/modes/interactive/components/first-time-setup.ts FirstTimeSetupComponent.handleInput

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

// The handler checks up (or "k"), down (or "j"), confirm (or LF) and cancel in that order, and the raw keys count with their action. Each probe binds one
// key to two of the actions, or binds the raw keys to another action, and the dialog's events and frames are compared with the pinned Pi's. D88's sprite
// step is passed by pressing the key that entered it once more.
func TestFirstTimeSetupBindingOverlapsMatchPi(t *testing.T) {
	restoreStartupTheme(t)
	bindings := []map[string][]string{
		{"tui.select.confirm": {"ctrl+y"}, "tui.select.cancel": {"ctrl+y"}},
		{"tui.select.up": {"ctrl+y"}, "tui.select.cancel": {"ctrl+y"}},
		{"tui.select.down": {"ctrl+y"}, "tui.select.confirm": {"ctrl+y"}},
		{"tui.select.up": {"ctrl+y"}, "tui.select.down": {"ctrl+y"}},
		{"tui.select.cancel": {"k", "j", "enter"}},
		{"tui.select.confirm": {"k"}, "tui.select.cancel": {"j"}},
		{"tui.select.up": {"enter"}, "tui.select.down": {"escape"}},
		{"tui.select.cancel": {"ctrl+j"}},
		{"tui.select.confirm": {"escape"}},
	}
	alphabet := []string{"\x1b[A", "\x1b[B", "k", "j", "\r", "\n", "\x1b", "\x19", "x", "\x03"}
	var probes []firstSetupProbe
	for _, b := range bindings {
		var keys []string
		for _, first := range alphabet {
			for _, second := range alphabet {
				keys = append(keys, first, second)
				probes = append(probes, firstSetupProbe{Width: 100, Bindings: b, Keys: []string{first, second}})
				probes = append(probes, firstSetupProbe{Width: 100, Bindings: b, Keys: []string{"\x1b[B", first, second, first}})
			}
		}
		probes = append(probes, firstSetupProbe{Width: 100, Bindings: b, Keys: keys[:24]})
	}
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/first_time_setup.mjs", pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []firstSetupOracleResult
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	previousTheme, previousCaps, previousKitty, previousBindings := tui.ActiveTheme(), tui.GetCapabilities(), tui.IsKittyProtocolActive(), tui.GetKeybindings()
	tui.SetKittyProtocolActive(false)
	t.Cleanup(func() {
		tui.SetCapabilities(previousCaps)
		tui.SetTheme(previousTheme.Name)
		tui.SetKittyProtocolActive(previousKitty)
		tui.SetKeybindings(previousBindings)
	})
	body := func(lines []string) []string {
		out := []string{}
		seen := false
		for _, line := range lines {
			line = strings.NewReplacer("Welcome to PiG", "Welcome to pi", "within PiG", "within Pi").Replace(visibleText([][]string{{line}})[0][0])
			seen = seen || strings.Contains(line, "Welcome to")
			if seen {
				out = append(out, line)
			}
		}
		return out
	}
	submitted, cancelled := 0, 0
	for i, probe := range probes {
		tui.SetCapabilities(tui.TerminalCapabilities{TrueColor: true})
		tui.SetTheme("dark")
		user := map[string][]tui.KeyID{}
		for action, keys := range probe.Bindings {
			for _, key := range keys {
				user[action] = append(user[action], tui.KeyID(key))
			}
		}
		tui.SetKeybindings(tui.NewKeybindingsManager(tui.TUIKeybindingDefinitionsFor(tui.HostKeybindingPlatform()), user))
		var events [][]any
		component := NewFirstTimeSetupComponent(FirstTimeSetupOptions{
			OnThemePreview: func(name string) { events = append(events, []any{"preview", name}) },
			OnSubmit:       func(r FirstTimeSetupResult) { events = append(events, []any{"submit", r.Theme, r.ShareAnalytics}) },
			OnCancel:       func() { events = append(events, []any{"cancel"}) },
		})
		got := firstSetupOracleResult{Steps: [][][]any{{}}, Frames: [][]string{body(component.Render(probe.Width))}}
		for _, key := range probe.Keys {
			events = [][]any{}
			component.HandleInput(key)
			if component.step == firstTimeSetupStepSprite {
				component.HandleInput(key)
			}
			got.Steps = append(got.Steps, events)
			got.Frames = append(got.Frames, body(component.Render(probe.Width)))
		}
		want := expected[i]
		for k, step := range want.Steps {
			for _, e := range step {
				switch e[0] {
				case "submit":
					submitted++
				case "cancel":
					cancelled++
				}
			}
			if len(step) == 0 {
				want.Steps[k] = [][]any{}
			}
		}
		for k, f := range want.Frames {
			want.Frames[k] = body(f)
		}
		if !reflect.DeepEqual(got.Steps, want.Steps) || !reflect.DeepEqual(got.Frames, want.Frames) {
			t.Errorf("probe %d bindings=%v keys=%q\nPig steps %v\nPi  steps %v", i, probe.Bindings, probe.Keys, got.Steps, want.Steps)
		}
	}
	if submitted == 0 || cancelled == 0 {
		t.Fatalf("probes never reached submit and cancel: %d %d", submitted, cancelled)
	}
}
