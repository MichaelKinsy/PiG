//go:build linux || darwin

package viewendpoint

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/uiview"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

type testHost struct {
	reg *uiview.Registry
	inv uiview.Invoker
}

func (h *testHost) Views() *uiview.Registry { return h.reg }
func (h *testHost) InvokeViewIntentFrom(ctx context.Context, t *uiview.Attachment, extension string, ref uiview.Ref, nodeKey, intent string, arg json.RawMessage) uiview.Result {
	return t.Invoke(ctx, extension, ref, nodeKey, intent, arg, h.inv)
}

// testPlugin answers "add" at once, holds "wait" until its request is cancelled, and holds "big" until gate closes and
// then answers with 32768 bytes.
type testPlugin struct {
	started chan string
	gate    chan struct{}
}

func (p *testPlugin) Invoke(ctx context.Context, inv uiview.Invocation) (uiview.Result, error) {
	switch inv.Intent {
	case "wait":
		p.started <- inv.Ticket
		<-ctx.Done()
		return uiview.Result{}, ctx.Err()
	case "big":
		p.started <- inv.Ticket
		<-p.gate
		return uiview.Result{Outcome: uiview.OutcomeDone, Answer: json.RawMessage(`{"pad":"` + strings.Repeat("x", 32768) + `"}`)}, nil
	}
	return uiview.Result{Outcome: uiview.OutcomeDone, Answer: json.RawMessage(`{"sum":3}`)}, nil
}

const testAbilities = `{"slots":["panel"],"nodes":["text"],"markdown":false,"imageFormats":[],"limits":{"nodes":64,"depth":8,"snapshotBytes":1048576,"textBytes":1048576,"mounts":16,"intentArgBytes":64},"input":{"keyboard":true,"pointer":true,"text":true}}`

const testIntents = `,"intents":[{"name":"add","arg":{"type":"object"},"answer":{"type":"object","properties":{"sum":{"type":"integer"}},"required":["sum"]}},{"name":"wait","arg":{"type":"object"},"cancellable":true,"timeoutMs":60000},{"name":"big","arg":{"type":"object"},"answer":{"type":"object","properties":{"pad":{"type":"string"}},"required":["pad"]},"timeoutMs":60000}]`

type fixture struct {
	t      *testing.T
	dir    string
	e      *Endpoint
	h      *testHost
	p      *testPlugin
	mu     sync.Mutex
	notice []string
}

// shortDir returns a directory whose path leaves room for a socket name: a socket address holds 104 bytes on macOS.
func shortDir(t *testing.T) string {
	t.Helper()
	return testenv.ShortTempDir(t, "ve")
}

func newHost() (*testHost, *testPlugin) {
	p := &testPlugin{started: make(chan string, 1024), gate: make(chan struct{})}
	return &testHost{reg: uiview.New(uiview.Options{}), inv: p}, p
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	return newFixtureAt(t, filepath.Join(shortDir(t), "view-endpoint"))
}

// newFixtureAt is newFixture with the endpoint's directory at dir.
func newFixtureAt(t *testing.T, dir string) *fixture {
	t.Helper()
	f := &fixture{t: t, dir: dir}
	f.h, f.p = newHost()
	f.e = New(Options{Dir: f.dir, Notify: func(message string) {
		f.mu.Lock()
		f.notice = append(f.notice, message)
		f.mu.Unlock()
	}})
	t.Cleanup(f.e.Close)
	return f
}

func (f *fixture) notices() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.notice...)
}

// settle waits until the endpoint's worker has run every queued bind and unbind.
func (f *fixture) settle() {
	f.e.mu.Lock()
	for f.e.working {
		f.e.idle.Wait()
	}
	f.e.mu.Unlock()
}

// abilitiesChanged returns a channel that holds a token once the registry's abilities have changed since it was last
// received from: an attachment began or ended. It is made after the attachments a test waits on.
func (f *fixture) abilitiesChanged() <-chan struct{} {
	changed := make(chan struct{}, 1)
	f.h.reg.OnAbilities(func() {
		select {
		case changed <- struct{}{}:
		default:
		}
	})
	return changed
}

