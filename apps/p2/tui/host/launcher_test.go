package host_test

import (
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/host"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/prefs"
	tuiruntime "github.com/MichaelKinsy/PiG/apps/p2/tui/runtime"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/vterm"
	chordhost "github.com/MichaelKinsy/PiG/chord/next/host"
)

func TestMain(m *testing.M) { p2test.Main(m) }

// recordingTerminal is the visitor's terminal: a virtual terminal that remembers what was written to it and the titles it was given, with a settable
// kitty keyboard negotiation.
type recordingTerminal struct {
	*vterm.Terminal
	mu     sync.Mutex
	writes []string
	titles []string
	stops  int
	kitty  bool
}

func newRecordingTerminal(columns, rows int) *recordingTerminal {
	return &recordingTerminal{Terminal: vterm.New(columns, rows), kitty: true}
}

func (r *recordingTerminal) Write(data string) {
	r.mu.Lock()
	r.writes = append(r.writes, data)
	r.mu.Unlock()
	r.Terminal.Write(data)
}

func (r *recordingTerminal) SetTitle(title string) {
	r.mu.Lock()
	r.titles = append(r.titles, title)
	r.mu.Unlock()
	r.Terminal.SetTitle(title)
}

// Stop counts the stops: the visitor's terminal is restored exactly once.
func (r *recordingTerminal) Stop() {
	r.mu.Lock()
	r.stops++
	r.mu.Unlock()
	r.Terminal.Stop()
}

func (r *recordingTerminal) stopCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.stops
}

func (r *recordingTerminal) KittyProtocolActive() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.kitty
}

func (r *recordingTerminal) written() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return strings.Join(r.writes, "")
}

func (r *recordingTerminal) clear() {
	r.mu.Lock()
	r.writes = nil
	r.mu.Unlock()
}

func (r *recordingTerminal) titleList() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.titles...)
}

// screen is the visible screen as text.
func (r *recordingTerminal) screen() string { return strings.Join(r.Terminal.Viewport(), "\n") }

func poll(t *testing.T, cond func() bool) {
	t.Helper()
	pollFor(t, 10*time.Second, cond)
}

func pollFor(t *testing.T, limit time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(limit)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("poll: the condition never held")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// launcher is a running client on a real server (no plugins: the runtime plays the worker).
type launcher struct {
	t        *testing.T
	fixture  *p2test.Fixture
	url      *url.URL
	terminal *recordingTerminal
	client   *host.Client
	mu       sync.Mutex
	copies   []string
	opened   []string
}

type launcherOptions struct {
	mouse       host.MousePolicy
	role        string
	preferences *prefs.TuiLocalPreferences
	actions     func(base map[string]host.LauncherAction) map[string]host.LauncherAction
	facets      []chordhost.HostFacet
	columns     int
	rows        int
	kitty       *bool
	options     func(*host.ClientOptions)
	// fixture is a server the test started itself (default: one without plugins); address is the URL the launcher is given (default: the fixture's loopback URL).
	fixture *p2test.Fixture
	address *url.URL
	// offline: the server is not expected to speak Chord, so the launcher is not waited for to connect.
	offline bool
}

func startLauncher(t *testing.T, options launcherOptions) *launcher {
	t.Helper()
	fixture := options.fixture
	if fixture == nil {
		fixture = p2test.StartFixture(t, hosts.ServerOptions{Plugins: []string{}})
	}
	address := options.address
	if address == nil {
		var err error
		address, err = url.Parse("http://127.0.0.1:" + strconv.Itoa(fixture.Server.Port))
		if err != nil {
			t.Fatal(err)
		}
	}
	columns, rows := options.columns, options.rows
	if columns == 0 {
		columns, rows = 80, 24
	}
	l := &launcher{t: t, fixture: fixture, url: address, terminal: newRecordingTerminal(columns, rows)}
	if options.kitty != nil {
		l.terminal.kitty = *options.kitty
	}
	role := options.role
	if role == "" {
		role = "owner"
	}
	actions := host.LauncherActions(host.LauncherActionOptions{
		URL: address, Role: role,
		Clipboard: func(text string, _ func(string)) error {
			l.mu.Lock()
			l.copies = append(l.copies, text)
			l.mu.Unlock()
			return nil
		},
		OpenBrowser: func(target string) {
			l.mu.Lock()
			l.opened = append(l.opened, target)
			l.mu.Unlock()
		},
	})
	if options.actions != nil {
		actions = options.actions(actions)
	}
	clientOptions := host.ClientOptions{
		Terminal: l.terminal, Facets: options.facets, Actions: actions, Mouse: options.mouse, Role: role, URL: address,
		ColorQuery: 10 * time.Millisecond, Poll: time.Minute,
	}
	if options.preferences != nil {
		clientOptions.Preferences = &host.PreferenceSource{Current: *options.preferences}
	}
	if options.options != nil {
		options.options(&clientOptions)
	}
	client, err := host.StartClient(clientOptions)
	if err != nil {
		t.Fatal(err)
	}
	l.client = client
	t.Cleanup(client.Close)
	if !options.offline {
		poll(t, client.Connected)
	}
	return l
}

func (l *launcher) copied() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.copies...)
}

// grant is what the runtime's request for a launcher action answers (the worker's `request`), run off the owner loop as the runtime does.
func (l *launcher) grant(action string, payload any) chan tuiruntime.GrantResult {
	answer := make(chan tuiruntime.GrantResult, 1)
	go func() { answer <- l.client.Runtime().OnGrant(action, payload) }()
	return answer
}

func awaitAnswer(t *testing.T, answer chan tuiruntime.GrantResult) tuiruntime.GrantResult {
	t.Helper()
	select {
	case value := <-answer:
		return value
	case <-time.After(10 * time.Second):
		t.Fatal("the launcher never answered")
		return tuiruntime.GrantResult{}
	}
}

type chan_ = chan tuiruntime.GrantResult

func contains(text, part string) bool { return strings.Contains(text, part) }

func containsAll(text string, parts ...string) bool {
	for _, part := range parts {
		if !strings.Contains(text, part) {
			return false
		}
	}
	return true
}

func record(value any) map[string]any {
	m, _ := value.(map[string]any)
	return m
}

func mustURL(text string) *url.URL {
	u, err := url.Parse(text)
	if err != nil {
		panic(err)
	}
	return u
}

func strconvItoa(n int) string { return strconv.Itoa(n) }
