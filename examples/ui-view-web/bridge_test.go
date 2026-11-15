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

// TestMain removes the directory of the pig the tests built. Every bridge has ended by then: its cleanup waits for the pigs it started.
func TestMain(m *testing.M) {
	code := m.Run()
	if pigBinary.path != "" {
		_ = os.RemoveAll(filepath.Dir(pigBinary.path))
	}
	os.Exit(code)
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
			pigBinary.err = fmt.Errorf("go build ./cmd/pig: %w\n%s", err, out)
		}
	})
	if pigBinary.err != nil {
		t.Fatal(pigBinary.err)
	}
	return pigBinary.path
}

// bridge is a running bridge: the page's base address, the page open.html that sends a browser to it (the file `address`
// is beside it) and the session secret its tests share (auth).
type bridge struct {
	base, openFile string
	signIn         *signIn
}

// startBridge runs bridge.mjs over a pig with the demo extension and the faux model, and returns the page's address.
// More arguments go to pig.
func startBridge(t *testing.T, more ...string) bridge {
	t.Helper()
	node := nodeBinary(t)
	home := t.TempDir()
	env := []string{"PIG_TEST_FAUX=1", "PIG_HOME=" + filepath.Join(home, "home"), "PI_HOME=" + filepath.Join(home, "pihome"),
		"PIG_CODING_AGENT_DIR=" + filepath.Join(home, "agent"), "PI_CODING_AGENT_DIR=" + filepath.Join(home, "piagent")}
	return runBridge(t, node, env, append([]string{"--pig", buildPig(t), "--", "--model", "test-faux/echo", "--no-session", "-e", "demo-extension.mjs"}, more...)...)
}

// nodeBinary returns the Node that runs the bridge. The test is skipped in short mode and where there is none.
func nodeBinary(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("starts Node")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		// Where the page tests are required (CI sets CHORD_REQUIRE_CHROMIUM, as for the chord web tests), a missing Node
		// fails them as a missing browser does (browsertest.Start) rather than skipping them.
		if os.Getenv("CHORD_REQUIRE_CHROMIUM") != "" {
			t.Fatal("node is not installed, and CHORD_REQUIRE_CHROMIUM is set")
		}
		t.Skip("node is not installed")
	}
	return node
}

// runBridge runs bridge.mjs with args on any free port and returns the page's address. It reads the address from the
// file the bridge's first line names, as an author following the README does.
func runBridge(t *testing.T, node string, env []string, args ...string) bridge {
	t.Helper()
	cmd := exec.Command(node, append([]string{"bridge.mjs", "--port", "0"}, args...)...)
	cmd.Env = append(os.Environ(), env...)
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
	return bridgeFrom(t, line)
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
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, b.base+"/events", nil)
	req.Header.Set("Authorization", b.auth(t))
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = res.Body.Close() })
	if res.StatusCode != http.StatusOK || res.Header.Get("Content-Type") != "text/event-stream" {
		_ = res.Body.Close()
		t.Fatalf("GET /events = %s %s", res.Status, res.Header.Get("Content-Type"))
	}
	p := &page{t: t, b: b, lines: make(chan string, 4096), close: cancel}
	session := make(chan string, 1)
	go p.relay(res, session)
	select {
	case p.session = <-session:
	case <-time.After(30 * time.Second):
		t.Fatal("the bridge sent no session")
	}
	return p
}

// relay reads the event stream of a page until it ends: the session it names and the lines pig wrote.
func (p *page) relay(res *http.Response, session chan<- string) {
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
}

