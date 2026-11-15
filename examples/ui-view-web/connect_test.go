//go:build linux || darwin

package uiviewweb

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/uiview"
	"github.com/MichaelKinsy/PiG/internal/browsertest"
	"github.com/MichaelKinsy/PiG/internal/testenv"
	"github.com/MichaelKinsy/PiG/internal/viewendpoint"
)

// The bridge's --connect mode: every page is a client of the view endpoint of one running interactive session. These
// tests drive it against a session of their own: internal/viewendpoint serving a registry, as an interactive pig does,
// or a bare socket that counts its connections and records what it is written.

const connectAbilities = `{"slots":["panel","widget-above"],"nodes":["vstack","text","select-list"],"markdown":false,"imageFormats":[],"limits":{"nodes":256,"depth":8,"snapshotBytes":1048576,"textBytes":1048576,"mounts":8,"intentArgBytes":1024},"input":{"keyboard":true,"pointer":true,"text":true}}`

const connectAttach = `{"id":"a","type":"ui_view_attach","abilities":` + connectAbilities + `}`

// The fake session's plugin.
const boardPlugin = "plan"

// board is the content of a plan board: a title and a list whose items raise board.pick.
func board(title string) string {
	return `"view":{"root":{"kind":"vstack","children":[{"kind":"text","text":` + strconv.Quote(title) + `},{"kind":"select-list","id":"pick","items":[{"value":"alpha","label":"Alpha item"},{"value":"beta","label":"Beta item"}]}]}},` +
		`"intents":[{"name":"board.pick","nodeKey":"pick","arg":{"type":"object","properties":{"value":{"type":"string"}},"required":["value"]}}]`
}

// note is the content of a mount that is one text.
func note(text string) string {
	return `"view":{"root":{"kind":"text","text":` + strconv.Quote(text) + `}}`
}

// host is one extension host of the fake session: a registry, the session's terminal attached to it, and the plugin
// that answers board.pick by redrawing the board with the picked value.
type host struct {
	t   *testing.T
	reg *uiview.Registry
}

func abilitiesOf(t *testing.T, raw string) uiview.ClientAbilities {
	t.Helper()
	var a uiview.ClientAbilities
	if err := json.Unmarshal([]byte(raw), &a); err != nil {
		t.Fatal(err)
	}
	return a
}

func newHost(t *testing.T) *host {
	t.Helper()
	h := &host{t: t, reg: uiview.New(uiview.Options{})}
	// The terminal: a registry takes no mount before a client is attached.
	if _, _, err := h.reg.Attach(abilitiesOf(t, connectAbilities), nil); err != nil {
		t.Fatalf("attach the terminal: %v", err)
	}
	return h
}

func (h *host) Views() *uiview.Registry { return h.reg }

func (h *host) InvokeViewIntentFrom(ctx context.Context, t *uiview.Attachment, extension string, ref uiview.Ref, nodeKey, intent string, arg json.RawMessage) uiview.Result {
	return t.Invoke(ctx, extension, ref, nodeKey, intent, arg, h)
}

// Invoke is the plugin's handler. Its update belongs to its answer, so the intent settles done and every client is
// sent the new board before the reply.
func (h *host) Invoke(_ context.Context, inv uiview.Invocation) (uiview.Result, error) {
	failed := func(err error) (uiview.Result, error) {
		return uiview.Result{Outcome: uiview.OutcomeFailed, Error: err.Error()}, nil
	}
	var arg struct {
		Value string `json:"value"`
	}
	if err := json.Unmarshal(inv.Arg, &arg); err != nil {
		return failed(err)
	}
	a, err := uiview.DecodeUpdate(fmt.Appendf(nil, `{"mount":{"key":%q,"epoch":%d,"rev":%d},%s}`, inv.Mount.Key, inv.Mount.Epoch, inv.Rev, board("picked "+arg.Value)))
	if err != nil {
		return failed(err)
	}
	a.Answering = inv.Ticket
	if _, err := h.reg.Update(boardPlugin, a); err != nil {
		return failed(err)
	}
	return uiview.Result{Outcome: uiview.OutcomeDone}, nil
}

// put mounts content under key in slot.
func (h *host) put(key, slot, content string) uiview.Ref {
	h.t.Helper()
	a, err := uiview.DecodeMount([]byte(`{"key":` + strconv.Quote(key) + `,"slot":` + strconv.Quote(slot) + `,` + content + `}`))
	if err != nil {
		h.t.Fatalf("mount %s: %v", key, err)
	}
	st, err := h.reg.Mount(boardPlugin, a)
	if err != nil {
		h.t.Fatalf("mount %s: %v", key, err)
	}
	return st.Mount
}

// replace updates the mount at ref with content.
func (h *host) replace(ref uiview.Ref, content string) uiview.Ref {
	h.t.Helper()
	a, err := uiview.DecodeUpdate(fmt.Appendf(nil, `{"mount":{"key":%q,"epoch":%d,"rev":%d},%s}`, ref.Key, ref.Epoch, ref.Rev, content))
	if err != nil {
		h.t.Fatalf("update %s: %v", ref.Key, err)
	}
	st, err := h.reg.Update(boardPlugin, a)
	if err != nil {
		h.t.Fatalf("update %s: %v", ref.Key, err)
	}
	return st.Mount
}

// drawn returns, by key, the digest of every mount as the registry resolves it for a client with these abilities. A
// mount that ended (the registry keeps the broken ones) is none.
func (h *host) drawn(abilities string) map[string]string {
	h.t.Helper()
	t, state, err := h.reg.Attach(abilitiesOf(h.t, abilities), nil)
	if err != nil {
		h.t.Fatalf("attach: %v", err)
	}
	defer t.Detach()
	digests := map[string]string{}
	for _, s := range state.Snapshots {
		if s.State != uiview.StateBroken && s.State != uiview.StateFinished && s.State != uiview.StateRemoved {
			digests[s.Mount.Key] = s.Digest
		}
	}
	return digests
}

// ownerGone breaks the plugin's mounts, as the end of its extension's process does: the registry and its clients stay.
func (h *host) ownerGone() { h.reg.Break(boardPlugin, uiview.ReasonOwnerGone) }

// held returns how many clients the registry holds, the terminal among them. A registry takes 8.
func (h *host) held() int {
	h.t.Helper()
	var probes []*uiview.Attachment
	for {
		t, _, err := h.reg.Attach(abilitiesOf(h.t, connectAbilities), nil)
		if err != nil {
			break
		}
		probes = append(probes, t)
	}
	for _, t := range probes {
		t.Detach()
	}
	return 8 - len(probes)
}

