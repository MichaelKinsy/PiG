package host_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/tui/host"
)

func press(x, y int) string   { return fmt.Sprintf("\x1b[<0;%d;%dM", x, y) }
func drag(x, y int) string    { return fmt.Sprintf("\x1b[<32;%d;%dM", x, y) }
func release(x, y int) string { return fmt.Sprintf("\x1b[<0;%d;%dm", x, y) }
func wheelUp(x, y int) string { return fmt.Sprintf("\x1b[<64;%d;%dM", x, y) }

func TestSelectionGestureADragsOrAMultiClicksReleaseNeverAPlainClickWheelOrOtherButton(t *testing.T) {
	gesture := &host.SelectionGesture{}
	at := func(ms int) time.Time { return time.UnixMilli(int64(ms)) }
	for _, step := range []struct {
		data string
		ms   int
		want bool
		note string
	}{
		{press(5, 5), 0, false, "press"},
		{drag(9, 5), 10, false, "motion"},
		{release(9, 5), 20, true, "drag"},
		{press(20, 8), 2000, false, "press"},
		{release(20, 8), 2010, false, "single click"},
		{press(20, 8), 2100, false, "press"},
		{release(20, 8), 2110, true, "double click"},
		{release(1, 1), 3000, false, "release without a press"},
		{wheelUp(3, 3) + wheelUp(3, 3), 4000, false, "wheel"},
		{"\x1b[<2;4;4M\x1b[<34;6;4M\x1b[<2;6;4m", 5000, false, "right button drag"},
		{press(4, 4) + drag(4, 4) + release(4, 4), 6000, false, "motion without moving"},
	} {
		if got := gesture.Feed(step.data, at(step.ms)); got != step.want {
			t.Fatalf("%s: %v, want %v", step.note, got, step.want)
		}
	}
	lookup := func(env map[string]string) func(string) (string, bool) {
		return func(name string) (string, bool) { value, ok := env[name]; return value, ok }
	}
	for _, c := range []struct {
		env  map[string]string
		want host.MousePolicy
	}{
		{map[string]string{"TMUX": "/tmp/x"}, host.MouseOn},
		{map[string]string{"TERM": "screen-256color"}, host.MouseOn},
		{map[string]string{"TERM": "xterm-256color"}, host.MouseAll},
	} {
		if got := host.DefaultMouse(lookup(c.env)); got != c.want {
			t.Fatalf("%v: %q", c.env, got)
		}
	}
}

func TestLauncherMouseReportsReachTheWorkerPiTuisCopyIsAcceptedOnlyAfterTheLauncherSawTheRelease(t *testing.T) {
	l := startLauncher(t, launcherOptions{mouse: host.MouseOn})
	if !containsAll(l.terminal.written(), "\x1b[?1000h", "\x1b[?1002h", "\x1b[?1004h", "\x1b[?1006h") {
		t.Fatalf("mouse modes missing from %q", l.terminal.written())
	}
	if contains(l.terminal.written(), "\x1b[?1003h") {
		t.Fatal("all-motion needs the 'all' policy")
	}
	request := func() chan_ { return l.grant("copy.selection", map[string]any{"text": "selected text"}) }
	// No gesture: a worker request on its own is denied.
	if got := awaitAnswer(t, request()); got.Error != "denied" {
		t.Fatalf("no gesture: %+v", got)
	}
	// A wheel notch and a plain click: forwarded, the click's release opens no window.
	for _, data := range []string{wheelUp(5, 5), press(5, 5), release(5, 5)} {
		l.terminal.SendInput(data)
	}
	if got := awaitAnswer(t, request()); got.Error != "denied" || len(l.copied()) != 0 {
		t.Fatalf("plain click: %+v %v", got, l.copied())
	}
	// A drag: the release the launcher saw permits exactly this copy.
	for _, data := range []string{press(5, 5), drag(12, 6), release(12, 6)} {
		l.terminal.SendInput(data)
	}
	if got := awaitAnswer(t, request()); !got.OK {
		t.Fatalf("drag: %+v", got)
	}
	if copies := l.copied(); len(copies) != 1 || copies[0] != "selected text" {
		t.Fatalf("copies = %v", copies)
	}
	// The window is single-use.
	if got := awaitAnswer(t, request()); got.Error != "denied" || len(l.copied()) != 1 {
		t.Fatalf("second request: %+v", got)
	}
	l.terminal.clear()
	l.client.Close()
	if !containsAll(l.terminal.written(), "\x1b[?1000l\x1b[?1002l\x1b[?1004l\x1b[?1006l") {
		t.Fatalf("modes not undone: %q", l.terminal.written())
	}
}

func TestLauncherWithTheMouseOffPiTuiDoesNotCaptureItAndNoGestureOpensACopyWindow(t *testing.T) {
	all := startLauncher(t, launcherOptions{mouse: host.MouseAll})
	if !contains(all.terminal.written(), "\x1b[?1003h") {
		t.Fatalf("all policy: %q", all.terminal.written())
	}
	none := startLauncher(t, launcherOptions{mouse: host.MouseOff})
	if contains(none.terminal.written(), "\x1b[?1000h") {
		t.Fatal("pi-tui captured the mouse with the policy off")
	}
	for _, data := range []string{press(5, 5), drag(12, 6), release(12, 6)} {
		none.terminal.SendInput(data)
	}
	if got := awaitAnswer(t, none.grant("copy.selection", map[string]any{"text": "selected text"})); got.Error != "denied" || len(none.copied()) != 0 {
		t.Fatalf("mouse off: %+v", got)
	}
}
