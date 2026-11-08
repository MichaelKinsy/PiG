package desktop_test

import (
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	chatcontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/chat/contract"
	sessionscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	"github.com/MichaelKinsy/PiG/apps/p2desktop/internal/desktop"
	"github.com/MichaelKinsy/PiG/apps/p2desktop/shell"
	"github.com/MichaelKinsy/PiG/apps/p2desktop/shell/fakeshell"
	"github.com/MichaelKinsy/PiG/chord/delta"

	// The plugins the desktop app's server loads (cmd/p2-desktop/plugins.go), registered in this test binary, which is also
	// the session worker.
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/chat"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/drafts"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/envs"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/models"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/status"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/subagents"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/web-shell"
)

func TestMain(m *testing.M) { p2test.Main(m) }

func object(value any) *delta.JsonObject { o, _ := value.(*delta.JsonObject); return o }

// run starts an app on the fake shell and waits for the server; stop quits it and returns Run's result.
type running struct {
	app   *desktop.App
	shell *fakeshell.Platform
	done  chan error
	port  int
	data  string
	state string
}

func start(t *testing.T, mutate func(*desktop.Config, *fakeshell.Platform)) *running {
	t.Helper()
	fake := true
	zero := 0
	root := t.TempDir()
	r := &running{shell: fakeshell.New(), done: make(chan error, 1), data: root + "/data", state: root + "/state"}
	cfg := desktop.Config{Platform: r.shell, DataDir: r.data, StateDir: r.state, Fake: &fake, Port: &zero, LeakCheck: true,
		Errors: func(format string, args ...any) { t.Logf("desktop: "+format, args...) }}
	if mutate != nil {
		mutate(&cfg, r.shell)
	}
	cfg.Platform = r.shell
	r.app = desktop.New(cfg)
	go func() { r.done <- r.app.Run() }()
	select {
	case <-r.app.Ready():
	case err := <-r.done:
		t.Fatalf("Run returned before the server was ready: %v", err)
	case <-time.After(30 * time.Second):
		t.Fatal("the server did not become ready")
	}
	if origin := r.app.Origin(); origin != "" {
		_, port, _ := net.SplitHostPort(strings.TrimPrefix(origin, "http://"))
		r.port, _ = strconv.Atoi(port)
	}
	return r
}

func (r *running) stop(t *testing.T) {
	t.Helper()
	r.shell.Quit()
	select {
	case err := <-r.done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the app did not quit")
	}
	if err := r.app.LeakError(); err != nil {
		t.Fatalf("leak check: %v", err)
	}
}

func (r *running) window(t *testing.T) *fakeshell.Window {
	t.Helper()
	windows, _, _, _ := r.shell.Snapshot()
	if len(windows) == 0 {
		t.Fatal("no window")
	}
	return windows[len(windows)-1]
}

func TestLaunchShowsTheServerAndQuitsCleanly(t *testing.T) {
	r := start(t, nil)
	win := r.window(t)
	if want := r.app.Origin() + "/"; win.CurrentURL() != want {
		t.Fatalf("window shows %q, want %q", win.CurrentURL(), want)
	}
	if !strings.HasPrefix(r.app.Origin(), "http://127.0.0.1:") {
		t.Fatalf("origin %q is not loopback", r.app.Origin())
	}
	// The window page is p2's own: /health answers on the same origin as the owner does.
	resp, err := http.Get(r.app.Origin() + "/health")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(body), `"name":"p2"`) {
		t.Fatalf("/health: %d %s", resp.StatusCode, body)
	}
	// And serves p2's UI at the page the window shows.
	page, err := http.Get(win.CurrentURL())
	if err != nil {
		t.Fatal(err)
	}
	html, _ := io.ReadAll(page.Body)
	_ = page.Body.Close()
	if page.StatusCode != 200 || !strings.Contains(string(html), "<title>") {
		t.Fatalf("GET %s: %d %.200s", win.CurrentURL(), page.StatusCode, html)
	}
	// Binds the loopback interface only.
	if conn, err := net.DialTimeout("tcp", net.JoinHostPort(outboundIP(), strconv.Itoa(r.port)), 300*time.Millisecond); err == nil {
		_ = conn.Close()
		t.Fatalf("the server listens beyond loopback")
	}
	r.stop(t)
	if conn, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(r.port), 500*time.Millisecond); err == nil {
		_ = conn.Close()
		t.Fatal("the port is still open after quit")
	}
}