// awaitDetached waits until no client is attached, on the registry's own notice that its abilities changed, and reports
// false when the bound testbudget gives the wait passed first.
func (f *fixture) awaitDetached(changed <-chan struct{}) bool {
	f.t.Helper()
	budget := time.After(testbudget.Wait(f.t))
	for f.h.reg.Abilities() != nil {
		select {
		case <-changed:
		case <-budget:
			return false
		}
	}
	return true
}

// serve binds the fixture's host and returns the socket's path.
func (f *fixture) serve() string {
	f.t.Helper()
	f.e.Bind(f.h)
	f.settle()
	path := f.e.Path()
	if path == "" {
		f.t.Fatalf("no socket is served: %v", f.notices())
	}
	return path
}

func (f *fixture) mount(key, text string) uiview.Ref {
	f.t.Helper()
	a, err := uiview.DecodeMount([]byte(`{"key":"` + key + `","slot":"panel","view":{"root":{"kind":"text","text":"` + text + `"}}` + testIntents + `}`))
	if err != nil {
		f.t.Fatal(err)
	}
	st, err := f.h.reg.Mount("p", a)
	if err != nil {
		f.t.Fatal(err)
	}
	return st.Mount
}

func (f *fixture) update(ref uiview.Ref, text string) uiview.Ref {
	f.t.Helper()
	a, err := uiview.DecodeUpdate(fmt.Appendf(nil, `{"mount":{"key":%q,"epoch":%d,"rev":%d},"view":{"root":{"kind":"text","text":%q}}%s}`, ref.Key, ref.Epoch, ref.Rev, text, testIntents))
	if err != nil {
		f.t.Fatal(err)
	}
	st, err := f.h.reg.Update("p", a)
	if err != nil {
		f.t.Fatal(err)
	}
	return st.Mount
}

// terminal attaches an in-process client, as the TUI does, and returns its deliveries' counter and digests.
type inproc struct {
	n       atomic.Int64
	mu      sync.Mutex
	digests []string
}

func (f *fixture) terminal() *inproc {
	f.t.Helper()
	var a uiview.ClientAbilities
	if err := json.Unmarshal([]byte(testAbilities), &a); err != nil {
		f.t.Fatal(err)
	}
	in := &inproc{}
	if _, _, err := f.h.reg.Attach(a, func(d uiview.Delivery) bool {
		in.mu.Lock()
		in.digests = append(in.digests, d.Snapshot.Digest)
		in.mu.Unlock()
		in.n.Add(1)
		return true
	}); err != nil {
		f.t.Fatal(err)
	}
	return in
}

type client struct {
	t *testing.T
	c net.Conn
	r *bufio.Reader
}

func dial(t *testing.T, path string) *client {
	t.Helper()
	c, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return &client{t: t, c: c, r: bufio.NewReaderSize(c, 1<<20)}
}

func (c *client) send(line string) {
	c.t.Helper()
	if _, err := c.c.Write([]byte(line + "\n")); err != nil {
		c.t.Fatalf("send: %v", err)
	}
}

// next returns the next line the client is written, or nil when the connection ended.
func (c *client) next() map[string]any {
	c.t.Helper()
	_ = c.c.SetReadDeadline(time.Now().Add(20 * time.Second))
	line, err := c.r.ReadString('\n')
	if err != nil {
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			c.t.Fatal("the client was written nothing for 20 s")
		}
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(line), &m); err != nil {
		c.t.Fatalf("line %q: %v", line, err)
	}
	return m
}

// until returns the next line of the given type.
func (c *client) until(kind string) map[string]any {
	c.t.Helper()
	for {
		m := c.next()
		if m == nil {
			c.t.Fatalf("the connection ended before a %s line", kind)
		}
		if m["type"] == kind {
			return m
		}
	}
}

func (c *client) attach() string {
	c.t.Helper()
	c.send(`{"id":"attach","type":"ui_view_attach","abilities":` + testAbilities + `}`)
	r := c.until("response")
	if r["success"] != true {
		c.t.Fatalf("attach = %v", r)
	}
	return strconv.Itoa(int(r["data"].(map[string]any)["attachment"].(float64)))
}

func digestOf(event map[string]any) string {
	s, _ := event["snapshot"].(map[string]any)
	d, _ := s["digest"].(string)
	return d
}

func textOf(event map[string]any) string {
	raw, _ := json.Marshal(event["snapshot"])
	var s struct {
		View struct {
			Root struct {
				Text string `json:"text"`
			} `json:"root"`
		} `json:"view"`
	}
	_ = json.Unmarshal(raw, &s)
	return s.View.Root.Text
}