func (p *page) post(line string) int {
	p.t.Helper()
	req, _ := http.NewRequest(http.MethodPost, p.b.base+"/send?session="+p.session, strings.NewReader(line))
	req.Header.Set("Content-Type", "text/plain")
	req.Header.Set("Authorization", p.b.auth(p.t))
	res, err := http.DefaultClient.Do(req)
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
// answer, with the update its handler made written before the answer), and closing the stream ends that pig.
func TestBridgeRelaysTheCarrier(t *testing.T) {
	b := startBridge(t)
	if status, body := get(t, b.base+"/"); status != 200 || !strings.Contains(body, `src="client.js"`) {
		t.Fatalf("GET / = %d", status)
	}
	if status, body := get(t, b.base+"/client.js"); status != 200 || !strings.Contains(body, "ui_view_attach") {
		t.Fatalf("GET /client.js = %d", status)
	}
	if status, _ := get(t, b.base+"/events"); status != 403 {
		t.Errorf("GET /events without the secret = %d, want 403", status)
	}
	if status, _ := get(t, b.base+"/events", "Authorization", b.auth(t), "Origin", "http://example.com"); status != 403 {
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
	// intent raises one intent and returns its reply and the ui_view events the page was written before the reply.
	intent := func(id, name, nodeKey, arg string, rev int) (reply map[string]any, before []map[string]any) {
		if status := p.post(fmt.Sprintf(`{"id":%q,"type":"ui_view_intent","attachment":1,"plugin":%q,"mount":{"key":"demo","epoch":1,"rev":%d},"nodeKey":%q,"intent":%q,"arg":%s}`, id, plugin, rev, nodeKey, name, arg)); status != http.StatusNoContent {
			t.Fatalf("POST %s = %d", name, status)
		}
		reply = p.until("the "+name+" reply", func(record map[string]any) bool {
			if record["type"] == "ui_view" {
				before = append(before, record)
			}
			return record["type"] == "response" && record["id"] == id && record["command"] == "ui_view_intent"
		})
		return reply, before
	}
	// The demo's handler redraws its mount and then answers: the intent is done, not stale, and the page holds the new
	// view when it reads the reply.
	r, before := intent("i1", "demo.pick", "colors", `{"value":"green"}`, 1)
	if r["data"].(map[string]any)["outcome"] != "done" {
		t.Fatalf("demo.pick = %v", r)
	}
	if len(before) != 1 {
		t.Fatalf("the page was written %d ui_view events before the reply of demo.pick, want the redrawn view: %v", len(before), before)
	}
	if view, _ := json.Marshal(before[0]["snapshot"].(map[string]any)["view"]); before[0]["seq"] != 2.0 || !strings.Contains(string(view), "green, 0 taps") {
		t.Fatalf("update event = %s", view)
	}
	if r, _ := intent("i2", "demo.tap", "tap", `{}`, 2); fmt.Sprint(r["data"].(map[string]any)["answer"]) != "map[taps:1]" {
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
	b.openIn(t, browser, "")
	browser.Wait(`window.uiView && window.uiView.attachment === 1`)
	browser.Run(`(() => { const form = document.getElementById("prompt"); form.elements.message.focus(); form.elements.message.value = "/ui-demo"; form.requestSubmit(); })()`)
	browser.Wait(`document.querySelector('[data-key="demo"]') !== null`)
	got := browser.String(`JSON.stringify(["colors","name","tap","prefs","notes","log"].map((id) => { const el = document.querySelector('[id$="-' + id + '"]'); return [el?.tagName, el?.tabIndex, el?.getAttribute("aria-label"), el?.getAttribute("role")].join("/"); }))`)
	// A select-list is a listbox whatever role its a11y record names (its items are options), and a mouse-region with
	// the role button is a button element, which needs no role attribute.
	if want := `["DIV/1/Color/listbox","INPUT/2/Name/","BUTTON/3/Tap counter/","DIV/4/Preferences/listbox","TEXTAREA/5/Notes/","DIV/6/Log/region"]`; got != want {
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
	// The mount's own intent is a button that reads the intent's verb and carries its name.
	if got := browser.String(`(() => { const b = document.querySelector('[data-key="demo"] header button[data-intent="demo.close"]'); return [b.textContent, b.title].join(); })()`); got != "Close,demo.close" {
		t.Errorf("the button of demo.close reads and is titled %s, want Close,demo.close", got)
	}
	browser.Run(`document.querySelector('[data-key="demo"] header button[data-intent="demo.close"]').click()`)
	browser.Wait(`document.querySelector('[data-key="demo"]') === null && document.activeElement.name === "message"`)
	if got := browser.String(`String(window.uiView.reports) + "/" + String(window.uiView.resyncs)`); got != "0/0" {
		t.Errorf("reports/resyncs = %s, want 0/0", got)
	}
}

// openFixtures opens the page over a pig with the two fixture extensions (testdata/fixture.mjs) and returns the browser
// and a function that sends a line as the user does: `/fx-a mount <content>`, `/fx-b update <content>`.
func openFixtures(t *testing.T) (*browsertest.Browser, func(line string)) {
	t.Helper()
	return openWith(t, "testdata/fixture-a.mjs", "testdata/fixture-b.mjs")
}

// openWith opens the page of a bridge whose session loads the named extensions, and returns the browser and a function
// that sends a line through the page's prompt.
func openWith(t *testing.T, extensions ...string) (*browsertest.Browser, func(line string)) {
	t.Helper()
	var load []string
	for _, extension := range extensions {
		load = append(load, "-e", extension)
	}
	b := startBridge(t, load...)
	browser := browsertest.Start(t)
	b.openIn(t, browser, "")
	browser.Wait(`window.uiView && window.uiView.attachment === 1`)
	return browser, func(line string) {
		t.Helper()
		text, err := json.Marshal(line)
		if err != nil {
			t.Fatal(err)
		}
		browser.Run(`(() => { const form = document.getElementById("prompt"); form.elements.message.value = ` + string(text) + `; form.requestSubmit(); })()`)
	}
}

// TestPageLaysOutStacks checks the geometry the page computes for the layout members of a view: an hstack's children
// with `stack {basis: 0, grow: 1}` are columns of one width side by side whatever their text, `gap` is cells between
// children, `align` places a shorter child, a `basis` is a width in cells, and the padding of a box and of a text is
// what the view says (a text's default is one cell each way). A cell is the page's spacing tokens: 8 px wide and 16 px
// high in the default look.
func TestPageLaysOutStacks(t *testing.T) {
	browser, send := openFixtures(t)
	send(`/fx-a mount {"view":{"root":{"kind":"vstack","gap":1,"children":[` +
		`{"kind":"hstack","gap":2,"children":[` +
		`{"kind":"text","text":"a","paddingX":0,"paddingY":0,"stack":{"basis":0,"grow":1}},` +
		`{"kind":"text","text":"a much longer cell than its neighbours","paddingX":0,"paddingY":0,"stack":{"basis":0,"grow":1}},` +
		`{"kind":"text","text":"c","paddingX":0,"paddingY":0,"stack":{"basis":0,"grow":1}}]},` +
		`{"kind":"hstack","align":"end","children":[{"kind":"text","text":"one","paddingX":0,"paddingY":0},{"kind":"text","text":"one\ntwo\nthree","paddingX":0,"paddingY":0}]},` +
		`{"kind":"box","paddingX":3,"paddingY":2,"children":[{"kind":"text","text":"boxed"}]},` +
		`{"kind":"hstack","children":[{"kind":"text","text":"x","paddingX":0,"paddingY":0,"stack":{"basis":10,"shrink":0}},{"kind":"text","text":"y","paddingX":0,"paddingY":0,"stack":{"grow":1}}]}` +
		`]}}}`)
	browser.Wait(`document.querySelector('[data-key="fx-a"] .box') !== null`)
	got := browser.String(`(() => {
		const view = document.querySelector('[data-key="fx-a"] .vstack');
		const probe = document.createElement("div");
		probe.style.cssText = "width: calc(10 * var(--col)); height: calc(10 * var(--row)); flex: none";
		view.append(probe);
		const col = probe.getBoundingClientRect().width / 10, row = probe.getBoundingClientRect().height / 10;
		probe.remove();
		const rect = (el) => el.getBoundingClientRect();
		const near = (a, b) => Math.abs(a - b) < 0.75;
		const [cells, ends, box, fixed] = view.children;
		const c = [...cells.children].map(rect), e = [...ends.children].map(rect);
		const boxStyle = getComputedStyle(box), textStyle = getComputedStyle(box.firstChild);
		return JSON.stringify({
			cell: col + "x" + row,
			equalColumns: near(c[0].width, c[1].width) && near(c[1].width, c[2].width) && c[0].width > col,
			sameRow: near(c[0].top, c[1].top) && near(c[1].top, c[2].top) && c[0].left < c[1].left && c[1].left < c[2].left,
			columnGap: near(c[1].left - c[0].right, 2 * col) && near(c[2].left - c[1].right, 2 * col),
			rowGap: near(rect(ends).top - rect(cells).bottom, row),
			alignEnd: near(e[0].bottom, e[1].bottom) && e[0].top > e[1].top + row,
			boxPadding: near(parseFloat(boxStyle.paddingLeft), 3 * col) && near(parseFloat(boxStyle.paddingTop), 2 * row),
			textPadding: near(parseFloat(textStyle.paddingLeft), col) && near(parseFloat(textStyle.paddingTop), row),
			basis: near(rect(fixed.children[0]).width, 10 * col) && near(rect(fixed.children[1]).right, rect(fixed).right),
		});
	})()`)
	if want := `{"cell":"8x16","equalColumns":true,"sameRow":true,"columnGap":true,"rowGap":true,"alignEnd":true,"boxPadding":true,"textPadding":true,"basis":true}`; got != want {
		t.Errorf("the layout is\n  %s\nwant\n  %s", got, want)
	}
}

// TestPageDrawsColoursThemeAndMarkdown checks the computed colours and the markdown the page draws: a `bg` and a
// `color` token take the colour the view's theme gives the token (a theme belongs to its view: the same token in
// another mount keeps the page's colour), a markdown node is drawn as headings, lists, emphasis and links with its
// default text style, and nothing in its text becomes markup: a script tag, an element with an on* attribute and a
// javascript link are drawn as the characters they are.
func TestPageDrawsColoursThemeAndMarkdown(t *testing.T) {
	browser, send := openFixtures(t)
	root := `"root":{"kind":"vstack","children":[` +
		`{"kind":"box","bg":"selectedBg","children":[{"kind":"text","text":"chip","bg":"customMessageBg"}]},` +
		`{"kind":"dynamic-border","color":"accent"},` +
		`{"kind":"markdown","defaultTextStyle":{"color":"accent","bold":true},"text":"# Title\n\nSome **bold** and a [link](https://example.com/).\n\n- one\n- two\n\n` +
		`<script>window.pwned = 1</script>\n\n<img src=x onerror=\"window.pwned = 2\">\n\n[bad](javascript:window.pwned=3)"}]}`
	send(`/fx-a mount {"view":{"theme":{"selectedBg":"#102030","accent":"#123456"},` + root + `}}`)
	send(`/fx-b mount {"view":{` + root + `}}`)
	browser.Wait(`document.querySelector('[data-key="fx-a"] .box') !== null && document.querySelector('[data-key="fx-b"] .box') !== null`)
	got := browser.String(`(() => {
		const a = document.querySelector('[data-key="fx-a"] .view'), b = document.querySelector('[data-key="fx-b"] .view');
		const style = (root, selector) => getComputedStyle(root.querySelector(selector));
		const md = a.querySelector(".markdown");
		const all = [...a.querySelectorAll("*")];
		return JSON.stringify({
			themedBg: style(a, ".box").backgroundColor,
			otherMountBg: style(b, ".box").backgroundColor !== "rgb(16, 32, 48)" && style(b, ".box").backgroundColor !== "rgba(0, 0, 0, 0)",
			textBg: style(a, ".box .text").backgroundColor !== "rgba(0, 0, 0, 0)",
			rule: style(a, ".rule").borderTopColor,
			markdownStyle: md ? getComputedStyle(md).color + " " + getComputedStyle(md).fontWeight : "",
			markdown: [md?.querySelector("h1")?.textContent, md?.querySelector("strong")?.textContent, md?.querySelectorAll("li").length, md?.querySelector("a")?.href].join("|"),
			inert: all.every((el) => !["SCRIPT", "IMG"].includes(el.tagName) && [...el.attributes].every((at) => !at.name.startsWith("on"))) &&
				a.querySelectorAll("a").length === 1 && window.pwned === undefined,
			asText: a.textContent.includes("<script>window.pwned = 1</script>") && a.textContent.includes('<img src=x onerror="window.pwned = 2">') && a.textContent.includes("[bad](javascript:window.pwned=3)"),
		});
	})()`)
	if want := `{"themedBg":"rgb(16, 32, 48)","otherMountBg":true,"textBg":true,"rule":"rgb(18, 52, 86)","markdownStyle":"rgb(18, 52, 86) 700","markdown":"Title|bold|2|https://example.com/","inert":true,"asText":true}`; got != want {
		t.Errorf("colours and markdown are\n  %s\nwant\n  %s", got, want)
	}
}

// TestPageKeepsTypedTextAcrossUpdates checks what an update does to what the user is typing: a text node whose id is
// in the next view keeps its element, text, selection and the focus while a sibling changes; a value the extension
// changes replaces the text; a node that moved to another parent keeps its text and the focus; and when the node is
// gone, the node with another id that stands in its place starts with its own value.
func TestPageKeepsTypedTextAcrossUpdates(t *testing.T) {
	browser, send := openFixtures(t)
	view := func(mail, input string) string {
		return `{"view":{"root":{"kind":"vstack","children":[{"kind":"text","text":"` + mail + `"},` + input + `,{"kind":"editor","id":"body","value":""}]}}}`
	}
	reply := `{"kind":"input","id":"reply","value":""}`
	state := `(() => { const el = document.querySelector('[id$="-reply"]'), body = document.querySelector('[id$="-body"]');
		return JSON.stringify({ same: el === window.typedInto, value: el?.value, selection: el?.selectionStart + "-" + el?.selectionEnd, focused: document.activeElement === el, body: body?.value, sameBody: body === window.bodyWas }); })()`
	send(`/fx-a mount ` + view("1 mail", reply))
	browser.Wait(`document.querySelector('[id$="-reply"]') !== null`)
	browser.Run(`(window.bodyWas = document.querySelector('[id$="-body"]')).focus()`)
	browser.Type("a draft")
	browser.Run(`(window.typedInto = document.querySelector('[id$="-reply"]')).focus()`)
	browser.Type("half typed")
	browser.Run(`window.typedInto.setSelectionRange(2, 5)`)
	// A sibling changes: a new mail arrives.
	send(`/fx-a update ` + view("2 mails", reply))
	browser.Wait(`document.querySelector('[data-key="fx-a"] .text').textContent === "2 mails"`)
	if got, want := browser.String(state), `{"same":true,"value":"half typed","selection":"2-5","focused":true,"body":"a draft","sameBody":true}`; got != want {
		t.Fatalf("after a sibling changed, the typed text is\n  %s\nwant\n  %s", got, want)
	}
	// The extension changes the value: its value is drawn.
	send(`/fx-a update ` + view("3 mails", `{"kind":"input","id":"reply","value":"sent"}`))
	browser.Wait(`document.querySelector('[data-key="fx-a"] .text').textContent === "3 mails"`)
	if got, want := browser.String(state), `{"same":true,"value":"sent","selection":"2-4","focused":true,"body":"a draft","sameBody":true}`; got != want {
		t.Errorf("after the extension changed the value, the text is\n  %s\nwant\n  %s", got, want)
	}
	// The node moves into a box: it is another element, with the text and the focus of the one it replaces.
	browser.Run(`window.typedInto.setSelectionRange(4, 4)`)
	browser.Type(" again")
	send(`/fx-a update ` + view("4 mails", `{"kind":"box","children":[{"kind":"input","id":"reply","value":"sent"}]}`))
	browser.Wait(`document.querySelector('[data-key="fx-a"] .text').textContent === "4 mails"`)
	if got, want := browser.String(state), `{"same":false,"value":"sent again","selection":"10-10","focused":true,"body":"a draft","sameBody":true}`; got != want {
		t.Errorf("after the node moved, the text is\n  %s\nwant\n  %s", got, want)
	}
	// The node is gone and one with another id and the same value stands in its place: it has its own value, not the
	// text typed into the old one.
	send(`/fx-a update ` + view("5 mails", `{"kind":"box","children":[{"kind":"input","id":"other","value":"sent"}]}`))
	browser.Wait(`document.querySelector('[data-key="fx-a"] .text').textContent === "5 mails"`)
	if got, want := browser.String(`(() => { const other = document.querySelector('[id$="-other"]'); return [document.querySelector('[id$="-reply"]') === null, JSON.stringify(other.value), other === window.typedInto, document.activeElement === other].join(); })()`), `true,"sent",false,false`; got != want {
		t.Errorf("after the node was removed: gone, the new node's value, same element, focused = %s, want %s", got, want)
	}
}

// TestPageShowsPanelsAsTabsAndSideBySide checks the panels of two extensions: each has a tab, both are drawn side by
// side until the user asks for tabs, then only the chosen one is shown; an update reaches the panel that is not shown
// and does not change the choice; choosing its tab shows it, with the update.
func TestPageShowsPanelsAsTabsAndSideBySide(t *testing.T) {
	browser, send := openFixtures(t)
	view := func(text string) string {
		return `{"view":{"root":{"kind":"text","text":"` + text + `"}}}`
	}
	send(`/fx-a mount ` + view("trough 1"))
	send(`/fx-b mount ` + view("pen 1"))
	browser.Wait(`document.querySelector('[data-key="fx-a"]') !== null && document.querySelector('[data-key="fx-b"]') !== null`)
	state := `(() => {
		const a = document.querySelector('[data-key="fx-a"]'), b = document.querySelector('[data-key="fx-b"]');
		const ra = a.getBoundingClientRect(), rb = b.getBoundingClientRect();
		const tabs = [...document.querySelectorAll('[role=tab]')];
		return JSON.stringify({
			tabs: tabs.map((tab) => tab.textContent + (tab.getAttribute("aria-selected") === "true" ? "*" : "")).join(),
			shown: [a, b].map((el) => el.getClientRects().length > 0).join(),
			beside: ra.width > 0 && rb.width > 0 && Math.abs(ra.top - rb.top) < 1 && ra.right <= rb.left,
			text: [a, b].map((el) => el.querySelector(".text").textContent).join(),
		});
	})()`
	if got, want := browser.String(state), `{"tabs":"fx-a,fx-b*","shown":"true,true","beside":true,"text":"trough 1,pen 1"}`; got != want {
		t.Fatalf("two panels are\n  %s\nwant\n  %s", got, want)
	}
	// The user asks for tabs: the chosen panel alone is shown.
	browser.Run(`document.getElementById("side").click()`)
	if got, want := browser.String(state), `{"tabs":"fx-a,fx-b*","shown":"false,true","beside":false,"text":"trough 1,pen 1"}`; got != want {
		t.Errorf("as tabs, the panels are\n  %s\nwant\n  %s", got, want)
	}
	// An update of the panel that is not shown is drawn in it and leaves the choice alone.
	send(`/fx-a update ` + view("trough 2"))
	browser.Wait(`document.querySelector('[data-key="fx-a"] .text').textContent === "trough 2"`)
	if got, want := browser.String(state), `{"tabs":"fx-a,fx-b*","shown":"false,true","beside":false,"text":"trough 2,pen 1"}`; got != want {
		t.Errorf("after an update of the panel behind, the panels are\n  %s\nwant\n  %s", got, want)
	}
	// The user chooses the other tab, by pointer and then back by keyboard.
	browser.Run(`document.querySelector('[role=tab]').click()`)
	if got, want := browser.String(state), `{"tabs":"fx-a*,fx-b","shown":"true,false","beside":false,"text":"trough 2,pen 1"}`; got != want {
		t.Errorf("after choosing the first tab, the panels are\n  %s\nwant\n  %s", got, want)
	}
	// The extension hides the chosen panel: its tab goes and the other panel is shown. Shown again, it is chosen.
	send(`/fx-a hide`)
	browser.Wait(`document.querySelector('[data-key="fx-a"]').hidden`)
	if got, want := browser.String(`[...document.querySelectorAll('[role=tab]')].map((tab) => tab.hidden).join() + "/" + (document.querySelector('[data-key="fx-b"]').getClientRects().length > 0)`), `true,false/true`; got != want {
		t.Errorf("after the extension hid the chosen panel: tabs hidden / other panel shown = %s, want %s", got, want)
	}
	send(`/fx-a show`)
	browser.Wait(`!document.querySelector('[data-key="fx-a"]').hidden`)
	if got, want := browser.String(state), `{"tabs":"fx-a*,fx-b","shown":"true,false","beside":false,"text":"trough 2,pen 1"}`; got != want {
		t.Errorf("after the extension showed the panel again, the panels are\n  %s\nwant\n  %s", got, want)
	}
	browser.Run(`document.querySelector('[role=tab][aria-selected=true]').dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowRight", bubbles: true }))`)
	if got, want := browser.String(state+` + "/" + document.activeElement.textContent`), `{"tabs":"fx-a,fx-b*","shown":"false,true","beside":false,"text":"trough 2,pen 1"}/fx-b`; got != want {
		t.Errorf("after ArrowRight on the tab, the panels are\n  %s\nwant\n  %s", got, want)
	}
}
