package host_test

// pi: apps/p2/test/p2/tui-theme.test.ts "terminal color probe consumes replies, never forwards them, and tracks scheme reports"

import (
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/tui/host"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/theme"
	pitui "github.com/MichaelKinsy/PiG/tui"
)

func TestTerminalColorProbeConsumesRepliesNeverForwardsAndTracksSchemeReports(t *testing.T) {
	var written []string
	var states []theme.TerminalState
	probe := host.NewTerminalColorProbe(host.TerminalColorProbeOptions{
		Write:    func(data string) { written = append(written, data) },
		OnUpdate: func(state theme.TerminalState) { states = append(states, state) },
	})
	probe.Start()
	if !strings.HasPrefix(strings.Join(written, ""), "\x1b[?2031h\x1b]10;?\x07") {
		t.Fatalf("start wrote %q", written)
	}
	if !probe.Consume("\x1b]11;rgb:ffff/ffff/ffff\x07") {
		t.Fatal("the background reply was not consumed")
	}
	if !probe.Consume("\x1b]10;#000000\x1b\\") {
		t.Fatal("the foreground reply was not consumed")
	}
	if probe.Consume("a") {
		t.Fatal("ordinary input was consumed")
	}
	if !probe.Consume("\x1b[?62;22c") {
		t.Fatal("the device attributes reply was not consumed")
	}
	got := states[len(states)-1].Colors
	want := pitui.TerminalColors{Foreground: &pitui.RgbColor{}, Background: &pitui.RgbColor{R: 255, G: 255, B: 255}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("colors = %+v, want %+v", got, want)
	}
	if !probe.Consume("\x1b[?997;2n") {
		t.Fatal("the scheme report was not consumed")
	}
	if probe.State().Scheme != "light" {
		t.Fatalf("scheme = %q", probe.State().Scheme)
	}
	probe.Stop()
	if !strings.Contains(written[len(written)-1], "\x1b[?2031l") {
		t.Fatalf("stop wrote %q", written[len(written)-1])
	}
	if rgb, ok := host.ParseOscColor("rgb:80/80/80"); !ok || rgb != (pitui.RgbColor{R: 128, G: 128, B: 128}) {
		t.Fatalf("ParseOscColor = %v %v", rgb, ok)
	}
	if _, ok := host.ParseOscColor("bogus"); ok {
		t.Fatal("ParseOscColor accepted bogus")
	}
	if scheme, _ := host.ColorFgBgScheme(map[string]string{"COLORFGBG": "15;0"}); scheme != "dark" {
		t.Fatalf("COLORFGBG 15;0 = %q", scheme)
	}
	if scheme, _ := host.ColorFgBgScheme(map[string]string{"COLORFGBG": "0;15"}); scheme != "light" {
		t.Fatalf("COLORFGBG 0;15 = %q", scheme)
	}
}