func intentLine(id, attachment string, ref uiview.Ref, intent string) string {
	return fmt.Sprintf(`{"id":%q,"type":"ui_view_intent","attachment":%s,"plugin":"p","mount":{"key":%q,"epoch":%d,"rev":%d},"intent":%q,"arg":{}}`, id, attachment, ref.Key, ref.Epoch, ref.Rev, intent)
}

// pig additive (D614): two clients of the socket and the terminal's in-process client hold one registry: each is sent
// the same content of a mount, and an intent one of them raises is answered and its update reaches all three.
func TestClientsOfTheSocketShareTheRegistryOfTheSession(t *testing.T) {
	f := newFixture(t)
	path := f.serve()
	if info, err := os.Lstat(path); err != nil || info.Mode().Type() != os.ModeSocket || info.Mode().Perm() != 0o600 {
		t.Fatalf("socket %s: %v %v, want a socket of mode 0600", path, info, err)
	}
	if info, err := os.Lstat(f.dir); err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
		t.Fatalf("directory %s: %v %v, want mode 0700", f.dir, info, err)
	}
	if want := filepath.Join(f.dir, strconv.Itoa(os.Getpid())+".sock"); path != want {
		t.Fatalf("path = %s, want %s", path, want)
	}
	term := f.terminal()
	a, b := dial(t, path), dial(t, path)
	a.attach()
	bn := b.attach()
	ref := f.mount("board", "one")
	ea, eb := a.until("ui_view"), b.until("ui_view")
	term.mu.Lock()
	first := term.digests[0]
	term.mu.Unlock()
	if digestOf(ea) == "" || digestOf(ea) != digestOf(eb) || digestOf(ea) != first {
		t.Fatalf("digests differ: socket clients %q %q, terminal %q", digestOf(ea), digestOf(eb), first)
	}
	b.send(intentLine("i1", bn, ref, "add"))
	r := b.until("response")
	if r["success"] != true || r["data"].(map[string]any)["outcome"] != "done" {
		t.Fatalf("intent = %v", r)
	}
	f.update(ref, "two")
	ea, eb = a.until("ui_view"), b.until("ui_view")
	if textOf(ea) != "two" || textOf(eb) != "two" || digestOf(ea) != digestOf(eb) || term.n.Load() != 2 {
		t.Fatalf("after the update: %q %q, digests %q %q, terminal deliveries %d", textOf(ea), textOf(eb), digestOf(ea), digestOf(eb), term.n.Load())
	}
}

// pig additive (D614): the endpoint serves the seven carrier commands only. Any other line, a command of RPC mode
// included, gets RPC mode's unknown-command response and changes nothing; the connection stays usable.
func TestOnlyCarrierCommandsAreServed(t *testing.T) {
	f := newFixture(t)
	c := dial(t, f.serve())
	c.send(`{"id":"1","type":"prompt","message":"hello"}`)
	if r := c.next(); r["success"] != false || r["command"] != "prompt" || r["error"] != "Unknown command: prompt" || r["id"] != "1" {
		t.Fatalf("prompt = %v", r)
	}
	c.send(`{"id":"2","type":"get_state"}`)
	if r := c.next(); r["error"] != "Unknown command: get_state" {
		t.Fatalf("get_state = %v", r)
	}
	c.send(`not json`)
	if r := c.next(); r["command"] != "parse" || r["success"] != false {
		t.Fatalf("parse = %v", r)
	}
	c.attach()
}

// pig additive (D614): a peer that is not this user is closed before a line of it is read. The modes of the directory
// and the socket are not the only guard.
func TestAPeerOfAnotherUserIsClosedBeforeALineIsRead(t *testing.T) {
	f := newFixture(t)
	f.e.uid = os.Geteuid() + 1
	// The directory and the socket are still made by this user, so the file checks compare with the real one.
	listenUID := os.Geteuid()
	listener, path, own, err := listen(f.dir, listenUID)
	if err != nil {
		t.Fatal(err)
	}
	s := &server{uid: f.e.uid, host: f.h, listener: listener, path: path, own: own, conns: map[*conn]struct{}{}, done: make(chan struct{})}
	s.work.Go(s.accept)
	defer s.close()
	c := dial(t, path)
	_, _ = c.c.Write([]byte(`{"id":"attach","type":"ui_view_attach","abilities":` + testAbilities + "}\n"))
	if got := c.next(); got != nil {
		t.Fatalf("a peer of another user was answered: %v", got)
	}
	if f.h.reg.Abilities() != nil {
		t.Fatal("a peer of another user attached")
	}
}

