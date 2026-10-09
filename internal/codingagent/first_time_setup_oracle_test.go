package codingagent

import (
	"bytes"
	"encoding/json"
	"math/rand"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
	"github.com/MichaelKinsy/PiG/tui"
)

type firstSetupProbe struct {
	Width    int                 `json:"width"`
	Bindings map[string][]string `json:"bindings,omitempty"`
	Keys     []string            `json:"keys"`
}

type firstSetupOracleResult struct {
	Steps  [][][]any  `json:"steps"`
	Frames [][]string `json:"frames"`
}

// first-time-setup.ts handleInput against pinned Pi: k/up and j/down clamp over the options and preview the theme only on a change,
// confirm (or LF) continues then submits, the cancel binding skips. pig divergence (D88): Pig inserts a sprite step after the theme
// step and draws its own logo and "PiG" name (Pi: APP_NAME "pi"), so the harness presses the key that entered the sprite step once more (Pi has no such
// step) and the frames compare from the "Welcome" line down with the app name read as Pi's; events, results and every option line must agree.
func TestFirstTimeSetupMatchesPi(t *testing.T) {
	bindings := []map[string][]string{
		nil,
		{"tui.select.cancel": {"ctrl+g"}, "tui.select.confirm": {"ctrl+y"}},
		{"tui.select.up": {"ctrl+p"}, "tui.select.down": {"ctrl+n"}, "tui.select.confirm": {"space"}},
		{"tui.select.confirm": {"enter", "x"}, "tui.select.cancel": {"escape", "q"}},
	}
	alphabet := []string{"\x1b[A", "\x1b[A", "\x1b[B", "\x1b[B", "k", "j", "k", "j", "\r", "\r", "\n", "\x1b", "\x03", " ", "x", "q", "\x07", "\x19", "\x10", "\x0e", "a"}
	rng := rand.New(rand.NewSource(20261011))
	var probes []firstSetupProbe
	for _, b := range bindings {
		for range 150 {
			keys := make([]string, 2+rng.Intn(14))
			for i := range keys {
				keys[i] = alphabet[rng.Intn(len(alphabet))]
			}
			probes = append(probes, firstSetupProbe{Width: 100, Bindings: b, Keys: keys})
		}
	}
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/first_time_setup.mjs", pigversion.UpstreamVersion)
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
	failures, submitted, cancelled, previewed := 0, 0, 0, 0
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
			OnSubmit: func(r FirstTimeSetupResult) {
				events = append(events, []any{"submit", r.Theme, r.ShareAnalytics})
			},
			OnCancel: func() { events = append(events, []any{"cancel"}) },
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
				case "preview":
					previewed++
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
			failures++
			if failures <= 3 {
				t.Errorf("probe %d bindings=%v keys=%q\nPig steps %v\nPi  steps %v\nPig last frame %q\nPi  last frame %q", i, probe.Bindings, probe.Keys, got.Steps, want.Steps, got.Frames[len(got.Frames)-1], want.Frames[len(want.Frames)-1])
			}
		}
	}
	if submitted == 0 || cancelled == 0 || previewed == 0 {
		t.Errorf("probes never reached submit/cancel/preview: %d %d %d", submitted, cancelled, previewed)
	}
	t.Logf("%d probes, %d failures; Pi submits %d, cancels %d, previews %d", len(probes), failures, submitted, cancelled, previewed)
}
