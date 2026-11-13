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
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// pig additive (D614): the view endpoint of a real interactive session. The Piglet testdata/ui-view/shared.yaml selects
// the terminal view client and the endpoint, and the test is a second client on the session's socket beside the
// terminal that tmux drives.

const uiViewEndpointAbilities = `{"slots":["panel","widget-above","widget-below"],"nodes":["container","box","vstack","hstack","text","truncated-text","markdown","lines","spacer","dynamic-border","select-list","settings-list","input","editor","mouse-region","scroll-view"],"markdown":false,"imageFormats":[],"limits":{"nodes":2048,"depth":32,"snapshotBytes":1048576,"textBytes":65536,"mounts":32,"intentArgBytes":4096},"input":{"keyboard":true,"pointer":true,"text":true},"claims":[{"name":"client.kind","value":"test"}]}`

// uiViewShortHome returns a home whose path leaves room for the socket's: a socket address holds 104 bytes on macOS and
// 108 on Linux, and a test's own temporary directory carries the test's name.
func uiViewShortHome(t *testing.T) string {
	t.Helper()
	return testenv.ShortTempDir(t, "pv")
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
// attachment: a resync under the same attachment succeeds. The mounts of the reloaded extension ended
// (TestUIViewEndpointReloadEndsTheMountsOfTheReloadedExtensions), so it returns no live mount. A new session replaces
// the host: the connection is closed, the socket removed, and the next host is served under
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
	if reply["success"] != true || strings.Contains(line, `"state":"live"`) {
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

// through returns the lines the client is written up to the reply to the command id, that reply last.
func (w *uiViewWire) through(id string) []string {
	w.t.Helper()
	var lines []string
	for {
		m, line := w.next()
		if m == nil {
			w.t.Fatalf("the connection ended before the reply to %s; written before it: %q", id, lines)
		}
		lines = append(lines, strings.TrimSpace(line))
		if m["type"] == "response" && m["id"] == id {
			return lines
		}
	}
}

// subPick raises the subprocess fixture's intent on the mount the snapshot names.
func (w *uiViewWire) subPick(id string, snapshot map[string]any, value string) {
	w.t.Helper()
	mount := snapshot["mount"].(map[string]any)
	w.send(fmt.Sprintf(`{"id":%q,"type":"ui_view_intent","attachment":%s,"plugin":%q,"mount":{"key":%q,"epoch":%d,"rev":%d},"intent":"viewsub.pick","arg":{"value":%q}}`,
		id, w.attachment, snapshot["plugin"], mount["key"], int(mount["epoch"].(float64)), int(mount["rev"].(float64)), value))
}

var uiViewDrawnBy = regexp.MustCompile(`by (\d+)`)

// uiViewDrawer returns the process the fixture's text in s names.
func uiViewDrawer(t *testing.T, s string) int {
	t.Helper()
	m := uiViewDrawnBy.FindStringSubmatch(s)
	if m == nil {
		t.Fatalf("no drawing process is named in:\n%s", s)
	}
	pid, err := strconv.Atoi(m[1])
	if err != nil {
		t.Fatal(err)
	}
	return pid
}

// uiViewKeyOf and uiViewEpochOf read a snapshot's mount.
func uiViewKeyOf(snapshot map[string]any) string {
	key, _ := snapshot["mount"].(map[string]any)["key"].(string)
	return key
}

func uiViewEpochOf(snapshot map[string]any) float64 {
	epoch, _ := snapshot["mount"].(map[string]any)["epoch"].(float64)
	return epoch
}

// live reads ui_view events until it holds a live snapshot of every key, and returns them by key.
func (w *uiViewWire) live(keys []string) map[string]map[string]any {
	w.t.Helper()
	held := map[string]map[string]any{}
	for len(held) < len(keys) {
		event, line := w.until("ui_view", `"state":"live"`)
		snapshot := event["snapshot"].(map[string]any)
		if !slices.Contains(keys, uiViewKeyOf(snapshot)) {
			w.t.Fatalf("a mount outside %v: %s", keys, line)
		}
		held[uiViewKeyOf(snapshot)] = snapshot
	}
	return held
}

// uiViewCopiedPiglet copies the subprocess Piglet and its extension to a directory of the test and returns the Piglet's
// path and the extension's, so a row can change the extension's source before a reload.
func uiViewCopiedPiglet(t *testing.T) (piglet, source string) {
	t.Helper()
	root, err := findRepoRoot()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for _, name := range []string{"subprocess.yaml", "viewsub.mjs"} {
		data, err := os.ReadFile(filepath.Join(root, "test", "integration", "testdata", "ui-view", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(dir, "subprocess.yaml"), filepath.Join(dir, "viewsub.mjs")
}

// uiViewPigletBinary builds the Piglet piglet of testdata/ui-view from this checkout's source into a Piglet Binary, with the pig the tests built, and returns the Binary's path.
func uiViewPigletBinary(t *testing.T, piglet string) string {
	t.Helper()
	root, err := findRepoRoot()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	binary := filepath.Join(dir, "pig-ui-view")
	cmd := exec.CommandContext(t.Context(), buildBinary(t), "piglet", "build", filepath.Join(root, "test", "integration", "testdata", "ui-view", piglet), "--format", "binary", "--builder", "native", "--out", binary)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "PIG_SOURCE_ROOT="+root)
	for _, key := range []string{"PIG_HOME", "PIG_CODING_AGENT_DIR", "PI_HOME", "PI_CODING_AGENT_DIR"} {
		cmd.Env = append(cmd.Env, key+"="+filepath.Join(dir, strings.ToLower(key)))
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("pig piglet build %s: %v\n%s", piglet, err, out)
	}
	return binary
}

// A reload replaces every extension's instance, and a mount belongs to the instance that made it (D614, as Pi's reload
// removes everything an extension drew). The client on the socket keeps its connection and its attachment and is sent
// each mount of the reloaded extension once more, broken with reason owner_gone, and nothing else; the terminal's
// panel goes. An intent raised afterwards on the mount of the instance before is refused and reaches no handler.
// The instance the reload loaded mounts the same keys again under a new epoch, drawn by both, and answers its intents.
// The rows: a Node extension in a process of its own, with its source as it was and with its source changed before the
// reload, the Go fixture, and the fixture's factory form as a member fused into a Piglet Binary, which runs in pig's own
// process.
func TestUIViewEndpointReloadEndsTheMountsOfTheReloadedExtensions(t *testing.T) {
	rows := []struct {
		name           string
		piglet         string // a file of testdata/ui-view, or "" for the copied subprocess Piglet
		binary         bool   // the session runs the Piglet Binary built from piglet
		edit           bool   // the extension's source changes before the reload
		mount          string
		keys           []string // the mounts' keys, the panel first
		needle, picked string   // the panel's text as mounted, and after a pick of alpha
		focused        bool     // the panel takes the keys, so Escape precedes a typed command
		pick           func(w *uiViewWire, id string, snapshot map[string]any)
	}{
		{name: "a Node extension in its own process", mount: "/smount", keys: []string{"panel"}, needle: "sub panel by ", picked: "picked alpha by ",
			pick: func(w *uiViewWire, id string, snapshot map[string]any) { w.subPick(id, snapshot, "alpha") }},
		{name: "a Node extension whose source changed", edit: true, mount: "/smount", keys: []string{"panel"}, needle: "sub panel by ", picked: "picked alpha by ",
			pick: func(w *uiViewWire, id string, snapshot map[string]any) { w.subPick(id, snapshot, "alpha") }},
		{name: "the Go fixture", piglet: "shared.yaml", mount: "/vmount", keys: []string{"panel", "note"}, needle: "fixture panel", picked: "picked alpha", focused: true,
			pick: func(w *uiViewWire, id string, snapshot map[string]any) {
				w.pick(id, map[string]any{"snapshot": snapshot}, "alpha")
			}},
		{name: "a Go member fused into a Piglet Binary", piglet: "fused.yaml", binary: true, mount: "/vmount", keys: []string{"panel", "note"}, needle: "fixture panel", picked: "picked alpha", focused: true,
			pick: func(w *uiViewWire, id string, snapshot map[string]any) {
				w.pick(id, map[string]any{"snapshot": snapshot}, "alpha")
			}},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			var run *uiViewRun
			var source string
			switch {
			case row.binary:
				run = startUIViewBinary(t, uiViewShortHome(t), uiViewPigletBinary(t, row.piglet), "fullscreen")
			case row.piglet == "":
				var piglet string
				piglet, source = uiViewCopiedPiglet(t)
				run = startUIViewPiglet(t, uiViewShortHome(t), piglet, "fullscreen", true)
			default:
				run = startUIViewAt(t, uiViewShortHome(t), row.piglet, "fullscreen", true)
			}
			path, pig := uiViewSocket(t, run)
			wire := uiViewDial(t, run)
			wire.attach()
			command := func(text string) {
				if row.focused {
					run.sendKey("Escape")
					run.settle()
				}
				run.send(text + "\r")
			}
			children := func(when string) {
				out, _ := exec.Command("ps", "-o", "pid=,stat=,args=", "--ppid", strconv.Itoa(pig)).Output()
				for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
					t.Logf("%s, a child of pig %d: %.120s", when, pig, l)
				}
			}
			run.send(row.mount + "\r")
			run.settle(row.needle)
			first := wire.live(row.keys)
			children("before the reload")

			if row.edit {
				data, err := os.ReadFile(source)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(source, []byte(strings.Replace(string(data), `const EDITION = "one";`, `const EDITION = "two";`, 1)), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			command("/reload")
			ended := map[string]bool{}
			for len(ended) < len(row.keys) {
				event, line := wire.until("ui_view", "")
				snapshot := event["snapshot"].(map[string]any)
				key := uiViewKeyOf(snapshot)
				if snapshot["state"] != "broken" || snapshot["reason"] != "owner_gone" || first[key] == nil || uiViewEpochOf(snapshot) != uiViewEpochOf(first[key]) || ended[key] {
					t.Fatalf("after /reload the client was sent %s\nwant each mount of the reloaded extension once, broken with reason owner_gone", line)
				}
				ended[key] = true
			}
			run.settle("Reloaded")
			run.gone(row.needle)
			children("after the reload")
			if got, _ := uiViewSocket(t, run); got != path {
				t.Fatalf("after /reload the socket is %s, was %s", got, path)
			}
			// Nothing else was sent because of the reload, and the same attachment holds no live mount.
			wire.send(`{"id":"r","type":"ui_view_resync","attachment":` + wire.attachment + `}`)
			if lines := wire.through("r"); len(lines) != 1 || !strings.Contains(lines[0], `"success":true`) || strings.Contains(lines[0], `"state":"live"`) {
				t.Fatalf("after the reload, up to the reply of a resync under the same attachment, the client was sent %q\nwant the reply alone, with no live mount", lines)
			}
			row.pick(wire, "old", first[row.keys[0]])
			if lines := wire.through("old"); len(lines) != 1 || !strings.Contains(lines[0], `"outcome":"refused","error":"mount is broken"`) {
				t.Fatalf("an intent on the mount of the instance before the reload: %q\nwant its reply alone, refused because the mount is broken", lines)
			}

			// The instance the reload loaded mounts the same keys: new mounts, drawn by both clients.
			run.send(row.mount + "\r")
			second := wire.live(row.keys)
			for _, key := range row.keys {
				if uiViewEpochOf(second[key]) <= uiViewEpochOf(first[key]) {
					t.Fatalf("the mount %s of the reloaded instance has epoch %v, the one before had %v", key, uiViewEpochOf(second[key]), uiViewEpochOf(first[key]))
				}
			}
			pane := run.settle(row.needle)
			if row.edit && !strings.Contains(pane, "edition two") {
				t.Fatalf("the reloaded extension does not run its changed source:\n%s", pane)
			}
			row.pick(wire, "new", second[row.keys[0]])
			wire.until("ui_view", row.picked)
			if reply, line := wire.until("response", `"id":"new"`); reply["success"] != true || reply["data"].(map[string]any)["outcome"] != "done" {
				t.Fatalf("an intent on the reloaded instance's mount = %s", line)
			}
			run.settle(row.picked)
		})
	}
}

// An extension's process that ends takes its mounts with it: the client on the socket is sent the mount broken with
// reason owner_gone and the terminal's panel goes, an intent on it is refused, and after a reload started
// the extension again its mount is a new one, drawn and answered by the new process.
func TestUIViewEndpointExtensionProcessEndBreaksItsMounts(t *testing.T) {
	piglet, _ := uiViewCopiedPiglet(t)
	run := startUIViewPiglet(t, uiViewShortHome(t), piglet, "fullscreen", true)
	wire := uiViewDial(t, run)
	wire.attach()
	run.send("/smount\r")
	killed := uiViewDrawer(t, run.settle("sub panel by "))
	first := wire.live([]string{"panel"})["panel"]
	if err := syscall.Kill(killed, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	event, line := wire.until("ui_view", "")
	if snapshot := event["snapshot"].(map[string]any); snapshot["state"] != "broken" || snapshot["reason"] != "owner_gone" || uiViewEpochOf(snapshot) != uiViewEpochOf(first) {
		t.Fatalf("after the extension's process was killed the client was sent %s\nwant its mount broken with reason owner_gone", line)
	}
	run.gone("sub panel by ")
	wire.subPick("dead", first, "alpha")
	lines := wire.through("dead")
	if len(lines) != 1 || !strings.Contains(lines[0], `"outcome":"refused"`) {
		t.Fatalf("an intent on the mount of the killed process: %q\nwant its reply alone, refused", lines)
	}

	run.send("/reload\r")
	run.settle("Reloaded")
	run.send("/smount\r")
	second := wire.live([]string{"panel"})["panel"]
	started := uiViewDrawer(t, run.settle("sub panel by "))
	if started == killed || uiViewEpochOf(second) <= uiViewEpochOf(first) {
		t.Fatalf("after the reload the mount has epoch %v and is drawn by %d; the killed process was %d and its mount had epoch %v", uiViewEpochOf(second), started, killed, uiViewEpochOf(first))
	}
	wire.subPick("new", second, "alpha")
	wire.until("ui_view", "picked alpha by "+strconv.Itoa(started))
	if reply, line := wire.until("response", `"id":"new"`); reply["success"] != true || reply["data"].(map[string]any)["outcome"] != "done" {
		t.Fatalf("an intent on the new process's mount = %s", line)
	}
	run.settle("picked alpha by " + strconv.Itoa(started))
}

// An agent directory whose socket path does not fit a socket address: the session starts and draws the mounts, the
// user is told once, in one notice that names the path, its length and the limit, and nothing is bound.
func TestUIViewEndpointTellsTheUserWhenTheSocketPathIsTooLong(t *testing.T) {
	home := filepath.Join(uiViewShortHome(t), strings.Repeat("h", 90))
	run := startUIViewAt(t, home, "shared.yaml", "fullscreen", true)
	dir := filepath.Join(home, "agent", "view-endpoint") + string(filepath.Separator)
	// The notice is longer than the terminal is wide: it is read without the breaks the terminal put into it.
	unbroken := func(s string) string { return strings.Join(strings.Fields(s), "") }
	notice := regexp.MustCompile(regexp.QuoteMeta(unbroken("View endpoint: the socket path "+dir)) + `(\d+\.sock)is(\d+)bytes,` +
		regexp.QuoteMeta(unbroken(fmt.Sprintf("longer than the %d a socket address holds; no view endpoint is served", len(syscall.RawSockaddrUnix{}.Path)-1))))
	pane := run.settle("View endpoint:")
	found := notice.FindStringSubmatch(unbroken(pane))
	if found == nil || found[2] != strconv.Itoa(len(dir)+len(found[1])) || strings.Count(pane, "View endpoint:") != 1 {
		t.Fatalf("the terminal does not show, once, the notice that names the socket path under %s, its length and the limit:\n%s", dir, pane)
	}
	if found, _ := filepath.Glob(filepath.Join(home, "agent", "view-endpoint", "*")); len(found) != 0 {
		t.Fatalf("something was made in the endpoint's directory: %v", found)
	}
	run.send("/vmount\r")
	run.settle("fixture panel", "Alpha item")
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