// shortDir returns a directory whose path leaves room for a socket's name: a socket address holds 104 bytes on macOS.
func shortDir(t *testing.T) string {
	t.Helper()
	return testenv.ShortTempDir(t, "vw")
}

// eventually waits until cond holds.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("%s: not within 30 s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// fakeSession is the view endpoint of a session that this test plays: dir is its view-endpoint directory.
type fakeSession struct {
	t   *testing.T
	dir string
	e   *viewendpoint.Endpoint
}

func newFakeSession(t *testing.T) *fakeSession {
	t.Helper()
	s := &fakeSession{t: t, dir: filepath.Join(shortDir(t), "view-endpoint")}
	s.e = viewendpoint.New(viewendpoint.Options{Dir: s.dir})
	t.Cleanup(s.e.Close)
	return s
}

// serve binds h, as the start of a session and of each session that takes its place does, and returns once its socket
// is served.
func (s *fakeSession) serve(h *host) {
	s.t.Helper()
	s.e.Bind(h)
	eventually(s.t, "the endpoint's socket", func() bool { return s.e.Path() != "" })
}

// end unbinds the host, as a session replacement and the exit do: every connection is closed and the socket is removed.
func (s *fakeSession) end() {
	s.t.Helper()
	path := s.e.Path()
	s.e.Unbind()
	eventually(s.t, "the removal of the endpoint's socket", func() bool {
		_, err := os.Lstat(path)
		return s.e.Path() == "" && err != nil
	})
}

// socket is a bare socket in place of a session's: it counts the connections it is given and records what they are
// written. serve, when set, is given each connection instead.
type socket struct {
	path string
	l    net.Listener

	mu       sync.Mutex
	accepted int
	got      bytes.Buffer
	conns    []net.Conn // held until the test ends, so that none is closed by being forgotten
}

func listenAt(t *testing.T, path string, serve func(c net.Conn)) *socket {
	t.Helper()
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	s := &socket{path: path, l: l}
	t.Cleanup(func() {
		_ = l.Close()
		s.mu.Lock()
		defer s.mu.Unlock()
		for _, c := range s.conns {
			_ = c.Close()
		}
	})
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			s.mu.Lock()
			s.accepted++
			s.conns = append(s.conns, c)
			s.mu.Unlock()
			if serve != nil {
				go serve(c)
				continue
			}
			go func() {
				buf := make([]byte, 1<<16)
				for {
					n, err := c.Read(buf)
					s.mu.Lock()
					s.got.Write(buf[:n])
					s.mu.Unlock()
					if err != nil {
						return
					}
				}
			}()
		}
	}()
	return s
}

// deadSocket leaves a socket that nothing listens on, as a pig that ended without removing its socket does.
func deadSocket(t *testing.T, path string) {
	t.Helper()
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	l.(*net.UnixListener).SetUnlinkOnClose(false)
	_ = l.Close()
}

func (s *socket) connections() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.accepted
}

func (s *socket) written() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.got.String()
}

// connectBridge runs bridge.mjs --connect target.
func connectBridge(t *testing.T, target string, args ...string) bridge {
	t.Helper()
	return runBridge(t, nodeBinary(t), nil, append([]string{"--connect", target}, args...)...)
}

// bridgeState is what GET /state reports.
type bridgeState struct {
	Sessions  int      `json:"sessions"`
	Pages     int      `json:"pages"`
	Resources []string `json:"resources"`
}

// timers and connections count what Node itself holds: a retry timer is a Timeout, a connection to a session's socket
// a PipeWrap.
func (s bridgeState) timers() int      { return count(s.Resources, "Timeout") }
func (s bridgeState) connections() int { return count(s.Resources, "PipeWrap") }

func count(list []string, item string) int {
	n := 0
	for _, x := range list {
		if x == item {
			n++
		}
	}
	return n
}

func (b bridge) state(t *testing.T) bridgeState {
	t.Helper()
	status, body := get(t, b.base+"/state", "Authorization", b.auth(t))
	var s bridgeState
	if err := json.Unmarshal([]byte(body), &s); status != 200 || err != nil {
		t.Fatalf("GET /state = %d %s: %v", status, body, err)
	}
	return s
}

type sse struct{ event, data string }

// stream is a page's event stream as the browser reads it: every event, in order.
type stream struct {
	t       *testing.T
	b       bridge
	events  chan sse
	close   context.CancelFunc
	session string         // the data of the last `session` event read
	seen    map[string]int // the events read so far, by name
	// attachment is the number the last attach was given.
	attachment float64
}

// follow opens a page's stream.
func (b bridge) follow(t *testing.T) *stream {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, b.base+"/events", nil)
	req.Header.Set("Authorization", b.auth(t))
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = res.Body.Close() })
	if res.StatusCode != http.StatusOK || res.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("GET /events = %s %s", res.Status, res.Header.Get("Content-Type"))
	}
	s := &stream{t: t, b: b, events: make(chan sse, 4096), close: cancel, seen: map[string]int{}}
	go func() {
		defer close(s.events)
		defer func() { _ = res.Body.Close() }()
		reader := bufio.NewReaderSize(res.Body, 1<<16)
		event, data, has := "message", "", false
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimSuffix(line, "\n")
			switch {
			case line == "":
				if has {
					s.events <- sse{event, data}
				}
				event, data, has = "message", "", false
			case strings.HasPrefix(line, "event: "):
				event = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				data, has = data+strings.TrimPrefix(line, "data: "), true
			}
		}
	}()
	return s
}

// next reads events until match reports true and returns that one.
func (s *stream) next(what string, match func(e sse) bool) sse {
	s.t.Helper()
	timer := time.NewTimer(60 * time.Second)
	defer timer.Stop()
	for {
		select {
		case e, ok := <-s.events:
			if !ok {
				s.t.Fatalf("the stream ended before %s", what)
			}
			s.seen[e.event]++
			if e.event == "session" {
				s.session = e.data
			}
			if match(e) {
				return e
			}
		case <-timer.C:
			s.t.Fatalf("%s: not within 60 s", what)
		}
	}
}

// event reads up to the next event of that name and returns its data.
func (s *stream) event(name string) string {
	s.t.Helper()
	return s.next("the "+name+" event", func(e sse) bool { return e.event == name }).data
}

