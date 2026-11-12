package uiviewweb

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/browsertest"
)

var pigBinary struct {
	once sync.Once
	path string
	err  error
}

// buildPig builds the stock pig the bridge starts.
func buildPig(t *testing.T) string {
	t.Helper()
	pigBinary.once.Do(func() {
		dir, err := os.MkdirTemp("", "ui-view-web-pig")
		if err != nil {
			pigBinary.err = err
			return
		}
		pigBinary.path = filepath.Join(dir, "pig")
		cmd := exec.Command("go", "build", "-o", pigBinary.path, "github.com/MichaelKinsy/PiG/cmd/pig")
		cmd.Dir = filepath.Join("..", "..")
		if out, err := cmd.CombinedOutput(); err != nil {
			pigBinary.err = fmt.Errorf("go build ./cmd/pig: %v\n%s", err, out)
		}
	})
	if pigBinary.err != nil {
		t.Fatal(pigBinary.err)
	}
	return pigBinary.path
}

type bridge struct {
	base, token string
}

// startBridge runs bridge.mjs over a pig with the demo extension and the faux model, and returns the page's address.
func startBridge(t *testing.T) bridge {
	t.Helper()
	if testing.Short() {
		t.Skip("starts Node and pig")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	home := t.TempDir()
	cmd := exec.Command(node, "bridge.mjs", "--port", "0", "--pig", buildPig(t), "--", "--model", "test-faux/echo", "--no-session", "-e", "demo-extension.mjs")
	cmd.Env = append(os.Environ(), "PIG_TEST_FAUX=1", "PIG_HOME="+filepath.Join(home, "home"), "PI_HOME="+filepath.Join(home, "pihome"),
		"PIG_CODING_AGENT_DIR="+filepath.Join(home, "agent"), "PI_CODING_AGENT_DIR="+filepath.Join(home, "piagent"))
	cmd.Stderr = os.Stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Signal(os.Interrupt)
		_ = cmd.Wait()
	})
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		t.Fatalf("the bridge printed no address: %v", err)
	}
	address := strings.TrimSpace(strings.TrimPrefix(line, "ui-view-web: "))
	base, token, ok := strings.Cut(address, "/?token=")
	if !ok || !strings.HasPrefix(base, "http://127.0.0.1:") {
		t.Fatalf("the bridge printed %q", line)
	}
	return bridge{base: base, token: token}
}

type page struct {
	t       *testing.T
	b       bridge
	session string
	lines   chan string
	close   context.CancelFunc
}

// open connects as a page does: it reads the event stream and learns its session.
func (b bridge) open(t *testing.T) *page {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, b.base+"/events?token="+b.token, nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK || res.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("GET /events = %s %s", res.Status, res.Header.Get("Content-Type"))
	}
	p := &page{t: t, b: b, lines: make(chan string, 4096), close: cancel}
	session := make(chan string, 1)
	go func() {
		defer close(p.lines)
		defer func() { _ = res.Body.Close() }()
		reader := bufio.NewReaderSize(res.Body, 1<<16)
		event := "message"
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimSuffix(line, "\n")
			switch {
			case strings.HasPrefix(line, "event: "):
				event = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: ") && event == "session":
				session <- strings.TrimPrefix(line, "data: ")
			case strings.HasPrefix(line, "data: ") && event == "message":
				p.lines <- strings.TrimPrefix(line, "data: ")
			case line == "":
				event = "message"
			}
		}
	}()
	select {
	case p.session = <-session:
	case <-time.After(30 * time.Second):
		t.Fatal("the bridge sent no session")
	}
	return p
}

func (p *page) post(line string) int {
	p.t.Helper()
	res, err := http.Post(p.b.base+"/send?token="+p.b.token+"&session="+p.session, "text/plain", strings.NewReader(line))
	if err != nil {
		p.t.Fatal(err)
	}
	_ = res.Body.Close()
	return res.StatusCode
}

// until reads relayed lines until done reports true.
func (p *page) until(what string, done func(record map[string]any) bool) map[string]any {
	p.t.Helper()
	timer := time.NewTimer(60 * time.Second)
	defer timer.Stop()
	for {
		select {
		case line, ok := <-p.lines:
			if !ok {
				p.t.Fatalf("the stream ended before %s", what)
			}
			var record map[string]any
			if err := json.Unmarshal([]byte(line), &record); err != nil {
				p.t.Fatalf("relayed line %.200s: %v", line, err)
			}
			if done(record) {
				return record
			}
		case <-timer.C:
			p.t.Fatalf("%s timed out", what)
		}
	}
}

