//go:build integration && (linux || darwin)

package integration

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// pig additive (D614): the view endpoint of a real interactive session. The Piglet testdata/ui-view/shared.yaml selects
// the terminal view client and the endpoint, and the test is a second client on the session's socket beside the
// terminal that tmux drives.

const uiViewEndpointAbilities = `{"slots":["panel","widget-above","widget-below"],"nodes":["container","box","vstack","hstack","text","truncated-text","markdown","lines","spacer","dynamic-border","select-list","settings-list","input","editor","mouse-region","scroll-view"],"markdown":false,"imageFormats":[],"limits":{"nodes":2048,"depth":32,"snapshotBytes":1048576,"textBytes":65536,"mounts":32,"intentArgBytes":4096},"input":{"keyboard":true,"pointer":true,"text":true},"claims":[{"name":"client.kind","value":"test"}]}`

// uiViewShortHome returns a home whose path leaves room for the socket's: a socket address holds 104 bytes on macOS and
// 108 on Linux, and a test's own temporary directory carries the test's name.
func uiViewShortHome(t *testing.T) string {
	t.Helper()
	//portlint:allow hardtmp a socket path must stay under the 104-byte sun_path of macOS, which a scoped TMPDIR exceeds
	home, err := os.MkdirTemp("/tmp", "pv")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	return home
}

// uiViewSocket waits for the session's socket and returns its path and the pid it is named for.
func uiViewSocket(t *testing.T, r *uiViewRun) (string, int) {
	t.Helper()
	var found []string
	if !waitFor(2*time.Minute, func() bool {
		found, _ = filepath.Glob(filepath.Join(r.home, "agent", "view-endpoint", "*.sock"))
		return len(found) == 1
	}) {
		t.Fatalf("the session serves %d sockets, want 1: %v\n%s", len(found), found, r.capture())
	}
	pid, err := strconv.Atoi(strings.TrimSuffix(filepath.Base(found[0]), ".sock"))
	if err != nil {
		t.Fatalf("socket %s is not named for a pid", found[0])
	}
	return found[0], pid
}

type uiViewWire struct {
	t          *testing.T
	c          net.Conn
	r          *bufio.Reader
	attachment string
}

// uiViewDial connects to the session's socket, which may still be on its way after a start or a reload.
func uiViewDial(t *testing.T, r *uiViewRun) *uiViewWire {
	t.Helper()
	path, _ := uiViewSocket(t, r)
	var c net.Conn
	if !waitFor(30*time.Second, func() bool {
		var err error
		c, err = net.Dial("unix", path)
		return err == nil
	}) {
		t.Fatalf("no connection to %s", path)
	}
	t.Cleanup(func() { _ = c.Close() })
	return &uiViewWire{t: t, c: c, r: bufio.NewReaderSize(c, 1<<20)}
}

func (w *uiViewWire) send(line string) {
	w.t.Helper()
	if _, err := w.c.Write([]byte(line + "\n")); err != nil {
		w.t.Fatalf("send: %v", err)
	}
}

// next returns the next line with its text, or nil when the connection ended.
func (w *uiViewWire) next() (map[string]any, string) {
	w.t.Helper()
	_ = w.c.SetReadDeadline(time.Now().Add(2 * time.Minute))
	line, err := w.r.ReadString('\n')
	if err != nil {
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			w.t.Fatal("the client was written nothing for 2 minutes")
		}
		return nil, ""
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(line), &m); err != nil {
		w.t.Fatalf("line %q: %v", line, err)
	}
	return m, line
}

// until returns the first line of the given type whose text holds needle.
func (w *uiViewWire) until(kind, needle string) (map[string]any, string) {
	w.t.Helper()
	for {
		m, line := w.next()
		if m == nil {
			w.t.Fatalf("the connection ended before a %s line with %q", kind, needle)
		}
		if m["type"] == kind && strings.Contains(line, needle) {
			return m, line
		}
	}
}

// ended reads until the connection ends and reports whether it did.
func (w *uiViewWire) ended() bool {
	w.t.Helper()
	for {
		if m, _ := w.next(); m == nil {
			return true
		}
	}
}

// attach attaches and returns the snapshots of the reply.
func (w *uiViewWire) attach() []any {
	w.t.Helper()
	w.send(`{"id":"attach","type":"ui_view_attach","abilities":` + uiViewEndpointAbilities + `}`)
	r, line := w.until("response", `"command":"ui_view_attach"`)
	if r["success"] != true {
		w.t.Fatalf("attach = %s", line)
	}
	data := r["data"].(map[string]any)
	w.attachment = strconv.Itoa(int(data["attachment"].(float64)))
	return data["snapshots"].([]any)
}

