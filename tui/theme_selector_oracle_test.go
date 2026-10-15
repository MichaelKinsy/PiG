package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

type themeSelProbe struct {
	Theme    string              `json:"theme"`
	Width    int                 `json:"width"`
	Current  string              `json:"current"`
	Bindings map[string][]string `json:"bindings,omitempty"`
	Keys     []string            `json:"keys"`
	// ExtraThemes are registered in both implementations beside the built-in ones; empty is the two built-in themes.
	ExtraThemes []string `json:"extraThemes,omitempty"`
}

type themeSelResult struct {
	Steps  [][]string `json:"steps"`
	Frames [][]string `json:"frames"`
}

// randomThemeSelectorProbes registers 0 to 25 extra themes with random names (wide, combining, long, case variants), a current theme among them or unknown,
// widths from 6 to 100, bindings and key scripts. PageUp and PageDown stay out: Pi 1.1.0 has no select page keys and Pig pages the selection (D155, Pi PR #7680).
func randomThemeSelectorProbes() []themeSelProbe {
	rng := rand.New(rand.NewSource(20261027))
	pieces := []string{"custom", "theme", "Solar", "dracula", "日本語", "e\u0301", "😀", "very-long-theme-name-0123456789-abcdefghijklmnop", "-", "_", " ", "Z", "9"}
	keys := []string{"\x1b[A", "\x1b[B", "\r", "\n", "\x1b", "\x03", "j", "k", "\x0e", "\x10", "x", "\x1b[H", "\x1b[F", "\t"}
	bindings := []map[string][]string{nil, {"tui.select.confirm": {"ctrl+s"}}, {"tui.select.down": {"ctrl+n", "j"}, "tui.select.up": {"k"}}, {"tui.select.cancel": {"ctrl+x"}}}
	var probes []themeSelProbe
	for range 300 {
		var extra []string
		for range rng.Intn(26) {
			var b strings.Builder
			for range 1 + rng.Intn(4) {
				b.WriteString(pieces[rng.Intn(len(pieces))])
			}
			extra = append(extra, b.String())
		}
		current := []string{"dark", "light", "nope"}[rng.Intn(3)]
		if len(extra) > 0 && rng.Intn(2) == 0 {
			current = extra[rng.Intn(len(extra))]
		}
		script := make([]string, rng.Intn(16))
		for i := range script {
			script[i] = keys[rng.Intn(len(keys))]
		}
		probes = append(probes, themeSelProbe{Theme: []string{"dark", "light"}[rng.Intn(2)], Width: 2 + rng.Intn(99), Current: current, Bindings: bindings[rng.Intn(len(bindings))], Keys: script, ExtraThemes: extra})
	}
	return probes
}