// pig additive (D614): the directory and a socket already at the path are used or removed only when they are this
// user's own, and never through a link.
func TestTheDirectoryAndTheSocketMustBeThisUsersOwn(t *testing.T) {
	name := strconv.Itoa(os.Getpid()) + ".sock"
	refused := func(t *testing.T, f *fixture, want string) {
		t.Helper()
		f.e.Bind(f.h)
		f.settle()
		if f.e.Path() != "" {
			t.Fatalf("a socket is served at %s", f.e.Path())
		}
		if n := f.notices(); len(n) != 1 || !strings.Contains(n[0], want) {
			t.Fatalf("notices = %q, want one with %q", n, want)
		}
	}
	t.Run("a link to a directory", func(t *testing.T) {
		f := newFixture(t)
		target := filepath.Join(filepath.Dir(f.dir), "elsewhere")
		if err := os.Mkdir(target, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, f.dir); err != nil {
			t.Fatal(err)
		}
		refused(t, f, "is not a directory of mode 0700")
		if entries, _ := os.ReadDir(target); len(entries) != 0 {
			t.Fatalf("the link's target holds %v", entries)
		}
	})
	t.Run("a directory others may enter", func(t *testing.T) {
		f := newFixture(t)
		if err := os.Mkdir(f.dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(f.dir, 0o755); err != nil {
			t.Fatal(err)
		}
		refused(t, f, "is not a directory of mode 0700")
	})
	t.Run("a file at the socket's path", func(t *testing.T) {
		f := newFixture(t)
		if err := os.Mkdir(f.dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(f.dir, name), []byte("mine"), 0o600); err != nil {
			t.Fatal(err)
		}
		refused(t, f, "is not a socket of mode 0600")
		if data, _ := os.ReadFile(filepath.Join(f.dir, name)); string(data) != "mine" {
			t.Fatalf("the file was touched: %q", data)
		}
	})
	t.Run("a link at the socket's path", func(t *testing.T) {
		f := newFixture(t)
		if err := os.Mkdir(f.dir, 0o700); err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(filepath.Dir(f.dir), "target")
		if err := os.Symlink(target, filepath.Join(f.dir, name)); err != nil {
			t.Fatal(err)
		}
		refused(t, f, "is not a socket of mode 0600")
		if _, err := os.Lstat(target); err == nil {
			t.Fatal("the link's target was created")
		}
	})
	stale := func(t *testing.T, path string, mode os.FileMode) {
		t.Helper()
		l, err := net.Listen("unix", path)
		if err != nil {
			t.Fatal(err)
		}
		l.(*net.UnixListener).SetUnlinkOnClose(false)
		_ = l.Close()
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
	}
	t.Run("stale sockets", func(t *testing.T) {
		f := newFixture(t)
		if err := os.Mkdir(f.dir, 0o700); err != nil {
			t.Fatal(err)
		}
		gone := exec.Command("true")
		if err := gone.Run(); err != nil {
			t.Skip("no true command")
		}
		dead := filepath.Join(f.dir, strconv.Itoa(gone.Process.Pid)+".sock")
		live := filepath.Join(f.dir, strconv.Itoa(os.Getppid())+".sock")
		note := filepath.Join(f.dir, "note.sock")
		stale(t, filepath.Join(f.dir, name), 0o600)
		stale(t, dead, 0o600)
		stale(t, live, 0o600)
		stale(t, note, 0o600)
		// A socket named for a process that ended, which somebody still serves: a process of another pid namespace.
		gone2 := exec.Command("true")
		if err := gone2.Run(); err != nil {
			t.Skip("no true command")
		}
		served := filepath.Join(f.dir, strconv.Itoa(gone2.Process.Pid)+".sock")
		other, err := net.Listen("unix", served)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = other.Close() }()
		if err := os.Chmod(served, 0o600); err != nil {
			t.Fatal(err)
		}
		path := f.serve()
		dial(t, path).attach()
		if _, err := os.Lstat(dead); err == nil {
			t.Error("the socket of a process that ended was kept")
		}
		for _, kept := range []string{live, note, served} {
			if _, err := os.Lstat(kept); err != nil {
				t.Errorf("%s was removed: it is not the socket of a process that ended, or somebody serves it", kept)
			}
		}
		if c, err := net.Dial("unix", served); err != nil {
			t.Errorf("the socket somebody serves no longer accepts: %v", err)
		} else {
			_ = c.Close()
		}
	})
	t.Run("a socket somebody serves at the socket's path", func(t *testing.T) {
		f := newFixture(t)
		if err := os.Mkdir(f.dir, 0o700); err != nil {
			t.Fatal(err)
		}
		other, err := net.Listen("unix", filepath.Join(f.dir, name))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = other.Close() }()
		if err := os.Chmod(filepath.Join(f.dir, name), 0o600); err != nil {
			t.Fatal(err)
		}
		refused(t, f, "is served by another process")
		if c, err := net.Dial("unix", filepath.Join(f.dir, name)); err != nil {
			t.Fatalf("the other process's socket no longer accepts: %v", err)
		} else {
			_ = c.Close()
		}
	})
}

