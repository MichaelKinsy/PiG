package host_test

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	sessionscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/host"
	"github.com/MichaelKinsy/PiG/chord/delta"

	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/chat-mode"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/conversations"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/status"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/subagents"
)

// The "real worker" tests of Pi drive the server-shipped TUI (the bundled sessions and chat surfaces) on a real server with the fake provider. The same here: the
// plugins' compiled-in TUI facets on a real server, a session created before the client starts, and the visitor's keys on a virtual terminal.

const (
	keyEnter = "\r"
	keyEsc   = "\x1b"
	keyDown  = "\x1b[B"
	keyCtrlG = "\x07"
	keyCtrlX = "\x18"
)

var realPlugins = []string{"chat", "chat-mode", "conversations", "drafts", "envs", "files", "models", "sessions", "sharing", "status", "subagents"}

// startReal is a launcher with the real surfaces on a real server that has one session (Pi's `startFixture` + `sessions.create`).
func startReal(t *testing.T, options launcherOptions) *launcher {
	t.Helper()
	fixture := p2test.StartFixture(t, hosts.ServerOptions{Plugins: realPlugins})
	sessions, err := p2test.HoldIn(fixture.Browser(), sessionscontract.Sessions, delta.NewJsonObject(0), "owner")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sessions.Call("create", delta.JsonObjectOf("cwd", fixture.Workspace), p2test.Background); err != nil {
		t.Fatal(err)
	}
	selection, err := hosts.SelectPlugins(realPlugins)
	if err != nil {
		t.Fatal(err)
	}
	options.fixture, options.facets = fixture, hosts.TUIFacets(selection)
	if options.columns == 0 {
		options.columns, options.rows = 100, 30
	}
	return startLauncher(t, options)
}

func (l *launcher) typed(text string) {
	for _, char := range text {
		l.terminal.SendInput(string(char))
	}
}

func (l *launcher) waitScreen(limit time.Duration, text string) {
	l.t.Helper()
	pollFor(l.t, limit, func() bool { return strings.Contains(l.terminal.screen(), text) })
}

// openNewestSession is the keys of the tests: the newest session is first after "+ New session".
func (l *launcher) openNewestSession() {
	l.t.Helper()
	l.waitScreen(15*time.Second, "New session")
	l.terminal.SendInput(keyDown)
	l.terminal.SendInput(keyEnter)
	pollFor(l.t, 10*time.Second, func() bool {
		screen := l.terminal.screen()
		return !strings.Contains(screen, "Connecting conversation") && strings.Contains(screen, "•")
	})
}

// draft is the draft of the viewed conversation (Pi's snapshot `drafts`).
func (l *launcher) draft() string {
	snapshot := l.client.Snapshot()
	for _, pair := range snapshot.Drafts {
		if pair.Key == snapshot.Session+"#1" {
			return pair.Value
		}
	}
	return ""
}

func TestRealWorkerCtrlXCopiesTheLastAnswerCopyAsksFirstCtrlGEditsTheDraftInTheExternalEditor(t *testing.T) {
	l := startReal(t, launcherOptions{actionOptions: func(options *host.LauncherActionOptions) {
		options.Editor = []string{"fake-editor"}
		options.RunEditor = func(_ []string, file string) (int, bool) {
			text, err := os.ReadFile(file)
			if err != nil || os.WriteFile(file, append(text, " edited"...), 0o600) != nil {
				return 1, false
			}
			return 0, true
		}
	}})
	l.openNewestSession()
	l.typed("hello")
	l.terminal.SendInput(keyEnter)
	l.waitScreen(20*time.Second, "Echo: hello")
	time.Sleep(300 * time.Millisecond)
	l.terminal.SendInput(keyCtrlX)
	poll(t, func() bool { return len(l.copied()) == 1 })
	if got := l.copied()[0]; got != "Echo: hello" {
		t.Fatalf("copied %q", got)
	}
	time.Sleep(1500 * time.Millisecond)
	l.typed("/copy")
	l.terminal.SendInput(keyEnter)
	l.waitScreen(10*time.Second, "Copy 11 characters to the clipboard")
	if len(l.copied()) != 1 {
		t.Fatalf("copied before confirming: %v", l.copied())
	}
	l.terminal.SendInput("y")
	poll(t, func() bool { return len(l.copied()) == 2 })
	time.Sleep(1500 * time.Millisecond)
	l.typed("draft")
	poll(t, func() bool { return l.draft() == "draft" })
	l.terminal.SendInput(keyCtrlG)
	poll(t, func() bool { return l.draft() == "draft edited" })
	l.waitScreen(10*time.Second, "draft edited")
}