func (p *page) request(line string) map[string]any {
	p.t.Helper()
	var head struct{ ID, Type string }
	if err := json.Unmarshal([]byte(line), &head); err != nil {
		p.t.Fatal(err)
	}
	if status := p.post(line); status != http.StatusNoContent {
		p.t.Fatalf("POST %s = %d", head.Type, status)
	}
	return p.until("the "+head.Type+" reply", func(record map[string]any) bool {
		return record["type"] == "response" && record["id"] == head.ID && record["command"] == head.Type
	})
}

const testAbilities = `{"slots":["panel"],"nodes":["vstack","text","select-list","input","mouse-region","settings-list","editor","scroll-view"],"markdown":false,"imageFormats":[],"limits":{"nodes":256,"depth":8,"snapshotBytes":65536,"textBytes":4096,"mounts":4,"intentArgBytes":1024},"input":{"keyboard":true,"pointer":true,"text":true}}`

func get(t *testing.T, url string, header ...string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	return res.StatusCode, string(body)
}

// TestBridgeRelaysTheCarrier drives the bridge the way the page does, with an HTTP client: the page and its script are
// served, a request without the token or from another origin is refused, a connected page gets its own pig, the
// carrier's lines pass both ways unchanged (attach, a mount with every node kind that carries an id, an intent and its
// answer, the update that follows), and closing the stream ends that pig.
func TestBridgeRelaysTheCarrier(t *testing.T) {
	b := startBridge(t)
	if status, body := get(t, b.base+"/"); status != 200 || !strings.Contains(body, `src="client.js"`) {
		t.Fatalf("GET / = %d", status)
	}
	if status, body := get(t, b.base+"/client.js"); status != 200 || !strings.Contains(body, "ui_view_attach") {
		t.Fatalf("GET /client.js = %d", status)
	}
	if status, _ := get(t, b.base+"/events"); status != 403 {
		t.Errorf("GET /events without the token = %d, want 403", status)
	}
	if status, _ := get(t, b.base+"/events?token="+b.token, "Origin", "http://example.com"); status != 403 {
		t.Errorf("GET /events from another origin = %d, want 403", status)
	}
	if status, _ := get(t, b.base+"/bridge.mjs"); status != 403 {
		t.Errorf("GET /bridge.mjs = %d, want 403: the bridge serves the page and its script only", status)
	}

	p := b.open(t)
	attached := p.request(`{"id":"a","type":"ui_view_attach","abilities":` + testAbilities + `}`)
	data, _ := attached["data"].(map[string]any)
	if attached["success"] != true || data["attachment"] != 1.0 {
		t.Fatalf("attach = %v", attached)
	}
	if status := p.post(`{"id":"p","type":"prompt","message":"/ui-demo"}`); status != http.StatusNoContent {
		t.Fatalf("POST prompt = %d", status)
	}
	mounted := p.until("the demo's mount", func(record map[string]any) bool {
		if message, _ := record["message"].(string); strings.HasPrefix(message, "ui-demo:") {
			t.Fatalf("the demo extension reported %s", message)
		}
		return record["type"] == "ui_view"
	})
	snapshot := mounted["snapshot"].(map[string]any)
	view, _ := json.Marshal(snapshot["view"])
	for _, id := range []string{"colors", "name", "tap", "prefs", "notes", "log"} {
		if !strings.Contains(string(view), `"id":"`+id+`"`) {
			t.Errorf("the mount lacks the node %s: %s", id, view)
		}
	}
	if mounted["seq"] != 1.0 || len(snapshot["a11y"].([]any)) != 6 || len(snapshot["focus"].([]any)) != 6 || len(snapshot["intents"].([]any)) != 6 {
		t.Fatalf("mount event = %v", mounted)
	}
	plugin := snapshot["plugin"].(string)
	intent := func(id, name, nodeKey, arg string, rev int) map[string]any {
		return p.request(fmt.Sprintf(`{"id":%q,"type":"ui_view_intent","attachment":1,"plugin":%q,"mount":{"key":"demo","epoch":1,"rev":%d},"nodeKey":%q,"intent":%q,"arg":%s}`, id, plugin, rev, nodeKey, name, arg))
	}
	if r := intent("i1", "demo.pick", "colors", `{"value":"green"}`, 1); r["data"].(map[string]any)["outcome"] != "done" {
		t.Fatalf("demo.pick = %v", r)
	}
	updated := p.until("the update after the intent", func(record map[string]any) bool { return record["type"] == "ui_view" })
	if view, _ := json.Marshal(updated["snapshot"].(map[string]any)["view"]); updated["seq"] != 2.0 || !strings.Contains(string(view), "green, 0 taps") {
		t.Fatalf("update event = %s", view)
	}
	if r := intent("i2", "demo.tap", "tap", `{}`, 2); fmt.Sprint(r["data"].(map[string]any)["answer"]) != "map[taps:1]" {
		t.Fatalf("demo.tap = %v", r)
	}
	if status := p.post("{}\n{}"); status != http.StatusBadRequest {
		t.Errorf("POST of two lines = %d, want 400", status)
	}

	// A second page gets its own pig, so its own session and attachment 1.
	q := b.open(t)
	if q.session == p.session {
		t.Fatal("two pages share a session")
	}
	if r := q.request(`{"id":"a","type":"ui_view_attach","abilities":` + testAbilities + `}`); r["data"].(map[string]any)["attachment"] != 1.0 || len(r["data"].(map[string]any)["snapshots"].([]any)) != 0 {
		t.Fatalf("the second page's attach = %v", r)
	}
	// Closing the stream ends the page's pig: its session is gone.
	p.close()
	deadline := time.Now().Add(30 * time.Second)
	for p.post(`{"id":"x","type":"get_state"}`) != http.StatusNotFound {
		if time.Now().After(deadline) {
			t.Fatal("the session outlived its page")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if r := q.request(`{"id":"s","type":"get_state"}`); r["success"] != true {
		t.Errorf("the other page's pig = %v", r)
	}
}

// TestPageDrawsFocusesAndRaisesIntents opens the page in headless Chromium (skipped where there is none): it attaches,
// draws the demo panel, makes every node kind that carries an id an element under that id with the a11y name and the
// focus order the snapshot gives, moves the focus to the node that requests it, raises intents from the keyboard and
// the pointer, keeps the focus across the redraw, and gives the focus back when the mount ends.
func TestPageDrawsFocusesAndRaisesIntents(t *testing.T) {
	b := startBridge(t)
	browser := browsertest.Start(t)
	browser.Open(b.base + "/?token=" + b.token)
	browser.Wait(`window.uiView && window.uiView.attachment === 1`)
	browser.Run(`(() => { const form = document.getElementById("prompt"); form.elements.message.focus(); form.elements.message.value = "/ui-demo"; form.requestSubmit(); })()`)
	browser.Wait(`document.querySelector('[data-key="demo"]') !== null`)
	got := browser.String(`JSON.stringify(["colors","name","tap","prefs","notes","log"].map((id) => { const el = document.querySelector('[id$="-' + id + '"]'); return [el?.tagName, el?.tabIndex, el?.getAttribute("aria-label"), el?.getAttribute("role")].join("/"); }))`)
	if want := `["DIV/1/Color/list","INPUT/2/Name/","DIV/3/Tap counter/button","DIV/4/Preferences/listbox","TEXTAREA/5/Notes/","DIV/6/Log/region"]`; got != want {
		t.Errorf("the id-carrying nodes are drawn as\n  %s\nwant\n  %s", got, want)
	}
	browser.Wait(`document.activeElement.id.endsWith("-colors")`)
	// Every one of them takes the focus.
	if got := browser.String(`["name","tap","prefs","notes","log","colors"].map((id) => { const el = document.querySelector('[id$="-' + id + '"]'); el.focus(); return String(document.activeElement === el); }).join()`); got != "true,true,true,true,true,true" {
		t.Errorf("focus = %s", got)
	}
	key := func(k string) string {
		return `document.activeElement.dispatchEvent(new KeyboardEvent("keydown", { key: "` + k + `", bubbles: true }))`
	}
	// Keyboard: the next color, Enter raises demo.pick; the panel is redrawn with the answer's effect and keeps the focus.
	browser.Run(key("ArrowDown"))
	browser.Run(key("Enter"))
	browser.Wait(`document.querySelector('[data-key="demo"] .text').textContent.includes("green") && document.querySelector('[data-key="demo"] footer').dataset.outcome === "done"`)
	browser.Wait(`document.activeElement.id.endsWith("-colors")`)
	// Pointer: a click on the mouse-region raises demo.tap and shows its answer.
	browser.Run(`document.querySelector('[id$="-tap"]').click()`)
	browser.Wait(`document.querySelector('[data-key="demo"] footer').textContent.includes('{"taps":1}')`)
	// Text: typing a name and Enter raises demo.rename.
	browser.Run(`document.querySelector('[id$="-name"]').focus()`)
	browser.Type("Ada")
	browser.Run(key("Enter"))
	browser.Wait(`document.querySelector('[data-key="demo"] .text').textContent.startsWith("Hello Ada")`)
	// The mount ends: the panel goes and the focus returns to where it was before the mount.
	browser.Run(`[...document.querySelectorAll('[data-key="demo"] header button')].find((b) => b.textContent === "demo.close").click()`)
	browser.Wait(`document.querySelector('[data-key="demo"]') === null && document.activeElement.name === "message"`)
	if got := browser.String(`String(window.uiView.reports) + "/" + String(window.uiView.resyncs)`); got != "0/0" {
		t.Errorf("reports/resyncs = %s, want 0/0", got)
	}
}