// pig additive (D614): unbinding the host closes every connection and removes the socket; an intent in flight ends
// with its attachment; the next host is served under the same path; Close leaves no file, attachment or goroutine.
func TestUnbindClosesEveryConnectionAndCloseLeavesNothing(t *testing.T) {
	before := runtime.NumGoroutine()
	f := newFixture(t)
	path := f.serve()
	a, b := dial(t, path), dial(t, path)
	an := a.attach()
	b.attach()
	ref := f.mount("board", "one")
	a.until("ui_view")
	a.send(intentLine("w", an, ref, "wait"))
	<-f.p.started
	first := f.h
	f.e.Unbind()
	f.settle()
	// The connection with the intent in flight ends; next fails the test when it does not.
	for a.next() != nil {
	}
	for b.next() != nil {
	}
	if _, err := os.Lstat(path); err == nil {
		t.Fatal("the socket was not removed")
	}
	if first.reg.Abilities() != nil {
		t.Fatal("an attachment outlived the unbind")
	}
	if _, err := net.Dial("unix", path); err == nil {
		t.Fatal("a removed socket accepted a connection")
	}
	// The next host: the same path, another registry.
	f.h, f.p = newHost()
	if again := f.serve(); again != path {
		t.Fatalf("the next host is served at %s, want %s", again, path)
	}
	c := dial(t, path)
	c.attach()
	f.mount("board", "next")
	if e := c.until("ui_view"); textOf(e) != "next" {
		t.Fatalf("the next host's mount = %q", textOf(e))
	}
	f.e.Close()
	if c.next() != nil {
		t.Fatal("Close left a connection open")
	}
	if _, err := os.Lstat(path); err == nil {
		t.Fatal("Close left the socket")
	}
	if f.h.reg.Abilities() != nil {
		t.Fatal("Close left an attachment")
	}
	f.e.Bind(f.h)
	f.settle()
	if f.e.Path() != "" {
		t.Fatal("a closed endpoint served again")
	}
	_ = a.c.Close()
	_ = b.c.Close()
	_ = c.c.Close()
	deadline := time.Now().Add(10 * time.Second)
	for runtime.NumGoroutine() > before && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if n := runtime.NumGoroutine(); n > before {
		buf := make([]byte, 1<<16)
		t.Fatalf("%d goroutines, %d before the endpoint:\n%s", n, before, buf[:runtime.Stack(buf, true)])
	}
}

// pig additive (D614): an endpoint that is never bound makes no directory and starts no goroutine.
func TestAnUnboundEndpointTouchesNothing(t *testing.T) {
	before := runtime.NumGoroutine()
	dir := filepath.Join(shortDir(t), "view-endpoint")
	e := New(Options{Dir: dir})
	if n := runtime.NumGoroutine(); n != before {
		t.Fatalf("New started %d goroutines", n-before)
	}
	e.Unbind()
	e.Close()
	if _, err := os.Lstat(dir); err == nil {
		t.Fatal("an endpoint that was never bound made its directory")
	}
}

