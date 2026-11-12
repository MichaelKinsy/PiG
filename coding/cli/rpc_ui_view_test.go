package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// rpcLines is the client of one RPC stream that keeps every line the server wrote, verbatim.
type rpcLines struct {
	t      *testing.T
	in     io.WriteCloser
	lines  chan string
	seen   []string
	stderr *lockedBuffer
	budget time.Duration
	// gate is held while the client does not read its stream.
	gate sync.Mutex
}

func newRPCLines(t *testing.T, in io.WriteCloser, out io.Reader, stderr *lockedBuffer) *rpcLines {
	t.Helper()
	c := &rpcLines{t: t, in: in, lines: make(chan string, 1<<16), stderr: stderr, budget: testbudget.Wait(t)}
	go func() {
		defer close(c.lines)
		r := bufio.NewReaderSize(out, 1<<16)
		for {
			c.passGate()
			line, err := r.ReadString('\n')
			if line != "" {
				c.lines <- strings.TrimSuffix(line, "\n")
			}
			if err != nil {
				return
			}
		}
	}()
	return c
}

// startRPCLines starts `pig --mode rpc` over dir with the plugin UI fixture loaded.
func startRPCLines(t *testing.T, dir string, env []string, args ...string) *rpcLines {
	t.Helper()
	fixture, err := filepath.Abs(filepath.Join("testdata", "rpc-ui-view.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	argv := append([]string{"--mode", "rpc", "--model", "test-faux/echo", "--no-session", "-e", fixture}, args...)
	cmd := exec.Command(buildPigBinaryForSignalTest(t), argv...)
	cmd.Env = append(append(os.Environ(), listenEnv(t, dir)...), env...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stderr := &lockedBuffer{}
	cmd.Stderr = stderr
	cmd.WaitDelay = 10 * time.Second
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	return newRPCLines(t, stdin, stdout, stderr)
}

func (c *rpcLines) send(line string) {
	c.t.Helper()
	if _, err := io.WriteString(c.in, line+"\n"); err != nil {
		c.t.Fatal(err)
	}
}

// until consumes lines until done reports true and returns that line and its record.
func (c *rpcLines) until(what string, done func(record map[string]any) bool) (string, map[string]any) {
	c.t.Helper()
	timer := time.NewTimer(c.budget)
	defer timer.Stop()
	for {
		select {
		case line, ok := <-c.lines:
			if !ok {
				c.t.Fatalf("the output ended before %s\n%s", what, c.stderr.String())
			}
			c.seen = append(c.seen, line)
			var record map[string]any
			if err := json.Unmarshal([]byte(line), &record); err != nil {
				c.t.Fatalf("line %q is not a JSON object: %v", line, err)
			}
			if done(record) {
				return line, record
			}
		case <-timer.C:
			c.t.Fatalf("%s timed out after %s; lines so far:\n%s\n%s", what, c.budget, strings.Join(c.seen, "\n"), c.stderr.String())
		}
	}
}

// response consumes lines until the response of command to the request id.
func (c *rpcLines) response(id, command string) (string, map[string]any) {
	c.t.Helper()
	return c.until("the "+command+" response to "+id, func(record map[string]any) bool {
		return record["type"] == "response" && record["id"] == id && record["command"] == command
	})
}

// request sends a command line and returns its response.
func (c *rpcLines) request(line string) (string, map[string]any) {
	c.t.Helper()
	var head struct{ ID, Type string }
	if err := json.Unmarshal([]byte(line), &head); err != nil || head.ID == "" {
		c.t.Fatalf("request %q needs a string id and a type: %v", line, err)
	}
	c.send(line)
	return c.response(head.ID, head.Type)
}

// end closes the input and returns every line the server wrote.
func (c *rpcLines) end() []string {
	c.t.Helper()
	_ = c.in.Close()
	timer := time.NewTimer(c.budget)
	defer timer.Stop()
	for {
		select {
		case line, ok := <-c.lines:
			if !ok {
				return c.seen
			}
			c.seen = append(c.seen, line)
		case <-timer.C:
			c.t.Fatalf("the output did not end after the input closed\n%s", c.stderr.String())
		}
	}
}

var rpcViewRunIDs = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)

const rpcViewAbilities = `{"slots":["panel"],"nodes":["text"],"markdown":false,"imageFormats":[],"limits":{"nodes":64,"depth":8,"snapshotBytes":65536,"textBytes":4096,"mounts":8,"intentArgBytes":100},"input":{"keyboard":true,"pointer":true,"text":true}}`

// TestRPCUIViewNeverAttachedClientSeesPiBytes pins D614's promise to a client that never attaches: an extension that
// mounts a view changes none of the client's lines except its own reported result, no carrier line is written, and a
// type that is not one of the carrier's seven commands gets Pi's unknown-command bytes (parity scenario
// 02-rpc-unknown-command), the carrier's event names and near misses of its command names included.
func TestRPCUIViewNeverAttachedClientSeesPiBytes(t *testing.T) {
	if testing.Short() {
		t.Skip("starts pig and a Node extension")
	}
	unknown := []string{"ui_view", "ui_view_gap", "ui_view_bogus", "ui_view_attached", "ui_view_asset", "UI_VIEW_ATTACH"}
	dir := listenTempDir(t)
	run := func(mount string) []string {
		c := startRPCLines(t, dir, []string{"PIG_TEST_VIEW_MOUNT=" + mount})
		c.request(`{"id":"try","type":"prompt","message":"/view-try"}`)
		c.request(`{"id":"state","type":"get_state"}`)
		for _, name := range unknown {
			line, _ := c.request(`{"id":"u-` + name + `","type":"` + name + `","attachment":1}`)
			if want := `{"id":"u-` + name + `","type":"response","command":"` + name + `","success":false,"error":"Unknown command: ` + name + `"}`; line != want {
				t.Errorf("mount=%s: %s answered\n  %s\nwant Pi's unknown-command bytes\n  %s", mount, name, line, want)
			}
		}
		c.request(`{"id":"commands","type":"get_commands"}`)
		lines := c.end()
		for i, line := range lines {
			lines[i] = rpcViewRunIDs.ReplaceAllString(line, "<id>")
		}
		return lines
	}
	plain, mounted := run("0"), run("1")
	for _, line := range mounted {
		if strings.Contains(line, `"type":"ui_view`) {
			t.Errorf("a client that never attached was written a carrier line: %s", line)
		}
	}
	if len(plain) != len(mounted) {
		t.Fatalf("the session with the mount wrote %d lines, the one without %d:\n%s\n---\n%s", len(mounted), len(plain), strings.Join(mounted, "\n"), strings.Join(plain, "\n"))
	}
	differing := 0
	for i := range plain {
		if plain[i] == mounted[i] {
			continue
		}
		differing++
		if !strings.Contains(plain[i], "view-result:none") || strings.Replace(plain[i], "view-result:none", "view-result:unsupported", 1) != mounted[i] {
			t.Errorf("line %d differs by more than the extension's result:\n  without the mount: %s\n  with the mount:    %s", i, plain[i], mounted[i])
		}
	}
	if differing != 1 {
		t.Errorf("%d lines differ, want only the extension's own result (none, unsupported)", differing)
	}
}

// TestRPCUIViewCarrierCommandsAnswerEveryClient pins the additive half of that promise: the carrier's seven names are
// commands whether or not the client attached, each reply carries the closed code beside Pi's error text, and an
// unknown member is invalid.
func TestRPCUIViewCarrierCommandsAnswerEveryClient(t *testing.T) {
	if testing.Short() {
		t.Skip("starts pig and a Node extension")
	}
	c := startRPCLines(t, listenTempDir(t), nil)
	for _, tc := range []struct{ line, want string }{
		{`{"id":"r","type":"ui_view_resync","attachment":1}`, `{"id":"r","type":"response","command":"ui_view_resync","success":false,"error":"stale: the connection does not hold attachment 1","code":"stale"}`},
		{`{"id":"d","type":"ui_view_detach","attachment":7}`, `{"id":"d","type":"response","command":"ui_view_detach","success":false,"error":"stale: the connection does not hold attachment 7","code":"stale"}`},
		{`{"id":"a","type":"ui_view_abilities","attachment":1,"abilities":` + rpcViewAbilities + `}`, `{"id":"a","type":"response","command":"ui_view_abilities","success":false,"error":"stale: the connection does not hold attachment 1","code":"stale"}`},
		{`{"id":"p","type":"ui_view_report","attachment":1,"plugin":"x","mount":{"key":"k","epoch":1},"notice":{"code":"undrawn","severity":"info"}}`, `{"id":"p","type":"response","command":"ui_view_report","success":false,"error":"stale: the connection does not hold attachment 1","code":"stale"}`},
		{`{"id":"i","type":"ui_view_intent","attachment":1,"plugin":"x","mount":{"key":"k","epoch":1,"rev":1},"intent":"go","arg":{}}`, `{"id":"i","type":"response","command":"ui_view_intent","success":true,"data":{"ticket":"","outcome":"stale","error":"attachment ended"}}`},
		{`{"id":"c","type":"ui_view_intent_cancel"}`, `{"id":"c","type":"response","command":"ui_view_intent_cancel","success":false,"error":"not_found: no intent in flight has this id","code":"not_found"}`},
		{`{"id":"v","type":"ui_view_attach","abilities":` + rpcViewAbilities + `,"version":1}`, `{"id":"v","type":"response","command":"ui_view_attach","success":false,"error":"invalid: unknown field \"version\"","code":"invalid"}`},
		{`{"id":"m","type":"ui_view_attach"}`, `{"id":"m","type":"response","command":"ui_view_attach","success":false,"error":"invalid: abilities is required","code":"invalid"}`},
		{`{"id":"ok","type":"ui_view_attach","abilities":` + rpcViewAbilities + `}`, `{"id":"ok","type":"response","command":"ui_view_attach","success":true,"data":{"attachment":1,"seq":0,"snapshots":[]}}`},
		{`{"id":"two","type":"ui_view_attach","abilities":` + rpcViewAbilities + `}`, `{"id":"two","type":"response","command":"ui_view_attach","success":false,"error":"limit: the connection already holds an attachment","code":"limit"}`},
	} {
		if line, _ := c.request(tc.line); line != tc.want {
			t.Errorf("%s answered\n  %s\nwant\n  %s", tc.line, line, tc.want)
		}
	}
	if lines := c.end(); slices.ContainsFunc(lines, func(line string) bool { return strings.Contains(line, `"type":"ui_view`) }) {
		t.Errorf("an attachment with no mount was written a carrier event:\n%s", strings.Join(lines, "\n"))
	}
}

// TestRPCUIViewCarrierOverBothTransports drives the carrier through the real rpc loop on the two transports pig serves
// rpc lines on: the process's stdin and stdout, and a --listen connection. An attached client is sent the extension's
// mounts as numbered events, its intent returns the extension's answer, a client that stops reading gets exactly one
// ui_view_gap and resyncs, a cancel ends an intent in flight, a detach makes the next intent stale, and after the
// session was replaced the same connection attaches again.
func TestRPCUIViewCarrierOverBothTransports(t *testing.T) {
	if testing.Short() {
		t.Skip("starts pig and a Node extension")
	}
	for _, transport := range []string{"stdio", "listen"} {
		t.Run(transport, func(t *testing.T) {
			dir := listenTempDir(t)
			done := filepath.Join(dir, "burst-done")
			env := []string{"PIG_TEST_VIEW_DONE=" + done}
			var c *rpcLines
			if transport == "stdio" {
				c = startRPCLines(t, dir, env)
			} else {
				fixture, err := filepath.Abs(filepath.Join("testdata", "rpc-ui-view.mjs"))
				if err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(dir, "pig.sock")
				s := startListenServer(t, append(listenEnv(t, dir), env...), "--mode", "rpc", "--listen", path, "--model", "test-faux/echo", "--no-session", "-e", fixture)
				s.path = path
				conn := s.dialConn()
				t.Cleanup(func() { _ = conn.Close() })
				c = newRPCLines(t, halfCloseConn{conn}, conn, s.errBuf)
			}
			event := func(what string, seq float64) map[string]any {
				t.Helper()
				_, record := c.until(what, func(record map[string]any) bool {
					if record["type"] == "ui_view_gap" {
						t.Fatalf("%s: a gap instead: %v", what, record)
					}
					return record["type"] == "ui_view"
				})
				if record["seq"] != seq {
					t.Fatalf("%s: seq %v, want %v", what, record["seq"], seq)
				}
				return record["snapshot"].(map[string]any)
			}
			data := func(record map[string]any) map[string]any {
				t.Helper()
				d, _ := record["data"].(map[string]any)
				if record["success"] != true || d == nil {
					t.Fatalf("reply = %v", record)
				}
				return d
			}

			if line, _ := c.request(`{"id":"at","type":"ui_view_attach","abilities":` + rpcViewAbilities + `}`); line != `{"id":"at","type":"response","command":"ui_view_attach","success":true,"data":{"attachment":1,"seq":0,"snapshots":[]}}` {
				t.Fatalf("attach = %s", line)
			}
			c.send(`{"id":"show","type":"prompt","message":"/view-show"}`)
			snap := event("the mount", 1)
			plugin, _ := snap["plugin"].(string)
			if snap["state"] != "live" || plugin == "" || len(snap["digest"].(string)) != 64 || !strings.Contains(fmt.Sprint(snap["view"]), "shown-0") {
				t.Fatalf("mount event = %v", snap)
			}
			c.response("show", "prompt")
			intent := func(id string, rev int, name, arg string) string {
				head := ""
				if id != "" {
					head = `"id":"` + id + `",`
				}
				return `{` + head + `"type":"ui_view_intent","attachment":1,"plugin":"` + plugin + `","mount":{"key":"show","epoch":1,"rev":` + strconv.Itoa(rev) + `},"intent":"` + name + `","arg":` + arg + `}`
			}
			// The answer adds the argument limit this client attached with, so it comes from the extension through the host.
			_, r := c.request(intent("add", 1, "fx.add", `{"n":5}`))
			if d := data(r); d["outcome"] != "done" || fmt.Sprint(d["answer"]) != "map[sum:105]" {
				t.Errorf("intent = %v", r)
			}

			// The client stops reading while the extension replaces the view 4000 times.
			c.gate.Lock()
			c.send(`{"id":"burst","type":"prompt","message":"/view-burst 4000"}`)
			deadline := time.Now().Add(c.budget)
			for {
				if _, err := os.Stat(done); err == nil {
					break
				}
				if time.Now().After(deadline) {
					c.gate.Unlock()
					t.Fatalf("the burst never ended\n%s", c.stderr.String())
				}
				time.Sleep(5 * time.Millisecond)
			}
			c.gate.Unlock()
			last := 1.0
			_, gap := c.until("the gap", func(record map[string]any) bool {
				if record["type"] == "ui_view" {
					if record["seq"] != last+1 {
						t.Fatalf("event %v follows %v", record["seq"], last)
					}
					last++
				}
				return record["type"] == "ui_view_gap"
			})
			if gap["attachment"] != 1.0 || gap["seq"] != last+1 || last < 257 || last >= 4001 {
				t.Fatalf("gap = %v after event %v", gap, last)
			}
			// The burst's response arrives, but the carrier promises no order between it and the gap: docs/ui-view-carrier.md
			// orders only "the events of an attachment" and the attach, abilities and resync replies ("Replies are not
			// numbered"). The rpc loop writes the response when the command returns, the pump writes the gap once the reader
			// resumes, so either line may come first.
			if !slices.ContainsFunc(c.seen, func(line string) bool {
				return strings.HasPrefix(line, `{"id":"burst","type":"response","command":"prompt","success":true`)
			}) {
				c.response("burst", "prompt")
			}
			// Nothing more for the attachment until it resyncs, whatever the extension does.
			before := len(c.seen)
			c.request(`{"id":"more","type":"prompt","message":"/view-burst 3"}`)
			c.request(`{"id":"state","type":"get_state"}`)
			for _, line := range c.seen[before:] {
				if strings.Contains(line, `"type":"ui_view`) {
					t.Fatalf("after the gap and before a resync the client was written %s", line)
				}
			}
			_, r = c.request(`{"id":"rs","type":"ui_view_resync","attachment":1}`)
			d := data(r)
			snaps, _ := d["snapshots"].([]any)
			if d["seq"] != gap["seq"] || len(snaps) != 1 || !strings.Contains(fmt.Sprint(snaps[0]), "shown-3") || snaps[0].(map[string]any)["mount"].(map[string]any)["rev"] != 4004.0 {
				t.Fatalf("resync = %.600v", r)
			}
			c.send(`{"id":"one","type":"prompt","message":"/view-burst 1"}`)
			if snap := event("the event after the resync", gap["seq"].(float64)+1); snap["mount"].(map[string]any)["rev"] != 4005.0 {
				t.Errorf("the event after the resync = %v", snap["mount"])
			}
			c.response("one", "prompt")

			// The transport's request cancel: the cancel names the intent request, and the intent replies cancelled.
			c.send(intent("wait", 4005, "fx.wait", `{"tag":"t1"}`))
			c.until("the extension's handler", func(record map[string]any) bool {
				return record["type"] == "extension_ui_request" && record["message"] == "intent-started:t1"
			})
			if line, _ := c.request(`{"id":"wait","type":"ui_view_intent_cancel"}`); line != `{"id":"wait","type":"response","command":"ui_view_intent_cancel","success":true,"data":{}}` {
				t.Errorf("cancel = %s", line)
			}
			_, r = c.response("wait", "ui_view_intent")
			if data(r)["outcome"] != "cancelled" {
				t.Errorf("the cancelled intent = %v", r)
			}
			// An intent without an id cannot be cancelled and still settles; its reply has no id.
			c.send(intent("", 4005, "fx.add", `{"n":1}`))
			_, r = c.until("the reply of the intent without an id", func(record map[string]any) bool {
				_, has := record["id"]
				return record["type"] == "response" && record["command"] == "ui_view_intent" && !has
			})
			if data(r)["outcome"] != "done" {
				t.Errorf("the intent without an id = %v", r)
			}

			// Detach: the next intent is stale.
			if line, _ := c.request(`{"id":"dt","type":"ui_view_detach","attachment":1}`); line != `{"id":"dt","type":"response","command":"ui_view_detach","success":true,"data":{}}` {
				t.Errorf("detach = %s", line)
			}
			_, r = c.request(intent("late", 4005, "fx.add", `{"n":1}`))
			if data(r)["outcome"] != "stale" {
				t.Errorf("the intent after the detach = %v", r)
			}

			// Attach, replace the session, attach: the attachment of the replaced session is stale, and the connection
			// attaches to the new session's registry.
			_, r = c.request(`{"id":"at2","type":"ui_view_attach","abilities":` + rpcViewAbilities + `}`)
			second := data(r)
			if second["attachment"] != 2.0 || len(second["snapshots"].([]any)) != 1 {
				t.Fatalf("second attach = %v", r)
			}
			if _, r := c.request(`{"id":"new","type":"new_session"}`); r["success"] != true {
				t.Fatalf("new_session = %v", r)
			}
			if _, r := c.request(`{"id":"rs2","type":"ui_view_resync","attachment":2}`); r["code"] != "stale" {
				t.Errorf("resync under the replaced session = %v", r)
			}
			_, r = c.request(`{"id":"at3","type":"ui_view_attach","abilities":` + rpcViewAbilities + `}`)
			third := data(r)
			if len(third["snapshots"].([]any)) != 0 {
				t.Errorf("attach after the replacement = %v", r)
			}
			c.send(`{"id":"show2","type":"prompt","message":"/view-show"}`)
			_, record := c.until("the new session's mount", func(record map[string]any) bool {
				return record["type"] == "ui_view" && record["attachment"] == third["attachment"]
			})
			if snap := record["snapshot"].(map[string]any); record["seq"] != third["seq"].(float64)+1 || snap["state"] != "live" {
				t.Errorf("the new session's mount = %v", record)
			}
			c.response("show2", "prompt")
			c.end()
		})
	}
}

// passGate returns once the gate is free: the reader goroutine stops here while the test holds the gate.
func (c *rpcLines) passGate() {
	c.gate.Lock()
	defer c.gate.Unlock()
}