type ending struct {
	Retry  bool   `json:"retry"`
	Reason string `json:"reason"`
}

// end reads up to the next `end` event.
func (s *stream) end() ending {
	s.t.Helper()
	var e ending
	if err := json.Unmarshal([]byte(s.event("end")), &e); err != nil {
		s.t.Fatal(err)
	}
	return e
}

// waiting reads up to the next `endpoint` event that says why the page waits, and returns the reason.
func (s *stream) waiting() string {
	s.t.Helper()
	var info struct{ State, Reason string }
	s.next("an endpoint event that says the page waits", func(e sse) bool {
		return e.event == "endpoint" && json.Unmarshal([]byte(e.data), &info) == nil && info.State == "waiting"
	})
	return info.Reason
}

// over reads to the end of the stream.
func (s *stream) over() {
	s.t.Helper()
	timer := time.NewTimer(60 * time.Second)
	defer timer.Stop()
	for {
		select {
		case _, ok := <-s.events:
			if !ok {
				return
			}
		case <-timer.C:
			s.t.Fatal("the stream did not end within 60 s")
		}
	}
}

// line reads up to the next relayed line that match accepts. A gap fails the test: no page here reads slowly enough
// for one.
func (s *stream) line(what string, match func(record map[string]any) bool) map[string]any {
	s.t.Helper()
	var found map[string]any
	s.next(what, func(e sse) bool {
		if e.event != "message" {
			return false
		}
		var record map[string]any
		if err := json.Unmarshal([]byte(e.data), &record); err != nil {
			s.t.Fatalf("relayed line %.200s: %v", e.data, err)
		}
		if record["type"] == "ui_view_gap" {
			s.t.Fatalf("the page was sent a gap before %s", what)
		}
		found = record
		return match(record)
	})
	return found
}

func (s *stream) post(line string) int {
	s.t.Helper()
	req, _ := http.NewRequest(http.MethodPost, s.b.base+"/send?session="+s.session, strings.NewReader(line))
	req.Header.Set("Content-Type", "text/plain")
	req.Header.Set("Authorization", s.b.auth(s.t))
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	_ = res.Body.Close()
	return res.StatusCode
}

// attach reads up to the stream's next session, attaches as a page does and returns the reply's snapshots.
func (s *stream) attach() []any {
	s.t.Helper()
	s.event("session")
	if status := s.post(connectAttach); status != http.StatusNoContent {
		s.t.Fatalf("POST ui_view_attach = %d", status)
	}
	reply := s.line("the attach reply", func(record map[string]any) bool { return record["type"] == "response" && record["id"] == "a" })
	data, _ := reply["data"].(map[string]any)
	if reply["success"] != true || reply["command"] != "ui_view_attach" || data == nil {
		s.t.Fatalf("attach = %v", reply)
	}
	s.attachment, _ = data["attachment"].(float64)
	snapshots, _ := data["snapshots"].([]any)
	return snapshots
}

// titleOf returns the text of a snapshot's first text node and its digest.
func titleOf(snapshot any) (title, digest string) {
	raw, _ := json.Marshal(snapshot)
	var s struct {
		Digest string `json:"digest"`
		View   struct {
			Root struct {
				Text     string `json:"text"`
				Children []struct {
					Text string `json:"text"`
				} `json:"children"`
			} `json:"root"`
		} `json:"view"`
	}
	_ = json.Unmarshal(raw, &s)
	if len(s.View.Root.Children) > 0 {
		return s.View.Root.Children[0].Text, s.Digest
	}
	return s.View.Root.Text, s.Digest
}