// pig additive (D614): a socket path that does not fit a socket address is refused before anything is bound, with one
// notice that names the path, its length and the limit.
func TestASocketPathLongerThanAnAddressHoldsIsRefused(t *testing.T) {
	parent := filepath.Join(shortDir(t), strings.Repeat("d", 120))
	if err := os.Mkdir(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	f := newFixtureAt(t, filepath.Join(parent, "view-endpoint"))
	f.e.Bind(f.h)
	f.settle()
	path := filepath.Join(f.dir, strconv.Itoa(os.Getpid())+".sock")
	limit := len(syscall.RawSockaddrUnix{}.Path) - 1
	want := fmt.Sprintf("View endpoint: the socket path %s is %d bytes, longer than the %d a socket address holds; no view endpoint is served", path, len(path), limit)
	if got := f.notices(); len(got) != 1 || got[0] != want {
		t.Fatalf("notices = %q, want %q", got, want)
	}
	if f.e.Path() != "" {
		t.Fatalf("a socket is served at %s", f.e.Path())
	}
	if _, err := os.Lstat(path); err == nil {
		t.Fatalf("%s was made", path)
	}
}

// pig additive (D614): a line longer than the carrier's bound of one command line closes the connection. The endpoint
// closes the socket and ends the attachment after it, so a client that sees the close may look before the attachment is
// gone; the test waits for the registry's own notice that the abilities changed, then checks them.
func TestALineLongerThanTheCarriersBoundClosesTheConnection(t *testing.T) {
	f := newFixture(t)
	c := dial(t, f.serve())
	c.attach()
	changed := f.abilitiesChanged()
	go func() {
		_, _ = c.c.Write([]byte(`{"type":"ui_view_report","pad":"` + strings.Repeat("x", maxLineBytes+(64<<10))))
	}()
	if got := c.next(); got != nil {
		t.Fatalf("an oversized line was answered: %v", got)
	}
	if !f.awaitDetached(changed) {
		t.Fatal("the attachment of the closed connection is still held")
	}
}

// pig additive (D614): the registry's bound of 8 attachments counts the terminal and the socket's clients together, so
// the ninth attach is the carrier's limit; and the endpoint serves 16 connections, the seventeenth is closed.
func TestTheNinthAttachmentIsRefusedAndTheSeventeenthConnectionClosed(t *testing.T) {
	f := newFixture(t)
	path := f.serve()
	f.terminal()
	clients := make([]*client, 0, maxConns)
	for range 7 {
		c := dial(t, path)
		c.attach()
		clients = append(clients, c)
	}
	ninth := dial(t, path)
	ninth.send(`{"id":"attach","type":"ui_view_attach","abilities":` + testAbilities + `}`)
	if r := ninth.until("response"); r["success"] != false || r["code"] != "limit" {
		t.Fatalf("the ninth attach = %v", r)
	}
	clients = append(clients, ninth)
	for len(clients) < maxConns {
		c := dial(t, path)
		c.send(`{"id":"x","type":"nothing"}`)
		if c.next() == nil {
			t.Fatalf("connection %d was closed", len(clients)+1)
		}
		clients = append(clients, c)
	}
	over := dial(t, path)
	_, _ = over.c.Write([]byte(`{"id":"x","type":"nothing"}` + "\n"))
	if got := over.next(); got != nil {
		t.Fatalf("the seventeenth connection was answered: %v", got)
	}
}

// pig additive (D614): a client that stops reading holds nobody. The registry keeps accepting updates and the
// terminal's client is delivered every one of them while the stalled client reads nothing; the stalled client is owed
// the carrier's bounded queue and then one gap, and resyncs when it reads again.
func TestAClientThatDoesNotReadHoldsNoOtherClient(t *testing.T) {
	f := newFixture(t)
	path := f.serve()
	term := f.terminal()
	stalled := dial(t, path)
	n := stalled.attach()
	ref := f.mount("board", "0")
	const updates = 2000
	pad := strings.Repeat("x", 16<<10)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 1; i <= updates; i++ {
			ref = f.update(ref, strconv.Itoa(i)+pad)
		}
	}()
	select {
	case <-done:
	case <-time.After(60 * time.Second):
		t.Fatalf("the registry waited on a client that does not read: %d of %d deliveries reached the terminal", term.n.Load(), updates+1)
	}
	if got := term.n.Load(); got != updates+1 {
		t.Fatalf("the terminal was delivered %d of %d snapshots", got, updates+1)
	}
	// The stalled client reads again: events in order, then the gap, and nothing after it until it resyncs.
	seq := 0.0
	var gap map[string]any
	for gap == nil {
		m := stalled.next()
		if m == nil {
			t.Fatal("the stalled client was closed, not sent a gap")
		}
		switch m["type"] {
		case "ui_view":
			if m["seq"].(float64) != seq+1 {
				t.Fatalf("event %v after %v", m["seq"], seq)
			}
			seq++
		case "ui_view_gap":
			gap = m
		}
	}
	if seq >= updates+1 || gap["seq"].(float64) != seq+1 {
		t.Fatalf("gap %v after %v events of %d", gap["seq"], seq, updates+1)
	}
	stalled.send(`{"id":"r","type":"ui_view_resync","attachment":` + n + `}`)
	r := stalled.until("response")
	snapshots := r["data"].(map[string]any)["snapshots"].([]any)
	if r["success"] != true || len(snapshots) != 1 {
		t.Fatalf("resync = %v", r["success"])
	}
	if text := textOf(map[string]any{"snapshot": snapshots[0]}); !strings.HasPrefix(text, strconv.Itoa(updates)+"x") {
		t.Fatalf("the resync carries %q", text[:8])
	}
}

