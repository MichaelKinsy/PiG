package codingagent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"slices"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
	"github.com/MichaelKinsy/PiG/tui"
)

type radiusLoginProbe struct {
	Theme     string         `json:"theme"`
	TrueColor bool           `json:"trueColor"`
	Title     string         `json:"title"`
	Options   []string       `json:"options"`
	Radius    map[string]any `json:"radius"`
	Downs     int            `json:"downs"`
	ElapsedMs int            `json:"elapsedMs"`
	Width     int            `json:"width"`
}

// radius-login-selector.ts radiusShimmer/RadiusLoginMenuComponent.render: every
// frame of the selected Radius row, in every color mode and at widths that wrap
// the label, equals the pinned Pi's rendered rows; an unselected row renders
// as the plain selector does.
func TestRadiusLoginMenuRendersLikePi(t *testing.T) {
	text := "Sign in with Radius"
	label := text + tui.FormatAuthSelectorProviderStatus(tui.OAuthProvider{ID: "radius", Name: "Radius", AuthType: "oauth"})
	options := []string{"Sign in with an account", "Sign in with an API key", label}
	var probes []radiusLoginProbe
	for _, theme := range []string{"dark", "light"} {
		for _, trueColor := range []bool{true, false} {
			for _, width := range []int{12, 30, 120} {
				for _, downs := range []int{0, 2} {
					for _, elapsed := range []int{0, 50, 137, 400, 1000, 12345, 3600000} {
						probes = append(probes, radiusLoginProbe{Theme: theme, TrueColor: trueColor, Title: "Select authentication method:", Options: options, Radius: map[string]any{"label": label, "text": text}, Downs: downs, ElapsedMs: elapsed, Width: width})
					}
				}
			}
		}
	}
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/radius-login-oracle.mjs", pigversion.UpstreamVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected [][]string
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	if len(expected) != len(probes) {
		t.Fatalf("oracle returned %d results for %d probes", len(expected), len(probes))
	}
	previousBindings, previousTheme, previousCaps := tui.GetKeybindings(), tui.ActiveTheme().Name, tui.GetCapabilities()
	t.Cleanup(func() {
		tui.SetKeybindings(previousBindings)
		tui.SetCapabilities(previousCaps)
		tui.SetTheme(previousTheme)
	})
	for i, probe := range probes {
		tui.SetCapabilities(tui.TerminalCapabilities{TrueColor: probe.TrueColor})
		tui.SetTheme(probe.Theme)
		tui.SetKeybindings(tui.NewTUIKeybindingsManager(nil))
		selector := tui.NewExtensionSelectorComponent(probe.Title, probe.Options, nil, nil)
		menu := newRadiusLoginMenu(selector, label, text, func() {})
		elapsed := time.Duration(probe.ElapsedMs) * time.Millisecond
		menu.now = func() time.Time { return menu.start.Add(elapsed) }
		for range probe.Downs {
			selector.HandleInput("\x1b[B")
		}
		got := menu.Render(probe.Width)
		menu.Dispose()
		if !slices.Equal(got, expected[i]) {
			t.Errorf("%s: probe %d %+v:\n got %q\n Pi  %q", fmt.Sprint(probe.Theme), i, probe, got, expected[i])
		}
	}
}