// pick raises the fixture's intent on the panel the event carries, as a click on an item does.
func (w *uiViewWire) pick(id string, event map[string]any, value string) {
	w.t.Helper()
	s := event["snapshot"].(map[string]any)
	mount := s["mount"].(map[string]any)
	w.send(fmt.Sprintf(`{"id":%q,"type":"ui_view_intent","attachment":%s,"plugin":%q,"mount":{"key":%q,"epoch":%d,"rev":%d},"nodeKey":"pick","intent":"viewfx.pick","arg":{"value":%q}}`,
		id, w.attachment, s["plugin"], mount["key"], int(mount["epoch"].(float64)), int(mount["rev"].(float64)), value))
}

// One session, two clients: what the client on the socket picks the terminal shows, and what the terminal's keys pick
// the client on the socket is sent.
func TestUIViewEndpointAndTerminalShareOneSession(t *testing.T) {
	run := startUIViewAt(t, uiViewShortHome(t), "shared.yaml", "fullscreen", true)
	wire := uiViewDial(t, run)
	if snapshots := wire.attach(); len(snapshots) != 0 {
		t.Fatalf("a session without a mount sent %d snapshots", len(snapshots))
	}
	run.send("/vmount\r")
	run.settle("fixture widget row", "fixture panel", "Alpha item", "Beta item")
	panel, line := wire.until("ui_view", "Alpha item")
	if !strings.Contains(line, `"viewfx.pick"`) || !strings.Contains(line, "fixture panel") {
		t.Fatalf("the panel as the socket's client is sent it lacks the intent or the title: %s", line)
	}
	// The client on the socket picks: the handler's redraw reaches it before the intent's reply, and the terminal.
	wire.pick("p1", panel, "alpha")
	redrawn, _ := wire.until("ui_view", "picked alpha")
	reply, text := wire.until("response", `"id":"p1"`)
	if reply["success"] != true || reply["data"].(map[string]any)["outcome"] != "done" {
		t.Fatalf("the intent raised on the socket = %s", text)
	}
	run.settle("picked alpha")
	// The terminal picks: Down and Enter go to the focused list. The client on the socket is sent the redraw.
	run.sendKey("Down")
	run.sendKey("Enter")
	run.settle("picked beta")
	later, _ := wire.until("ui_view", "picked beta")
	if later["seq"].(float64) <= redrawn["seq"].(float64) {
		t.Fatalf("delivery %v after %v", later["seq"], redrawn["seq"])
	}
	// And back: the socket's client picks on the view the terminal changed.
	wire.pick("p2", later, "alpha")
	wire.until("ui_view", "picked alpha")
	run.settle("picked alpha")
}

// A client that attached and reads nothing does not hold the terminal: every pick made with the keys is drawn.
func TestUIViewEndpointClientThatDoesNotReadDoesNotHoldTheTerminal(t *testing.T) {
	run := startUIViewAt(t, uiViewShortHome(t), "shared.yaml", "fullscreen", true)
	stalled := uiViewDial(t, run)
	stalled.attach()
	run.send("/vmount\r")
	run.settle("fixture panel", "Alpha item")
	for i := range 12 {
		key, want := "Down", "picked beta"
		if i%2 == 1 {
			key, want = "Up", "picked alpha"
		}
		run.sendKey(key)
		run.sendKey("Enter")
		run.settle(want)
	}
	// The stalled client was owed every one of those redraws and is still attached.
	if _, line := stalled.until("ui_view", "picked alpha"); line == "" {
		t.Fatal("the stalled client was sent nothing")
	}
}

// A reload keeps the extension host and its registry, so a client on the socket keeps its connection and its
// attachment and is sent nothing because of the reload: a resync under the same attachment returns the mounts as they
// are. A new session replaces the host: the connection is closed, the socket removed, and the next host is served under
// the same path with a registry of its own.
func TestUIViewEndpointReloadKeepsAndNewSessionClosesTheConnection(t *testing.T) {
	run := startUIViewAt(t, uiViewShortHome(t), "shared.yaml", "fullscreen", true)
	path, _ := uiViewSocket(t, run)
	wire := uiViewDial(t, run)
	wire.attach()
	run.send("/vmount\r")
	run.settle("fixture panel", "Alpha item")
	wire.until("ui_view", "Alpha item")

	// The panel's list holds the focus: Escape returns it to the editor, where the command is typed.
	run.sendKey("Escape")
	run.settle()
	run.send("/reload\r")
	run.settle("Reloaded")
	wire.send(`{"id":"r","type":"ui_view_resync","attachment":` + wire.attachment + `}`)
	reply, line := wire.until("response", `"id":"r"`)
	if reply["success"] != true || !strings.Contains(line, "Alpha item") || !strings.Contains(line, `"state":"live"`) {
		t.Fatalf("after the reload a resync under the same attachment = %s", line)
	}
	if got, _ := uiViewSocket(t, run); got != path {
		t.Fatalf("after /reload the socket is %s, was %s", got, path)
	}

	run.send("/new\r")
	if !wire.ended() {
		t.Fatal("/new left the connection open")
	}
	again := uiViewDial(t, run)
	if got, _ := uiViewSocket(t, run); got != path {
		t.Fatalf("after /new the socket is %s, was %s", got, path)
	}
	if snapshots := again.attach(); len(snapshots) != 0 {
		t.Fatalf("after /new the registry holds %d mounts of the session before", len(snapshots))
	}
	run.settle()
	run.send("/vmount\r")
	run.settle("fixture panel", "Alpha item")
	again.until("ui_view", "Alpha item")
}