// unreadIntents is the "big" intents the tests of replies nobody reads raise: 1200 answers of 32768 bytes are 39 MiB,
// past the endpoint's bound of unwritten replies and any socket buffer.
const unreadIntents = 1200

// pipeClient serves one end of an in-memory pipe as a connection of the endpoint and returns the client at the other.
// A pipe is synchronous, so the connection's writer waits in its first Write until the client reads: a client that
// reads nothing is written nothing, whatever the scheduler does. The wiring is admit's, without its check of the peer's
// user, which a pipe has no kernel to answer.
func (f *fixture) pipeClient() *client {
	f.t.Helper()
	f.serve()
	s := f.e.srv
	serverEnd, clientEnd := net.Pipe()
	f.t.Cleanup(func() { _ = clientEnd.Close() })
	k := &conn{s: s, c: serverEnd}
	k.wake = sync.NewCond(&k.mu)
	s.mu.Lock()
	s.conns[k] = struct{}{}
	s.work.Go(k.write)
	s.work.Go(k.read)
	s.mu.Unlock()
	return &client{t: f.t, c: clientEnd, r: bufio.NewReaderSize(clientEnd, 1<<20)}
}

// unreadAnswers mounts a view for c, which is attached as n, raises unreadIntents "big" intents, reads none of their
// answers while they settle, waits for the endpoint to cut the connection at its bound of unwritten replies, and
// returns how many lines the client is written after the cut.
func (f *fixture) unreadAnswers(c *client, n string) (written int) {
	f.t.Helper()
	ref := f.mount("board", "one")
	c.until("ui_view")
	changed := f.abilitiesChanged()
	for i := range unreadIntents {
		c.send(intentLine("b"+strconv.Itoa(i), n, ref, "big"))
	}
	for range unreadIntents {
		<-f.p.started
	}
	close(f.p.gate)
	if !f.awaitDetached(changed) {
		f.t.Fatal("the client that read no answer still holds its attachment")
	}
	for c.next() != nil {
		written++
	}
	return written
}

// pig additive (D614): replies nobody reads are bounded too. A client that raises intents and reads none of their
// answers is cut when 8388608 bytes of them are unwritten; the endpoint keeps serving others. The endpoint promises the
// cut, and no answer before it: how many reach the client first is the socket's buffer and the writer's turn, so the
// test asks only that the client is not written all of them.
func TestRepliesNobodyReadsCutTheConnection(t *testing.T) {
	f := newFixture(t)
	path := f.serve()
	c := dial(t, path)
	if written := f.unreadAnswers(c, c.attach()); written >= unreadIntents {
		t.Fatalf("the client was written %d answers of %d: the bound did not cut the connection", written, unreadIntents)
	}
	dial(t, path).attach()
}

