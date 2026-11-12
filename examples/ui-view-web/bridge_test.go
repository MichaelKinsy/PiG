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
// More arguments go to pig.
func startBridge(t *testing.T, more ...string) bridge {
	t.Helper()
	if testing.Short() {
		t.Skip("starts Node and pig")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	home := t.TempDir()
	args := []string{"bridge.mjs", "--port", "0", "--pig", buildPig(t), "--", "--model", "test-faux/echo", "--no-session", "-e", "demo-extension.mjs"}
	cmd := exec.Command(node, append(args, more...)...)
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

// openFixtures opens the page over a pig with the two fixture extensions (testdata/fixture.mjs) and returns the browser
// and a function that sends a line as the user does: `/fx-a mount <content>`, `/fx-b update <content>`.
func openFixtures(t *testing.T) (*browsertest.Browser, func(line string)) {
	t.Helper()
	b := startBridge(t, "-e", "testdata/fixture-a.mjs", "-e", "testdata/fixture-b.mjs")
	browser := browsertest.Start(t)
	browser.Open(b.base + "/?token=" + b.token)
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
// what the view says (a text's default is one cell each way). A cell is 1ch wide and one line high.
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
		probe.style.cssText = "width: 10ch; height: 0";
		view.append(probe);
		const col = probe.getBoundingClientRect().width / 10;
		probe.remove();
		const row = parseFloat(getComputedStyle(view).lineHeight);
		const rect = (el) => el.getBoundingClientRect();
		const near = (a, b) => Math.abs(a - b) < 0.75;
		const [cells, ends, box, fixed] = view.children;
		const c = [...cells.children].map(rect), e = [...ends.children].map(rect);
		const boxStyle = getComputedStyle(box), textStyle = getComputedStyle(box.firstChild);
		return JSON.stringify({
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
	if want := `{"equalColumns":true,"sameRow":true,"columnGap":true,"rowGap":true,"alignEnd":true,"boxPadding":true,"textPadding":true,"basis":true}`; got != want {
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