// request sends method to url the way no page of the bridge does, and returns the status.
func request(t *testing.T, method, url, host, origin string, header ...string) int {
	t.Helper()
	req, _ := http.NewRequest(method, url, strings.NewReader(connectAttach))
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	if host != "" {
		req.Host = host
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	return res.StatusCode
}

// TestConnectOpensNoConnectionForARefusedRequest: the secret and the origin rule come before the session's socket. A
// request without the secret, with a wrong one, with the secret in its address, from another origin or for another host
// is refused and opens no connection; the page's own request opens exactly one. And the bridge listens on 127.0.0.1 only.
func TestConnectOpensNoConnectionForARefusedRequest(t *testing.T) {
	session := listenAt(t, filepath.Join(shortDir(t), "1.sock"), nil)
	b := connectBridge(t, session.path)
	good := b.auth(t)
	refused := []struct {
		what, method, url, host, origin, authorization string
		want                                           int
	}{
		{"the stream without the secret", http.MethodGet, "/events", "", "", "", 403},
		{"the stream with a wrong secret", http.MethodGet, "/events", "", "", "Bearer " + strings.Repeat("0", 64), 403},
		{"the stream with the secret in its address", http.MethodGet, "/events?token=" + strings.TrimPrefix(good, "Bearer "), "", "", "", 403},
		{"the stream from another origin", http.MethodGet, "/events", "", "http://example.com", good, 403},
		{"the stream for another host", http.MethodGet, "/events", "example.com", "", good, 403},
		{"a line without the secret", http.MethodPost, "/send?session=x", "", "", "", 403},
		{"a line from another origin", http.MethodPost, "/send?session=x", "", "http://example.com", good, 403},
		{"a line for no session", http.MethodPost, "/send?session=x", "", "", good, 404},
		{"the state without the secret", http.MethodGet, "/state", "", "", "", 403},
	}
	for _, r := range refused {
		var header []string
		if r.authorization != "" {
			header = []string{"Authorization", r.authorization}
		}
		if status := request(t, r.method, b.base+r.url, r.host, r.origin, header...); status != r.want {
			t.Errorf("%s = %d, want %d", r.what, status, r.want)
		}
	}
	// The page's own stream connects. The socket takes connections in the order they were made, so one that a refused
	// request had opened would be counted by now.
	page := b.follow(t)
	page.event("session")
	eventually(t, "the page's connection", func() bool { return session.connections() > 0 })
	if n := session.connections(); n != 1 {
		t.Fatalf("the session's socket was given %d connections, want the page's one: a refused request opened one", n)
	}

	_, port, _ := net.SplitHostPort(strings.TrimPrefix(b.base, "http://"))
	others := []string{"::1"}
	if runtime.GOOS == "linux" {
		others = append(others, "127.0.0.2") // every address of 127.0.0.0/8 is this machine's on Linux
	}
	addresses, _ := net.InterfaceAddrs()
	for _, a := range addresses {
		if ip, ok := a.(*net.IPNet); ok && !ip.IP.IsLoopback() && !ip.IP.IsLinkLocalUnicast() {
			others = append(others, ip.IP.String())
		}
	}
	for _, address := range others {
		if c, err := net.DialTimeout("tcp", net.JoinHostPort(address, port), 5*time.Second); err == nil {
			_ = c.Close()
			t.Errorf("the bridge takes a connection on %s, want 127.0.0.1 only", address)
		}
	}
}

// TestConnectPicksTheOneSocketOfADirectory: given a directory, the bridge connects to the one socket in it, waits
// while there is none and tells the page why, gives up after --wait, and refuses to choose among several.
func TestConnectPicksTheOneSocketOfADirectory(t *testing.T) {
	t.Run("none, then one", func(t *testing.T) {
		s := newFakeSession(t)
		b := connectBridge(t, s.dir)
		page := b.follow(t)
		if reason := page.waiting(); !strings.Contains(reason, s.dir+" does not exist") {
			t.Fatalf("with no directory the page is told %q", reason)
		}
		if err := os.Mkdir(s.dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if reason := page.waiting(); !strings.Contains(reason, "no socket in "+s.dir) {
			t.Fatalf("with an empty directory the page is told %q", reason)
		}
		h := newHost(t)
		h.put("board", "panel", board("plan one"))
		s.serve(h)
		snapshots := page.attach()
		if len(snapshots) != 1 {
			t.Fatalf("attach sent %d snapshots, want the board", len(snapshots))
		}
		if title, _ := titleOf(snapshots[0]); title != "plan one" {
			t.Fatalf("the board is %q", title)
		}
	})
	t.Run("none within the wait", func(t *testing.T) {
		dir := shortDir(t)
		b := connectBridge(t, dir, "--wait", "0.3")
		base := b.state(t)
		page := b.follow(t)
		if reason := page.waiting(); !strings.Contains(reason, "no socket in "+dir) {
			t.Fatalf("the page is told %q", reason)
		}
		end := page.end()
		if end.Retry || !strings.Contains(end.Reason, "no socket in "+dir) || !strings.Contains(end.Reason, "waited 0.3 s") {
			t.Fatalf("the wait ended with %+v", end)
		}
		page.over()
		eventually(t, "the bridge to hold nothing for the page", func() bool {
			now := b.state(t)
			return now.Pages == 0 && now.timers() == base.timers() && now.connections() == base.connections()
		})
	})
	t.Run("several", func(t *testing.T) {
		dir := shortDir(t)
		one, two := listenAt(t, filepath.Join(dir, "1.sock"), nil), listenAt(t, filepath.Join(dir, "2.sock"), nil)
		b := connectBridge(t, dir)
		page := b.follow(t)
		end := page.end()
		if end.Retry || !strings.Contains(end.Reason, "2 sessions") || !strings.Contains(end.Reason, one.path+", "+two.path) || !strings.Contains(end.Reason, "--connect <socket>") {
			t.Fatalf("with two sockets the page is told %+v", end)
		}
		page.over()
		if page.seen["session"] != 0 || one.written() != "" || two.written() != "" {
			t.Fatalf("among two sockets the page was given %d sessions; the sockets were written %q and %q", page.seen["session"], one.written(), two.written())
		}
		// With one of them gone the next page is connected to the other.
		_ = two.l.Close()
		_ = os.Remove(two.path)
		b.follow(t).event("session")
	})
	t.Run("a dead socket beside the session's", func(t *testing.T) {
		s := newFakeSession(t)
		h := newHost(t)
		h.put("board", "panel", board("plan one"))
		s.serve(h)
		dead := filepath.Join(s.dir, "dead.sock")
		deadSocket(t, dead)
		b := connectBridge(t, s.dir)
		snapshots := b.follow(t).attach()
		if len(snapshots) != 1 {
			t.Fatalf("attach sent %d snapshots, want the board", len(snapshots))
		}
		if title, _ := titleOf(snapshots[0]); title != "plan one" {
			t.Fatalf("the board is %q", title)
		}
		if info, err := os.Lstat(dead); err != nil || info.Mode().Type() != os.ModeSocket {
			t.Fatalf("the socket nothing listens on is not the bridge's to touch: %v", err)
		}
	})
	t.Run("a dead socket only", func(t *testing.T) {
		dir := shortDir(t)
		dead := filepath.Join(dir, "7.sock")
		deadSocket(t, dead)
		b := connectBridge(t, dir, "--wait", "0.3")
		page := b.follow(t)
		if reason := page.waiting(); reason != "no running session: nothing listens on "+dead {
			t.Fatalf("the page is told %q", reason)
		}
		if end := page.end(); end.Retry || !strings.Contains(end.Reason, "no running session: nothing listens on "+dead) || !strings.Contains(end.Reason, "waited 0.3 s") {
			t.Fatalf("the wait ended with %+v", end)
		}
		page.over()
		if info, err := os.Lstat(dead); err != nil || info.Mode().Type() != os.ModeSocket {
			t.Fatalf("the socket nothing listens on is not the bridge's to touch: %v", err)
		}
	})
}

// TestConnectKeepsTheConnectionWhenAMountBreaks: a mount whose extension ended leaves the registry in place, so the
// session closes nothing. The page is sent the board as broken and then the board that is mounted again, on the
// connection and under the attachment it has; it is sent no `end` and no second session.
func TestConnectKeepsTheConnectionWhenAMountBreaks(t *testing.T) {
	s := newFakeSession(t)
	h := newHost(t)
	h.put("board", "panel", board("plan one"))
	s.serve(h)
	b := connectBridge(t, s.dir)
	page := b.follow(t)
	if snapshots := page.attach(); len(snapshots) != 1 {
		t.Fatalf("attach sent %d snapshots, want the board", len(snapshots))
	}
	view := func(what string) (event, snapshot, mount map[string]any) {
		t.Helper()
		event = page.line(what, func(record map[string]any) bool { return record["type"] == "ui_view" })
		snapshot, _ = event["snapshot"].(map[string]any)
		mount, _ = snapshot["mount"].(map[string]any)
		return event, snapshot, mount
	}
	h.ownerGone()
	event, snapshot, mount := view("the broken board")
	if event["seq"] != 1.0 || snapshot["state"] != "broken" || mount["key"] != "board" || mount["epoch"] != 1.0 {
		t.Fatalf("after the extension ended the page is sent %v", event)
	}
	h.put("board", "panel", board("plan two"))
	event, snapshot, mount = view("the board mounted again")
	title, digest := titleOf(snapshot)
	if want := h.drawn(connectAbilities)["board"]; event["seq"] != 2.0 || snapshot["state"] != "live" || mount["epoch"] != 2.0 || title != "plan two" || digest == "" || digest != want {
		t.Fatalf("the board mounted again is sent as %v, the host has it with digest %q", event, want)
	}
	// The attachment is the one the page has: a resync under it answers with that board and nothing else.
	if status := page.post(fmt.Sprintf(`{"id":"r","type":"ui_view_resync","attachment":%v}`, page.attachment)); status != http.StatusNoContent {
		t.Fatalf("POST ui_view_resync = %d", status)
	}
	reply := page.line("the resync reply", func(record map[string]any) bool { return record["type"] == "response" && record["id"] == "r" })
	data, _ := reply["data"].(map[string]any)
	snapshots, _ := data["snapshots"].([]any)
	if reply["success"] != true || len(snapshots) != 1 {
		t.Fatalf("resync = %v", reply)
	}
	if title, again := titleOf(snapshots[0]); title != "plan two" || again != digest {
		t.Fatalf("the resync has the board as %q with digest %q, want %q", title, again, digest)
	}
	if page.seen["end"] != 0 || page.seen["session"] != 1 {
		t.Fatalf("the page was sent %d end events and %d sessions, want 0 and 1", page.seen["end"], page.seen["session"])
	}
}

// TestConnectFollowsTheSessionToItsNextHost: when the session closes the connection (another session took its place,
// or it ended) the page is sent `end` and its
// session is gone; the bridge waits on one timer while there is no socket, connects to the next host and tells the
// page, which attaches and is sent the board as that host has it: its mounts only, with its digests. When the page
// goes away the bridge keeps no connection and no timer for it.
func TestConnectFollowsTheSessionToItsNextHost(t *testing.T) {
	s := newFakeSession(t)
	first := newHost(t)
	first.put("board", "panel", board("plan one"))
	first.put("note", "widget-above", note("only the first host has this"))
	s.serve(first)
	b := connectBridge(t, s.dir)
	base := b.state(t)

	page := b.follow(t)
	if snapshots := page.attach(); len(snapshots) != 2 {
		t.Fatalf("the first host sent %d snapshots, want 2", len(snapshots))
	}
	if now := b.state(t); now.Pages != 1 || now.Sessions != 1 || now.connections() != base.connections()+1 || now.timers() != base.timers() {
		t.Fatalf("with one page attached the bridge holds %+v, at the start %+v", now, base)
	}
	old := page.session
	s.end()
	if end := page.end(); !end.Retry || !strings.Contains(end.Reason, "closed the connection") {
		t.Fatalf("after the host ended the page is sent %+v", end)
	}
	if status := page.post(`{"id":"r","type":"ui_view_resync","attachment":1}`); status != http.StatusNotFound {
		t.Fatalf("a line for the session that ended = %d, want 404", status)
	}
	if reason := page.waiting(); !strings.Contains(reason, "no socket in "+s.dir) {
		t.Fatalf("while no host is served the page is told %q", reason)
	}
	// While there is no socket the bridge holds one timer for the page and no connection.
	if now := b.state(t); now.Pages != 1 || now.Sessions != 0 || now.timers() != base.timers()+1 || now.connections() != base.connections() {
		t.Fatalf("while the page waits the bridge holds %+v, at the start %+v", now, base)
	}

	second := newHost(t)
	second.put("board", "panel", board("plan two"))
	s.serve(second)
	snapshots := page.attach()
	if page.session == old {
		t.Fatal("the next connection has the session of the one that ended")
	}
	if len(snapshots) != 1 {
		t.Fatalf("the second host sent %d snapshots, want its board only: %v", len(snapshots), snapshots)
	}
	title, digest := titleOf(snapshots[0])
	if want := second.drawn(connectAbilities)["board"]; title != "plan two" || digest == "" || digest != want {
		t.Fatalf("the board after the reconnect is %q with digest %q, the second host has it with %q", title, digest, want)
	}

	page.close()
	eventually(t, "the bridge to hold nothing for the page that left", func() bool {
		now := b.state(t)
		return now.Pages == 0 && now.Sessions == 0 && now.timers() == base.timers() && now.connections() == base.connections()
	})
	eventually(t, "the session to hold its terminal only", func() bool { return second.held() == 1 })

	// A page that leaves while the bridge waits for a socket leaves no timer.
	waiting := b.follow(t)
	waiting.attach()
	s.end()
	waiting.end()
	waiting.waiting()
	if now := b.state(t); now.timers() != base.timers()+1 {
		t.Fatalf("while the page waits the bridge holds %+v, at the start %+v", now, base)
	}
	waiting.close()
	eventually(t, "the bridge to hold nothing for the page that left while it waited", func() bool {
		now := b.state(t)
		return now.Pages == 0 && now.timers() == base.timers() && now.connections() == base.connections()
	})
}

// TestConnectStopsTryingASessionThatNeverServes: an endpoint that closes every connection at once, as one with no room
// does, is tried again every 100 ms (RETRY_MS in bridge.mjs) and not faster, and after --wait the page is told and
// its stream ends.
func TestConnectStopsTryingASessionThatNeverServes(t *testing.T) {
	session := listenAt(t, filepath.Join(shortDir(t), "1.sock"), func(c net.Conn) { _ = c.Close() })
	b := connectBridge(t, session.path, "--wait", "1")
	base := b.state(t)
	page := b.follow(t)
	var end ending
	retried := 0
	for end = page.end(); end.Retry; end = page.end() {
		// One try at the start and one every 100 ms of the second the bridge waits.
		if retried++; retried > 11 {
			t.Fatalf("the session closed %d connections and the bridge still tries: its wait is 1 s", retried)
		}
	}
	if retried < 1 || !strings.Contains(end.Reason, "closed the connection") || !strings.Contains(end.Reason, "waited 1 s") {
		t.Fatalf("after %d connections that the session closed the page is told %+v", retried, end)
	}
	page.over()
	if n := session.connections(); n < 2 || n > 11 {
		t.Fatalf("the bridge connected %d times in the 1 s it waits, want 2 to 11: a try every 100 ms", n)
	}
	eventually(t, "the bridge to hold nothing for the page", func() bool {
		now := b.state(t)
		return now.Pages == 0 && now.timers() == base.timers() && now.connections() == base.connections()
	})
}

// TestConnectHoldsNoTimerForAPageThatLeft: a page leaves just after a try failed, with the timer for its next try set.
// From the moment the bridge counts no page it holds no timer, and then no connection.
func TestConnectHoldsNoTimerForAPageThatLeft(t *testing.T) {
	session := listenAt(t, filepath.Join(shortDir(t), "1.sock"), func(c net.Conn) { _ = c.Close() })
	b := connectBridge(t, session.path)
	base := b.state(t)
	page := b.follow(t)
	if end := page.end(); !end.Retry {
		t.Fatalf("after the session closed the first connection the page is sent %+v", end)
	}
	page.close()
	// The state is asked for without a pause: the first answer that counts no page is at most a few milliseconds after
	// the page left, well inside the 100 ms a timer that was left would still have to run.
	deadline := time.Now().Add(30 * time.Second)
	now := b.state(t)
	for now.Pages != 0 {
		if time.Now().After(deadline) {
			t.Fatal("the bridge still counts the page that left")
		}
		now = b.state(t)
	}
	if now.timers() != base.timers() {
		t.Fatalf("the bridge counts no page and holds %+v, at the start %+v: the timer of the page that left", now, base)
	}
	eventually(t, "the bridge to hold no connection", func() bool { return b.state(t).connections() == base.connections() })
}

// TestConnectPassesOnOnlyTheCarriersCommands: the session's socket is written the seven ui_view_* commands as the page
// posted them and nothing else. A prompt, any other command of RPC mode and a line that could name two commands are
// refused at the bridge.
func TestConnectPassesOnOnlyTheCarriersCommands(t *testing.T) {
	session := listenAt(t, filepath.Join(shortDir(t), "1.sock"), nil)
	b := connectBridge(t, session.path)
	page := b.follow(t)
	page.event("session")
	refused := []struct {
		line string
		want int
	}{
		{`{"id":"1","type":"prompt","message":"hello"}`, 403},
		{`{"id":"2","type":"get_state"}`, 403},
		{`{"id":"3","type":"ui_view"}`, 403},
		{`{"id":"4","type":"ui_view_attach "}`, 403},
		{`{"id":"5"}`, 403},
		{`{"id":"6","type":["ui_view_attach"]}`, 403},
		{`{"id":"7","type":"ui_view_attach","Type":"prompt","message":"hello"}`, 403},
		{`{"id":"8","TYPE":"ui_view_attach"}`, 403},
		{`["ui_view_attach"]`, 400},
		{`null`, 400},
		{`prompt`, 400},
	}
	for _, r := range refused {
		if status := page.post(r.line); status != r.want {
			t.Errorf("POST %s = %d, want %d", r.line, status, r.want)
		}
	}
	carrier := []string{
		connectAttach,
		`{"id":"b","type":"ui_view_abilities","attachment":1,"abilities":` + connectAbilities + `}`,
		`{"id":"c","type":"ui_view_resync","attachment":1}`,
		`{ "type" : "ui_view_intent", "id":"d", "attachment":1, "plugin":"plan", "mount":{"key":"board","epoch":1,"rev":1}, "intent":"board.pick", "arg":{"value":"\u00e9 1.0"} }`,
		`{"id":"d","type":"ui_view_intent_cancel"}`,
		`{"id":"e","type":"ui_view_report","attachment":1,"plugin":"plan","mount":{"key":"board","epoch":1},"notice":{"code":"undrawn","severity":"warning"}}`,
		`{"id":"f","type":"ui_view_detach","attachment":1}`,
	}
	for _, line := range carrier {
		if status := page.post(line); status != http.StatusNoContent {
			t.Errorf("POST %s = %d, want 204", line, status)
		}
	}
	want := strings.Join(carrier, "\n") + "\n"
	eventually(t, "the seven commands at the session's socket", func() bool { return len(session.written()) >= len(want) })
	if got := session.written(); got != want {
		t.Fatalf("the session's socket was written\n%s\nwant the seven commands as the page posted them\n%s", got, want)
	}
}

// TestConnectRefusesALineForASessionThatDoesNotRead: what the bridge holds for a session that stopped reading is
// bounded. Once the session is owed 8 MiB a further line is refused.
func TestConnectRefusesALineForASessionThatDoesNotRead(t *testing.T) {
	session := listenAt(t, filepath.Join(shortDir(t), "1.sock"), func(net.Conn) {})
	b := connectBridge(t, session.path)
	page := b.follow(t)
	page.event("session")
	line := `{"type":"ui_view_report","pad":"` + strings.Repeat("x", 3<<20) + `"}`
	for i := 1; ; i++ {
		status := page.post(line)
		if status == http.StatusServiceUnavailable {
			break
		}
		if status != http.StatusNoContent || i > 8 {
			t.Fatalf("line %d of 3 MiB for a session that does not read = %d, want 204 and, within 8 lines, 503", i, status)
		}
	}
}

// TestConnectGivesThePageNoLineTheSessionDidNotFinish: a connection that ends inside a line gives the page no line,
// and does not start the wait anew.
func TestConnectGivesThePageNoLineTheSessionDidNotFinish(t *testing.T) {
	session := listenAt(t, filepath.Join(shortDir(t), "1.sock"), func(c net.Conn) {
		_, _ = c.Write([]byte(`{"type":"ui_view","attachment":1,"seq":1,"snap`))
		_ = c.Close()
	})
	b := connectBridge(t, session.path, "--wait", "0.3")
	page := b.follow(t)
	partial, retried := 0, 0
	last := page.next("the end of the wait", func(e sse) bool {
		switch e.event {
		case "message":
			t.Errorf("the page was given the unfinished line %q", e.data)
		case "partial":
			partial++
		case "end":
			retried++
			return strings.Contains(e.data, `"retry":false`)
		}
		return false
	})
	if partial == 0 || retried < 2 || !strings.Contains(last.data, "waited 0.3 s") {
		t.Fatalf("%d unfinished lines, %d ends, the last %s", partial, retried, last.data)
	}
	page.over()
}

// stall opens a page's stream, attaches, and from then on reads nothing.
func (b bridge) stall(t *testing.T) net.Conn {
	t.Helper()
	c, err := net.Dial("tcp", strings.TrimPrefix(b.base, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	if _, err := fmt.Fprintf(c, "GET /events HTTP/1.1\r\nHost: %s\r\nAuthorization: %s\r\n\r\n", strings.TrimPrefix(b.base, "http://"), b.auth(t)); err != nil {
		t.Fatal(err)
	}
	_ = c.SetReadDeadline(time.Now().Add(30 * time.Second))
	// The session's id is read byte by byte so that nothing after it is taken from the connection.
	var head []byte
	for one := make([]byte, 1); !bytes.HasSuffix(head, []byte("\n\n")) || !bytes.Contains(head, []byte("event: session\ndata: ")); {
		if _, err := c.Read(one); err != nil {
			t.Fatalf("the stalled page's stream: %v after %q", err, head)
		}
		head = append(head, one[0])
	}
	_ = c.SetReadDeadline(time.Time{})
	lines := strings.Split(strings.TrimSuffix(string(head), "\n\n"), "\n")
	session := strings.TrimPrefix(lines[len(lines)-1], "data: ")
	req, _ := http.NewRequest(http.MethodPost, b.base+"/send?session="+session, strings.NewReader(connectAttach))
	req.Header.Set("Content-Type", "text/plain")
	req.Header.Set("Authorization", b.auth(t))
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("the stalled page's attach = %d", res.StatusCode)
	}
	return c
}

// TestConnectCutsAPageThatDoesNotRead: a page that stopped reading is owed at most 8 MiB by the bridge and is then
// cut, which ends its attachment. The session is read all the while: a second page is sent every update in order,
// without a gap, before and after the cut.
func TestConnectCutsAPageThatDoesNotRead(t *testing.T) {
	s := newFakeSession(t)
	h := newHost(t)
	s.serve(h)
	b := connectBridge(t, s.dir)
	healthy := b.follow(t)
	healthy.attach()
	stalled := b.stall(t)
	eventually(t, "both pages attached", func() bool { return h.held() == 3 })

	const size = 256 << 10
	text := func(i int) string { return fmt.Sprintf("update %d %s", i, strings.Repeat("x", size)) }
	ref := h.put("note", "widget-above", note(text(0)))
	seq := healthy.line("the mount", func(record map[string]any) bool { return record["type"] == "ui_view" })["seq"].(float64)
	// update makes update i and reads it at the healthy page: the next delivery, with that update's text.
	update := func(i int) {
		t.Helper()
		ref = h.replace(ref, note(text(i)))
		event := healthy.line(fmt.Sprintf("update %d", i), func(record map[string]any) bool { return record["type"] == "ui_view" })
		if title, _ := titleOf(event["snapshot"]); event["seq"] != seq+1 || !strings.HasPrefix(title, fmt.Sprintf("update %d ", i)) {
			t.Fatalf("after update %d the healthy page was sent delivery %v with %.20q, want delivery %v", i, event["seq"], title, seq+1)
		}
		seq++
	}
	cut := 0
	for i := 1; cut == 0; i++ {
		update(i)
		if b.state(t).Pages == 1 {
			cut = i
		} else if i*size > 64<<20 {
			t.Fatalf("the page that does not read is owed %d updates of %d bytes and was not cut", i, size)
		}
	}
	for i := cut + 1; i <= cut+3; i++ {
		update(i)
	}
	// The bridge closed the stalled page's stream: reading it now comes to its end.
	_ = stalled.SetReadDeadline(time.Now().Add(30 * time.Second))
	if _, err := io.Copy(io.Discard, stalled); err != nil && os.IsTimeout(err) {
		t.Fatal("the stream of the page that was cut is still open")
	}
	eventually(t, "the session to hold its terminal and the healthy page", func() bool { return h.held() == 2 })
	if now := b.state(t); now.Pages != 1 || now.Sessions != 1 {
		t.Fatalf("after the cut the bridge holds %+v, want the healthy page", now)
	}
	t.Logf("cut after %d updates of %d bytes", cut, size)
}

// TestPageFollowsARunningSession opens the page in headless Chromium (skipped where there is none) on a bridge that is
// connected to a session: the page draws the board and has no prompt, a click raises the intent and the board
// changes. When the extension ends, the page keeps its session: the broken mounts go from the page, each with a line
// that says so until its key is mounted again, and the board mounted again is drawn once. When the session's host ends the board goes from the page,
// and with the next host the page shows what that one holds, each mount once, by its content and by the digest the
// host gives.
func TestPageFollowsARunningSession(t *testing.T) {
	s := newFakeSession(t)
	first := newHost(t)
	first.put("board", "panel", board("plan one"))
	first.put("note", "widget-above", note("only the first host has this"))
	s.serve(first)
	b := connectBridge(t, s.dir)
	browser := browsertest.Start(t)
	b.openIn(t, browser, "")
	browser.Wait(`window.uiView && window.uiView.attachment > 0 && document.querySelectorAll(".mount").length === 2`)
	abilities := browser.String(`JSON.stringify(window.uiView.abilities)`)
	// shows compares the mounts on the page, each with the digest it was drawn from and its first text, with the mounts
	// of the host as it resolves them for this page.
	shows := func(h *host, titles map[string]string) {
		t.Helper()
		var want []string
		for key, digest := range h.drawn(abilities) {
			want = append(want, key+" "+digest+" "+titles[key])
		}
		sort.Strings(want)
		got := browser.String(`[...document.querySelectorAll(".mount")].map((m) => m.dataset.key + " " + m.dataset.digest + " " + m.querySelector(".text").textContent).sort().join("\n")`)
		if got != strings.Join(want, "\n") || len(want) != len(titles) || slices.Contains(want, "") {
			t.Fatalf("the page shows\n%s\nthe host has\n%s", got, strings.Join(want, "\n"))
		}
		if n := browser.String(`String(window.uiView.mounts.size)`); n != strconv.Itoa(len(want)) {
			t.Fatalf("the page holds %s mounts, want %d", n, len(want))
		}
	}
	pick := func(label, value string) {
		t.Helper()
		browser.Run(`[...document.querySelectorAll('[data-key="board"] [role=option]')].find((option) => option.textContent.includes("` + label + `")).click()`)
		browser.Wait(`document.querySelector('[data-key="board"] .text').textContent === "picked ` + value + `" && document.querySelector('[data-key="board"] footer').dataset.outcome === "done"`)
	}
	// gone waits until the page shows no mount and holds no session.
	gone := func() {
		t.Helper()
		browser.Wait(`document.querySelectorAll(".mount").length === 0 && window.uiView.mounts.size === 0 && window.uiView.attachment === 0 && window.uiView.session === ""`)
	}

	shows(first, map[string]string{"board": "plan one", "note": "only the first host has this"})
	// The page cannot prompt over a view endpoint: the prompt is gone, one line says why, and a submit sends nothing.
	if got := browser.String(`(() => { const form = document.getElementById("prompt"); form.elements.message.value = "hello"; form.requestSubmit();
		return [getComputedStyle(form).display, form.elements.message.disabled, document.getElementById("attached").hidden, document.getElementById("attached").textContent.includes("has no prompt"), document.querySelectorAll(".line.user").length].join(); })()`); got != "none,true,false,true,0" {
		t.Errorf("the prompt of a connected page: %s", got)
	}
	pick("Beta item", "beta")
	shows(first, map[string]string{"board": "picked beta", "note": "only the first host has this"})

	// The extension ends: the page keeps its session. The mounts arrive broken and go from the page, each with a line
	// that says so, and the board that is mounted again is drawn once.
	session := browser.String(`window.uiView.session`)
	first.ownerGone()
	browser.Wait(`document.querySelectorAll(".mount").length === 0 && window.uiView.mounts.size === 0`)
	notes := `[...document.querySelectorAll(".line.note")].map((line) => line.textContent).sort().join("|")`
	if got, want := browser.String(notes), "board closed: its extension stopped.|note closed: its extension stopped."; got != want {
		t.Errorf("after the extension ended the page says %q, want %q", got, want)
	}
	first.put("board", "panel", board("plan one again"))
	browser.Wait(`document.querySelectorAll(".mount").length > 0`)
	if got, want := browser.String(notes), "note closed: its extension stopped."; got != want {
		t.Errorf("with the board mounted again the page says %q, want %q", got, want)
	}
	shows(first, map[string]string{"board": "plan one again"})
	pick("Alpha item", "alpha")
	shows(first, map[string]string{"board": "picked alpha"})
	if got := browser.String(`window.uiView.session`); got != session {
		t.Errorf("the broken mounts gave the page the session %s, it had %s", got, session)
	}

	// The host ends, as when another session takes its place: the page shows nothing of it.
	s.end()
	gone()
	// The next host has another board and no note.
	second := newHost(t)
	second.put("board", "panel", board("plan two"))
	s.serve(second)
	browser.Wait(`window.uiView.attachment > 0 && document.querySelectorAll(".mount").length > 0`)
	shows(second, map[string]string{"board": "plan two"})
	pick("Beta item", "beta")
	shows(second, map[string]string{"board": "picked beta"})

	// A third host has the board the page drew last, digest and all: it is on the page once.
	s.end()
	gone()
	third := newHost(t)
	third.put("board", "panel", board("picked beta"))
	if third.drawn(abilities)["board"] != second.drawn(abilities)["board"] {
		t.Fatal("the same board has two digests")
	}
	s.serve(third)
	browser.Wait(`window.uiView.attachment > 0 && document.querySelectorAll(".mount").length > 0`)
	shows(third, map[string]string{"board": "picked beta"})
	pick("Alpha item", "alpha")
	if got := browser.String(`String(window.uiView.resyncs) + "/" + String(window.uiView.gaps) + "/" + String(window.uiView.reports)`); got != "0/0/0" {
		t.Errorf("resyncs/gaps/reports = %s, want 0/0/0", got)
	}
}

// TestPageSaysNothingOfAMountItsExtensionEndedOrMountedAgain: what the page tells the user when a mount ends. A mount
// its extension ended itself goes from the page without a line. A mount that breaks because its extension stopped
// (`broken`, reason `owner_gone`, which is also what a reload sends before the extensions load again) leaves one line
// in the user's words, by the mount's title, and that line goes when the same key is mounted again under a higher
// epoch; it stays while the key is not mounted again.
func TestPageSaysNothingOfAMountItsExtensionEndedOrMountedAgain(t *testing.T) {
	s := newFakeSession(t)
	h := newHost(t)
	first := h.put("board", "panel", `"title":"Plan",`+board("plan one"))
	slip := h.put("slip", "widget-above", note("a slip"))
	s.serve(h)
	b := connectBridge(t, s.dir)
	browser := browsertest.Start(t)
	b.openIn(t, browser, "")
	browser.Wait(`window.uiView && window.uiView.attachment > 0 && document.querySelectorAll(".mount").length === 2`)
	lines := func() string {
		return browser.String(`[...document.querySelectorAll(".line.note")].map((line) => line.textContent).join("|")`)
	}
	const stopped = "Plan closed: its extension stopped."

	// The extension ends its own mount.
	if _, err := h.reg.Finish(boardPlugin, uiview.FinishArgs{Mount: slip}); err != nil {
		t.Fatalf("finish: %v", err)
	}
	browser.Wait(`document.querySelectorAll(".mount").length === 1 && window.uiView.mounts.size === 1`)
	if got := lines(); got != "" {
		t.Errorf("after the extension ended its own mount the page says %q, want no line", got)
	}
	// A reload, as the page is sent it: the mount broken with owner_gone, then the same key under a higher epoch.
	h.ownerGone()
	browser.Wait(`document.querySelectorAll(".mount").length === 0 && window.uiView.mounts.size === 0`)
	if got := lines(); got != stopped {
		t.Errorf("while the board is broken the page says %q, want %q", got, stopped)
	}
	again := h.put("board", "panel", `"title":"Plan",`+board("plan two"))
	if again.Epoch <= first.Epoch {
		t.Fatalf("the board mounted again has epoch %d, the one before had %d", again.Epoch, first.Epoch)
	}
	browser.Wait(`document.querySelector('[data-key="board"] .text')?.textContent === "plan two"`)
	if got := lines(); got != "" {
		t.Errorf("with the board mounted again the page still says %q, want no line", got)
	}
	// An extension that stopped and does not come back: the line stays.
	h.ownerGone()
	browser.Wait(`document.querySelectorAll(".mount").length === 0 && window.uiView.mounts.size === 0`)
	if got := lines(); got != stopped {
		t.Errorf("after the extension stopped for good the page says %q, want %q", got, stopped)
	}
}