// pig additive (D614): the cut does not wait for the writer. With the writer held by a client that reads nothing, the
// bound still cuts the connection and the answers it holds are dropped: the client is written none of them.
func TestRepliesNobodyReadsCutTheConnectionWhileTheWriterIsHeld(t *testing.T) {
	f := newFixture(t)
	c := f.pipeClient()
	if written := f.unreadAnswers(c, c.attach()); written != 0 {
		t.Fatalf("the client was written %d answers with the writer held", written)
	}
}

// pig additive (D614): a client that connects while the host is being unbound is closed with it. No client is left
// attached to a host the endpoint no longer serves, however the connects and the unbinds interleave.
func TestAClientThatConnectsWhileTheHostIsUnboundIsNeverLeftAttached(t *testing.T) {
	f := newFixture(t)
	path := filepath.Join(f.dir, strconv.Itoa(os.Getpid())+".sock")
	stop := make(chan struct{})
	var clients sync.WaitGroup
	var attached atomic.Int64
	for range 4 {
		clients.Go(func() {
			for {
				select {
				case <-stop:
					return
				default:
				}
				c, err := net.Dial("unix", path)
				if err != nil {
					continue
				}
				_, _ = c.Write([]byte(`{"id":"attach","type":"ui_view_attach","abilities":` + testAbilities + "}\n"))
				_ = c.SetReadDeadline(time.Now().Add(20 * time.Second))
				r := bufio.NewReader(c)
				if line, err := r.ReadString('\n'); err == nil && strings.Contains(line, `"success":true`) {
					attached.Add(1)
					// An attached client stays until the endpoint closes it.
					for err == nil {
						_, err = r.ReadString('\n')
					}
				}
				_ = c.Close()
			}
		})
	}
	var hosts []*testHost
	for range 50 {
		h, _ := newHost()
		hosts = append(hosts, h)
		f.e.Bind(h)
		f.e.Unbind()
	}
	f.settle()
	for attached.Load() == 0 {
		h, _ := newHost()
		hosts = append(hosts, h)
		f.e.Bind(h)
		f.settle()
		time.Sleep(5 * time.Millisecond)
		f.e.Unbind()
		f.settle()
	}
	close(stop)
	clients.Wait()
	for i, h := range hosts {
		if h.reg.Abilities() != nil {
			t.Fatalf("host %d of %d still holds an attachment after it was unbound", i, len(hosts))
		}
	}
}

// pig additive (D614): Close waits for no client. With a client that attached and reads nothing, its queue full and
// its writer blocked, Close returns within 5 seconds, the socket is gone and the connection is closed.
func TestCloseDoesNotWaitForAClientThatDoesNotRead(t *testing.T) {
	f := newFixture(t)
	path := f.serve()
	stalled := dial(t, path)
	stalled.attach()
	ref := f.mount("board", "0")
	pad := strings.Repeat("x", 16<<10)
	for i := 1; i <= 600; i++ {
		ref = f.update(ref, strconv.Itoa(i)+pad)
	}
	closed := make(chan struct{})
	go func() {
		f.e.Close()
		close(closed)
	}()
	bound := time.NewTimer(5 * time.Second)
	defer bound.Stop()
	select {
	case <-closed:
	case <-bound.C:
		// A Close that waits for the client ends once the client is gone, so this row fails by its own line.
		_ = stalled.c.Close()
		<-closed
		t.Fatal("Close did not return within 5 s with a client that does not read")
	}
	if _, err := os.Lstat(path); err == nil {
		t.Fatal("Close left the socket")
	}
	if f.h.reg.Abilities() != nil {
		t.Fatal("Close left the attachment of the client that does not read")
	}
	for stalled.next() != nil {
	}
}

// pig additive (D614): repeated binds whose listener fails emit the failure notice once after the worker settles,
// matching the unsupported-system notice contract tested by TestNoSocketIsServedOnThisSystem.
func TestFailedBindNotifiesOnceAfterSettle(t *testing.T) {
	parent := filepath.Join(shortDir(t), strings.Repeat("d", 120))
	if err := os.Mkdir(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	f := newFixtureAt(t, filepath.Join(parent, "view-endpoint"))
	for range 3 {
		f.e.Bind(f.h)
		f.e.Unbind()
	}
	f.settle()
	f.e.Close()
	if got := f.notices(); len(got) != 1 || !strings.Contains(got[0], "no view endpoint is served") {
		t.Fatalf("notices = %q, want one failed-bind notice", got)
	}
}