// theme-selector.ts (a bordered SelectList of the available themes with the current one marked, previewing on every selection change) against pinned Pi: every frame and the select and
// cancel callbacks after each key, with default and rebound select keys.
func TestThemeSelectorMatchesPi(t *testing.T) {
	scripts := [][]string{
		nil,
		{"\x1b[A"},
		{"\x1b[B"},
		{"\x1b[B", "\x1b[B", "\x1b[B"},
		{"\x1b[A", "\x1b[A", "\r"},
		{"\r"},
		{"\x1b[B", "\r"},
		{"\n"},
		{"\x1b"},
		{"\x03"},
		{"y"},
		// Pi 1.1.0 has no select page keys; Pig pages the selection (D155, Pi PR #7680), covered by the select page key tests, so the oracle input leaves PageUp and PageDown out.
		{"\x1b[H", "\x1b[F", "\r"},
		{"\x1b[B", "x", "\r", "\x1b"},
		{"\x13"},
		{"\x1b[B", "\x13"},
		{"\x18"},
		{"j", "j"},
		{"\x0e", "k", "\r"},
	}
	bindings := []map[string][]string{
		nil,
		{"tui.select.confirm": {"ctrl+s"}},
		{"tui.select.cancel": {"ctrl+x"}},
		{"tui.select.down": {"ctrl+n", "j"}, "tui.select.up": {"k"}},
	}
	var probes []themeSelProbe
	for _, theme := range []string{"dark", "light"} {
		for _, width := range []int{70, 24, 8} {
			for _, current := range []string{"dark", "light", "nope"} {
				for _, b := range bindings {
					for _, keys := range scripts {
						probes = append(probes, themeSelProbe{Theme: theme, Width: width, Current: current, Bindings: b, Keys: keys})
					}
				}
			}
		}
	}
	// More themes than the list shows at once (maxVisible 10, theme-selector.ts:38) and names wider than the primary column's cap (THEME_SELECT_LIST_LAYOUT max 32,
	// theme-selector.ts:5-8), so the scrolling window, the scroll hint, the truncation and the paging keys are all observable.
	var extra []string
	for i := range 13 {
		extra = append(extra, fmt.Sprintf("custom-theme-%02d", i))
	}
	extra = append(extra, "an-extremely-long-theme-name-that-exceeds-the-primary-column-cap")
	for _, width := range []int{70, 24} {
		for _, current := range []string{"dark", "custom-theme-07", "an-extremely-long-theme-name-that-exceeds-the-primary-column-cap", "nope"} {
			for _, keys := range scripts {
				probes = append(probes, themeSelProbe{Theme: "dark", Width: width, Current: current, Keys: keys, ExtraThemes: extra})
			}
		}
	}
	probes = append(probes, randomThemeSelectorProbes()...)
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/theme_selector.mjs", pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []themeSelResult
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	previousTheme, previousCaps, previousRegistry := ActiveTheme(), GetCapabilities(), globalRegistry.Load()
	// Pi runs without the Kitty keyboard protocol, so LF is Enter; an earlier test may have left it active.
	previousKitty := IsKittyProtocolActive()
	SetKittyProtocolActive(false)
	restoreKeybindingsAfterTest(t)
	t.Cleanup(func() {
		SetKittyProtocolActive(previousKitty)
		SetCapabilities(previousCaps)
		SetTheme(previousTheme.Name)
		globalRegistry.Store(previousRegistry)
	})
	failures := 0
	for i, probe := range probes {
		SetCapabilities(TerminalCapabilities{TrueColor: true})
		SetTheme(probe.Theme)
		SetKeybindings(NewKeybindingsManager(TUIKeybindingDefinitionsFor(HostKeybindingPlatform()), probe.Bindings))
		registry := NewThemeRegistry()
		for _, name := range probe.ExtraThemes {
			custom := *builtinDarkTheme()
			custom.Name = name
			registry.Add(&custom)
		}
		globalRegistry.Store(registry)
		var events []string
		selector := NewThemeSelectorComponent(probe.Current,
			func(name string) { events = append(events, "select:"+name) },
			func() { events = append(events, "cancel") },
			func(name string) { events = append(events, "preview:"+name) })
		got := themeSelResult{Steps: [][]string{}, Frames: [][]string{selector.Render(probe.Width)}}
		for _, key := range probe.Keys {
			selector.GetSelectList().HandleInput(key)
			got.Steps = append(got.Steps, append([]string{}, events...))
			got.Frames = append(got.Frames, selector.Render(probe.Width))
		}
		if !reflect.DeepEqual(got.Steps, expected[i].Steps) || !reflect.DeepEqual(got.Frames, expected[i].Frames) {
			if failures++; failures <= 3 {
				t.Errorf("probe %d %+v:\n steps Pig %q Pi %q\n frames Pig %q\n Pi  %q", i, probe, got.Steps, expected[i].Steps, got.Frames, expected[i].Frames)
			}
		}
	}
	if failures > 3 {
		t.Errorf("%d of %d probes differ from Pi", failures, len(probes))
	}
}