// outboundIP is an address of this machine other than loopback, or a TEST-NET address that nothing answers on.
func outboundIP() string {
	addrs, _ := net.InterfaceAddrs()
	for _, addr := range addrs {
		if ip, ok := addr.(*net.IPNet); ok && !ip.IP.IsLoopback() && ip.IP.To4() != nil {
			return ip.IP.String()
		}
	}
	return "192.0.2.1"
}

func TestChatRoundTripAndNotification(t *testing.T) {
	r := start(t, nil)
	win := r.window(t)
	conn, err := p2test.Dial(r.port, nil)
	if err != nil {
		t.Fatal(err)
	}
	browser, err := p2test.ClientHost(conn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = browser.Close(p2test.Background) }()
	sessions, err := p2test.HoldIn(browser, sessionscontract.Sessions, delta.NewJsonObject(0), "owner")
	if err != nil {
		t.Fatal(err)
	}
	created, err := sessions.Call("create", delta.JsonObjectOf("cwd", t.TempDir(), "title", "hello"), p2test.Background)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := object(created).Value("id").(string)
	chat, err := p2test.HoldIn(browser, chatcontract.Chat, delta.JsonObjectOf("session", id, "conversation", float64(1)), "participant")
	if err != nil {
		t.Fatal(err)
	}
	// The window is behind: a finished run is announced; one in front is not.
	win.SetFocused(false)
	if _, err := chat.Call("send", delta.JsonObjectOf("text", "hello", "requestId", "r1"), p2test.Background); err != nil {
		t.Fatal(err)
	}
	if _, err := p2test.Until(chat.Member("view"), func(view delta.JsonValue) bool {
		entries, _ := object(view).Value("entries").([]any)
		for _, entry := range entries {
			model, _ := object(entry).Value("model").([]any)
			if len(model) > 0 && object(model[0]).Value("role") == "assistant" {
				return true
			}
		}
		return false
	}, 0); err != nil {
		t.Fatalf("no answer from the faux provider: %v", err)
	}
	var note shell.Notification
	deadline := time.Now().Add(20 * time.Second)
	for {
		_, notes, _, _ := r.shell.Snapshot()
		if len(notes) > 0 {
			note = notes[0]
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no notification for the finished session")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if note.ID != "session/"+id || note.Title != "Session finished" {
		t.Fatalf("notification %+v", note)
	}
	// A click on it opens that session in the window.
	r.shell.Handlers.NotificationClick(note.ID)
	if want := r.app.Origin() + "/s/" + id; win.CurrentURL() != want {
		t.Fatalf("after the click the window shows %q, want %q", win.CurrentURL(), want)
	}
	chat.Release()
	sessions.Release()
	r.stop(t)
}

func TestFocusedWindowGetsNoNotification(t *testing.T) {
	r := start(t, nil)
	r.window(t).SetFocused(true)
	conn, err := p2test.Dial(r.port, nil)
	if err != nil {
		t.Fatal(err)
	}
	browser, err := p2test.ClientHost(conn)
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := p2test.HoldIn(browser, sessionscontract.Sessions, delta.NewJsonObject(0), "owner")
	if err != nil {
		t.Fatal(err)
	}
	created, err := sessions.Call("create", delta.JsonObjectOf("cwd", t.TempDir(), "title", "quiet"), p2test.Background)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := object(created).Value("id").(string)
	chat, err := p2test.HoldIn(browser, chatcontract.Chat, delta.JsonObjectOf("session", id, "conversation", float64(1)), "participant")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := chat.Call("send", delta.JsonObjectOf("text", "hello", "requestId", "r1"), p2test.Background); err != nil {
		t.Fatal(err)
	}
	time.Sleep(3 * time.Second)
	if _, notes, _, _ := r.shell.Snapshot(); len(notes) != 0 {
		t.Fatalf("notified with the window in front: %+v", notes)
	}
	chat.Release()
	sessions.Release()
	_ = browser.Close(p2test.Background)
	r.stop(t)
}

func TestDeepLinks(t *testing.T) {
	r := start(t, nil)
	win := r.window(t)
	origin := r.app.Origin()
	id := "0b1c2d3e-4f50-4162-8394-a5b6c7d8e9f0"
	for link, want := range map[string]string{
		"pig://open":                   origin + "/",
		"pig://session/" + id:          origin + "/s/" + id,
		"pig://session/" + id + "/c/3": origin + "/s/" + id + "/c/3",
	} {
		r.shell.Handlers.OpenURL(link)
		if win.CurrentURL() != want {
			t.Errorf("%s: window shows %q, want %q", link, win.CurrentURL(), want)
		}
	}
	before := win.CurrentURL()
	for _, bad := range []string{
		"pig://session/../../etc", "pig://session/a%2Fb", "pig://session/" + id + "/c/0", "pig://session/" + id + "/x",
		"pig://evil.example/", "pig://open/x", "pig://user@open", "pig://open:80", "http://127.0.0.1/", "javascript:alert(1)",
		"pig://session/" + strings.Repeat("a", 200), "pig://session/", "pig:///s/x",
	} {
		r.shell.Handlers.OpenURL(bad)
		if win.CurrentURL() != before {
			t.Errorf("%s navigated to %q", bad, win.CurrentURL())
		}
	}
	// A second launch of the app (Windows, Linux) passes its link in its arguments.
	r.shell.Handlers.SecondInstance([]string{"--flag", "pig://session/" + id})
	if want := origin + "/s/" + id; win.CurrentURL() != want {
		t.Errorf("second instance: window shows %q, want %q", win.CurrentURL(), want)
	}
	r.stop(t)
}

func TestMacWindowReopensOnActivate(t *testing.T) {
	r := start(t, nil)
	first := r.window(t)
	first.Close()
	r.shell.Handlers.Activate()
	second := r.window(t)
	if second == first || second.CurrentURL() != r.app.Origin()+"/" {
		t.Fatalf("activate did not open a window on the server: %v", second.CurrentURL())
	}
	r.stop(t)
}

func TestTheServerPortIsRememberedAndNotRequired(t *testing.T) {
	var state string
	first := start(t, func(c *desktop.Config, _ *fakeshell.Platform) { c.Port = nil; state = c.StateDir })
	port := first.port
	first.stop(t)
	// The same port again, so the web UI keeps its per-origin settings.
	second := start(t, func(c *desktop.Config, _ *fakeshell.Platform) {
		c.Port = nil
		c.StateDir = state
		c.DataDir = first.data
	})
	if second.port != port {
		t.Logf("port %d was taken in between; got %d", port, second.port)
	}
	second.stop(t)
	// A taken port is not an error: the app takes another.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	taken := ln.Addr().(*net.TCPAddr).Port
	if err := os.WriteFile(state+"/state.json", []byte(`{"port":`+strconv.Itoa(taken)+`}`), 0o600); err != nil {
		t.Fatal(err)
	}
	third := start(t, func(c *desktop.Config, _ *fakeshell.Platform) {
		c.Port = nil
		c.StateDir = state
		c.DataDir = first.data
	})
	if third.port == taken || third.port == 0 {
		t.Fatalf("started on %d, which was taken", third.port)
	}
	third.stop(t)
}

func TestSecondProcessDoesNotStartAServer(t *testing.T) {
	p := fakeshell.New()
	p.NotHolder = true
	app := desktop.New(desktop.Config{Platform: p, DataDir: t.TempDir(), StateDir: t.TempDir()})
	if err := app.Run(); err != nil {
		t.Fatal(err)
	}
	if windows, _, _, _ := p.Snapshot(); len(windows) != 0 || app.Origin() != "" {
		t.Fatal("a second process opened a window or a server")
	}
}

func TestAnotherServerOnTheDataDirIsReportedAndQuits(t *testing.T) {
	first := start(t, nil)
	var second *running
	var once sync.Once
	done := make(chan struct{})
	go func() {
		defer close(done)
		p := fakeshell.New()
		app := desktop.New(desktop.Config{Platform: p, DataDir: first.data, StateDir: t.TempDir(), Port: new(int),
			Fake: func() *bool { b := true; return &b }(), Errors: func(string, ...any) {}})
		second = &running{app: app, shell: p}
		once.Do(func() {})
		_ = app.Run()
	}()
	select {
	case <-done:
	case <-time.After(60 * time.Second):
		t.Fatal("the second app did not give up")
	}
	_, _, _, errs := second.shell.Snapshot()
	if len(errs) != 1 || !strings.Contains(errs[0], "Another p2 server") {
		t.Fatalf("errors shown: %v", errs)
	}
	first.stop(t)
}