// The socket is removed when the session ends: by /quit and by each signal that ends pig.
func TestUIViewEndpointSocketIsRemovedAtExit(t *testing.T) {
	ends := []struct {
		name string
		end  func(run *uiViewRun, pid int)
	}{
		{"quit", func(run *uiViewRun, _ int) { run.send("/quit\r") }},
		{"SIGTERM", func(_ *uiViewRun, pid int) { _ = syscall.Kill(pid, syscall.SIGTERM) }},
		{"SIGINT", func(_ *uiViewRun, pid int) { _ = syscall.Kill(pid, syscall.SIGINT) }},
		{"SIGHUP", func(run *uiViewRun, _ int) { run.kill() }},
	}
	for _, e := range ends {
		t.Run(e.name, func(t *testing.T) {
			run := startUIViewAt(t, uiViewShortHome(t), "shared.yaml", "fullscreen", true)
			wire := uiViewDial(t, run)
			wire.attach()
			// A second client that reads nothing must not hold the exit.
			stalled := uiViewDial(t, run)
			stalled.attach()
			path, pid := uiViewSocket(t, run)
			e.end(run, pid)
			if !wire.ended() {
				t.Fatal("the connection outlived the session")
			}
			if !waitFor(15*time.Second, func() bool {
				_, err := os.Lstat(path)
				return err != nil && syscall.Kill(pid, 0) != nil
			}) {
				_, err := os.Lstat(path)
				t.Fatalf("after the end: socket lstat error %v, process alive %v", err, syscall.Kill(pid, 0) == nil)
			}
		})
	}
}

// Stock PiG and a Piglet without the key serve nothing: no directory is made. A Piglet with the key serves a socket
// in an interactive session only; print, JSON and RPC mode ignore the key.
func TestUIViewEndpointIsSelectedByThePigletAndInteractiveOnly(t *testing.T) {
	absent := func(t *testing.T, home string) {
		t.Helper()
		if _, err := os.Lstat(filepath.Join(home, "agent", "view-endpoint")); err == nil {
			t.Fatal("the view endpoint's directory was made")
		}
	}
	t.Run("interactive without the key", func(t *testing.T) {
		run := startUIView(t, "on.yaml", "fullscreen", true)
		run.send("/vmount\r")
		run.settle("fixture panel", "Alpha item")
		absent(t, run.home)
	})
	root, err := findRepoRoot()
	if err != nil {
		t.Fatal(err)
	}
	shared := filepath.Join(root, "test", "integration", "testdata", "ui-view", "shared.yaml")
	modes := []struct {
		name  string
		args  []string
		stdin string
		want  string
	}{
		{"print", []string{"-p", "What is 20+22?"}, "", "42"},
		{"json", []string{"--mode", "json", "What is 20+22?"}, "", "42"},
		{"rpc", []string{"--mode", "rpc"}, `{"id":"1","type":"get_state"}` + "\n", `"command":"get_state","success":true`},
	}
	for _, mode := range modes {
		t.Run(mode.name+" with the key", func(t *testing.T) {
			bin := buildBinary(t)
			home, cwd := t.TempDir(), t.TempDir()
			agentDir := filepath.Join(home, "agent")
			if err := os.MkdirAll(agentDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(uiViewModels), 0o600); err != nil {
				t.Fatal(err)
			}
			trustIntegrationProjectAt(t, home, cwd)
			seedIntegrationSettings(t, home)
			cmd := exec.Command(bin, append([]string{"--piglet", shared, "--model", "test-faux/faux-1"}, mode.args...)...)
			cmd.Dir = cwd
			cmd.Env = append(os.Environ(), "PIG_HOME="+home, "PIG_OFFLINE=1", "PIG_TEST_FAUX=1", "PIG_TEST_FAUX_SCENARIO=parity-basic", "PIG_UI_VIEW_FIXTURE_MOUNTS=1")
			cmd.Stdin = strings.NewReader(mode.stdin)
			out, err := cmd.Output()
			if err != nil || !strings.Contains(string(out), mode.want) {
				t.Fatalf("pig %v: %v\n%s", mode.args, err, out)
			}
			absent(t, home)
		})
	}
}
