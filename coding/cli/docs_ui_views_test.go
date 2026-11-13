package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// The tests below run what the documentation page docs/site/docs/ui-views.md teaches (D614). They take the page's
// complete extensions and its Piglet out of the page's code fences, load them in the real binary over rpc, attach
// as a client with the terminal's claims and as one with the web page's, and raise the intents the page declares.
// An example that the page breaks fails here.

const docsUIViewsPagePath = "../../docs/site/docs/ui-views.md"

var docsUIViewsFence = regexp.MustCompile("(?ms)^```(js|yaml|go)\n(.*?)\n```$")

var docsUIViewsExampleName = regexp.MustCompile(`^// ([a-z-]+\.mjs|[a-z]+/extension\.go): `)

// docsUIViewsSamples returns the page, its complete extensions by file name and its Piglet.
func docsUIViewsSamples(t *testing.T) (page string, examples map[string]string, piglet string) {
	t.Helper()
	raw, err := os.ReadFile(docsUIViewsPagePath)
	if err != nil {
		t.Fatal(err)
	}
	page = string(raw)
	examples = map[string]string{}
	for _, m := range docsUIViewsFence.FindAllStringSubmatch(page, -1) {
		if name := docsUIViewsExampleName.FindStringSubmatch(m[2]); m[1] != "yaml" && name != nil {
			examples[name[1]] = m[2] + "\n"
		}
		if m[1] == "yaml" {
			piglet = m[2] + "\n"
		}
	}
	if examples["todo.mjs"] == "" || examples["todo/extension.go"] == "" || examples["week.mjs"] == "" || examples["meter.mjs"] == "" || piglet == "" {
		t.Fatalf("%s holds %d examples and the Piglet %q, want todo.mjs, todo/extension.go, week.mjs, meter.mjs and a Piglet", docsUIViewsPagePath, len(examples), piglet)
	}
	return page, examples, piglet
}

// startDocsUIViews starts `pig --mode rpc` with args and waits until the session answers.
func startDocsUIViews(t *testing.T, args ...string) *rpcLines {
	t.Helper()
	argv := append([]string{"--mode", "rpc", "--model", "test-faux/echo", "--no-session"}, args...)
	cmd := exec.Command(buildPigBinaryForSignalTest(t), argv...)
	cmd.Env = append(os.Environ(), listenEnv(t, listenTempDir(t))...)
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
	c := newRPCLines(t, stdin, stdout, stderr)
	c.request(`{"id":"state","type":"get_state"}`)
	return c
}

// docsUIViewsExtension writes source as name in a new directory and returns its path.
func docsUIViewsExtension(t *testing.T, name, source string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

const docsUIViewsNodes = `"container","box","vstack","hstack","text","truncated-text","markdown","lines","spacer","dynamic-border","select-list","settings-list","input","editor","mouse-region","scroll-view"`

// The claims and limits below are the terminal's (internal/codingagent/view_client.go) and the reference page's
// (examples/ui-view-web/client.js). docsUIViewsDrawingClient is the page's with the scene profile, which no client of
// this tree states.
const (
	docsUIViewsTerminal = `{"slots":["widget-above","widget-below","panel","dialog"],"nodes":[` + docsUIViewsNodes + `,"grid"],"markdown":true,"imageFormats":[],` +
		`"limits":{"nodes":512,"depth":16,"snapshotBytes":262144,"textBytes":65536,"mounts":16,"intentArgBytes":4096},"input":{"keyboard":true,"pointer":false,"text":true},` +
		`"claims":[{"name":"client.kind","value":"terminal"},{"name":"size.columns","value":80}],"slotClaims":[{"slot":"panel","claims":[{"name":"size.rows","value":12}]},{"slot":"dialog","claims":[{"name":"size.columns","value":62},{"name":"size.rows","value":17}]}]}`
	docsUIViewsWebHead = `{"slots":["notify","status","widget-above","widget-below","header","footer","dialog","overlay","panel","tool-card","message","entry"],"nodes":[` + docsUIViewsNodes + `],"markdown":true,"imageFormats":["png","jpeg","gif","webp"],` +
		`"limits":{"nodes":2048,"depth":32,"snapshotBytes":1048576,"textBytes":65536,"mounts":32,"intentArgBytes":65536},"input":{"keyboard":true,"pointer":true,"text":true},` +
		`"claims":[{"name":"client.kind","value":"web"}]`
	docsUIViewsWeb           = docsUIViewsWebHead + `}`
	docsUIViewsDrawingClient = docsUIViewsWebHead + `,"drawing":{"profiles":["scene"],"shapes":4096,"pixels":0}}`
)

// docsUIViewsMount is one snapshot a client was sent, with its view as JSON text.
type docsUIViewsMount struct {
	snapshot map[string]any
	view     string
	rev      float64
}

func docsUIViewsSnapshot(t *testing.T, value any) docsUIViewsMount {
	t.Helper()
	snapshot, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("%v is not a snapshot", value)
	}
	view, err := json.Marshal(snapshot["view"])
	if err != nil {
		t.Fatal(err)
	}
	ref, _ := snapshot["mount"].(map[string]any)
	rev, _ := ref["rev"].(float64)
	return docsUIViewsMount{snapshot: snapshot, view: string(view), rev: rev}
}

// attach attaches with abilities and returns the attachment and the first mount the client is sent: the one of the
// attach reply, or the one the extension makes when the client arrives.
func docsUIViewsAttach(t *testing.T, c *rpcLines, id, abilities string) (attachment float64, mount docsUIViewsMount) {
	t.Helper()
	line, reply := c.request(`{"id":"` + id + `","type":"ui_view_attach","abilities":` + abilities + `}`)
	data, _ := reply["data"].(map[string]any)
	if reply["success"] != true {
		t.Fatalf("attach = %s", line)
	}
	attachment, _ = data["attachment"].(float64)
	if snapshots, _ := data["snapshots"].([]any); len(snapshots) > 0 {
		return attachment, docsUIViewsSnapshot(t, snapshots[0])
	}
	_, event := c.until("the mount the extension makes for the client", func(record map[string]any) bool { return record["type"] == "ui_view" })
	return attachment, docsUIViewsSnapshot(t, event["snapshot"])
}

// docsUIViewsRaise raises intent on mount and returns the handler's redraw, written before the reply, and the reply's data.
func docsUIViewsRaise(t *testing.T, c *rpcLines, attachment float64, mount docsUIViewsMount, node, intent, arg string) ([]docsUIViewsMount, map[string]any) {
	t.Helper()
	before, data := docsUIViewsRaiseAny(t, c, attachment, mount, node, intent, arg)
	if len(before) != 1 || before[0].rev != mount.rev+1 {
		t.Fatalf("intent %s: %d snapshots were written before the reply, want the handler's redraw at rev %v: %v", intent, len(before), mount.rev+1, before)
	}
	return before, data
}

// docsUIViewsRaiseAny raises intent on mount and returns the snapshots written before the reply and the reply's data.
func docsUIViewsRaiseAny(t *testing.T, c *rpcLines, attachment float64, mount docsUIViewsMount, node, intent, arg string) ([]docsUIViewsMount, map[string]any) {
	t.Helper()
	ref, _ := mount.snapshot["mount"].(map[string]any)
	nodeKey := ""
	if node != "" {
		nodeKey = fmt.Sprintf(`"nodeKey":%q,`, node)
	}
	c.send(fmt.Sprintf(`{"id":%q,"type":"ui_view_intent","attachment":%v,"plugin":%q,"mount":{"key":%q,"epoch":%v,"rev":%v},%s"intent":%q,"arg":%s}`,
		intent, attachment, mount.snapshot["plugin"], ref["key"], ref["epoch"], ref["rev"], nodeKey, intent, arg))
	var before []docsUIViewsMount
	line, reply := c.until("the reply of intent "+intent, func(record map[string]any) bool {
		if record["type"] == "ui_view" {
			before = append(before, docsUIViewsSnapshot(t, record["snapshot"]))
		}
		return record["type"] == "response" && record["id"] == intent && record["command"] == "ui_view_intent"
	})
	data, _ := reply["data"].(map[string]any)
	if reply["success"] != true || data["outcome"] != "done" {
		t.Fatalf("intent %s replied %s, want done", intent, line)
	}
	return before, data
}

func docsUIViewsWant(t *testing.T, what, got string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("%s = %s, want it to hold %q", what, got, w)
		}
	}
}

// TestDocsUIViewsTodoExampleServesTheTerminalAndTheWebPage runs the page's first example: a client with the terminal's
// claims is sent the view, its intent is answered with a redraw, and a client with the web page's claims is then sent
// the alternative of the same state.
func TestDocsUIViewsTodoExampleServesTheTerminalAndTheWebPage(t *testing.T) {
	if testing.Short() {
		t.Skip("starts pig and a Node extension")
	}
	_, examples, _ := docsUIViewsSamples(t)
	c := startDocsUIViews(t, "-e", docsUIViewsExtension(t, "todo.mjs", examples["todo.mjs"]))

	terminal, mount := docsUIViewsAttach(t, c, "terminal", docsUIViewsTerminal)
	if mount.snapshot["alternative"] != nil || mount.snapshot["slot"] != "panel" || mount.snapshot["state"] != "live" {
		t.Fatalf("the terminal was sent %v, want the live panel with the mount's own view", mount.snapshot)
	}
	docsUIViewsWant(t, "the terminal's view", mount.view, "Todo: 2 open", "[ ] Buy milk")
	if strings.Contains(mount.view, "markdown") {
		t.Errorf("the terminal's view = %s, want no markdown node", mount.view)
	}
	a11y, _ := mount.snapshot["a11y"].([]any)
	bindings, _ := mount.snapshot["bindings"].([]any)
	if len(a11y) != 1 || len(bindings) != 1 || mount.snapshot["title"] != "Todo" {
		t.Errorf("the terminal was sent the a11y records %v, the bindings %v and the title %v, want the list's record, the binding of c and Todo", mount.snapshot["a11y"], mount.snapshot["bindings"], mount.snapshot["title"])
	}
	redrawn, _ := docsUIViewsRaise(t, c, terminal, mount, "items", "todo.toggle", `{"value":"milk"}`)
	docsUIViewsWant(t, "the view after the toggle", redrawn[0].view, "Todo: 1 open", "[x] Buy milk")
	if line, _ := c.request(fmt.Sprintf(`{"id":"detach","type":"ui_view_detach","attachment":%v}`, terminal)); !strings.Contains(line, `"success":true`) {
		t.Fatalf("detach = %s", line)
	}

	web, mount := docsUIViewsAttach(t, c, "web", docsUIViewsWeb)
	if mount.snapshot["alternative"] != 1.0 {
		t.Fatalf("the web page was sent alternative %v of %v, want alternative 1", mount.snapshot["alternative"], mount.snapshot)
	}
	docsUIViewsWant(t, "the web page's view", mount.view, `# Todo\n\n**1** of 3 open.`, "[x] Buy milk")
	// A binding and a mount-wide control raise the intent without a nodeKey. The handler opens the dialog, a second mount.
	asked, _ := docsUIViewsRaiseAny(t, c, web, mount, "", "todo.clear", `{}`)
	if len(asked) != 1 || asked[0].snapshot["slot"] != "dialog" || asked[0].snapshot["title"] != "Clear done items" || asked[0].snapshot["state"] != "live" {
		t.Fatalf("todo.clear wrote %v before its reply, want the live dialog", asked)
	}
	docsUIViewsWant(t, "the dialog's view", asked[0].view, "Remove 2 done items?")
	if intents, _ := json.Marshal(asked[0].snapshot["intents"]); !strings.Contains(string(intents), `"dismiss":true,"name":"todo.cancel"`) {
		t.Errorf("the dialog's intents = %s, want todo.cancel as the dismiss intent", intents)
	}
	// A client raises the dismiss intent with {} when the user dismisses the dialog: the handler removes the mount and
	// keeps the items. The next todo.clear opens the dialog again, under a new epoch.
	closed, data := docsUIViewsRaiseAny(t, c, web, asked[0], "", "todo.cancel", `{}`)
	if len(closed) == 0 || closed[0].snapshot["state"] != "removed" || fmt.Sprint(data["answer"]) != "map[removed:0]" {
		t.Fatalf("todo.cancel wrote %v and answered %v, want the removed dialog and removed 0", closed, data["answer"])
	}
	list := closed[len(closed)-1]
	if list.snapshot["slot"] != "panel" || !strings.Contains(list.view, "**1** of 3 open.") {
		t.Fatalf("after todo.cancel the last snapshot is %v, want the unchanged list", list.snapshot)
	}
	asked, _ = docsUIViewsRaiseAny(t, c, web, list, "", "todo.clear", `{}`)
	if len(asked) != 1 || asked[0].snapshot["slot"] != "dialog" || asked[0].snapshot["state"] != "live" {
		t.Fatalf("the second todo.clear wrote %v before its reply, want the live dialog", asked)
	}
	closed, data = docsUIViewsRaiseAny(t, c, web, asked[0], "", "todo.confirm", `{"yes":true}`)
	if fmt.Sprint(data["answer"]) != "map[removed:2]" {
		t.Errorf("todo.confirm answered %v, want removed 2", data["answer"])
	}
	var gone, listed bool
	for _, s := range closed {
		gone = gone || s.snapshot["slot"] == "dialog" && s.snapshot["state"] == "removed"
		listed = listed || s.snapshot["slot"] == "panel" && strings.Contains(s.view, "**1** of 1 open.")
	}
	if !gone || !listed {
		t.Errorf("todo.confirm wrote %v before its reply, want the removed dialog and the list of one item", closed)
	}
	c.end()
}

// TestDocsUIViewsGoExampleServesTheTerminalAndTheWebPage runs the page's Go extension, which pig builds when it loads
// it: a client with the terminal's claims is sent the view, its intent is answered with a redraw, and a client with the
// web page's claims is then sent the alternative of the same state.
func TestDocsUIViewsGoExampleServesTheTerminalAndTheWebPage(t *testing.T) {
	if testing.Short() {
		t.Skip("starts pig and builds a Go extension")
	}
	_, examples, _ := docsUIViewsSamples(t)
	c := startDocsUIViews(t, "-e", docsUIViewsGoModule(t, "todo", examples["todo/extension.go"]))
	terminal, mount := docsUIViewsAttach(t, c, "terminal", docsUIViewsTerminal)
	if mount.snapshot["alternative"] != nil || mount.snapshot["title"] != "Todo" {
		t.Fatalf("the terminal was sent %v, want the mount's own view under the title Todo", mount.snapshot)
	}
	docsUIViewsWant(t, "the terminal's view", mount.view, "Todo: 2 open", "[ ] Buy milk")
	redrawn, data := docsUIViewsRaise(t, c, terminal, mount, "items", "todo.toggle", `{"value":"milk"}`)
	docsUIViewsWant(t, "the view after the toggle", redrawn[0].view, "Todo: 1 open", "[x] Buy milk")
	if data["answer"] != nil {
		t.Errorf("todo.toggle answered %v, want no answer", data["answer"])
	}
	if line, _ := c.request(fmt.Sprintf(`{"id":"detach","type":"ui_view_detach","attachment":%v}`, terminal)); !strings.Contains(line, `"success":true`) {
		t.Fatalf("detach = %s", line)
	}
	web, mount := docsUIViewsAttach(t, c, "web", docsUIViewsWeb)
	if mount.snapshot["alternative"] != 1.0 {
		t.Fatalf("the web page was sent alternative %v of %v, want alternative 1", mount.snapshot["alternative"], mount.snapshot)
	}
	docsUIViewsWant(t, "the web page's view", mount.view, `# Todo\n\n**1** of 3 open.`, "[x] Buy milk")
	redrawn, data = docsUIViewsRaise(t, c, web, mount, "", "todo.clear", `{}`)
	docsUIViewsWant(t, "the web page's view after todo.clear", redrawn[0].view, "**1** of 1 open.")
	if fmt.Sprint(data["answer"]) != "map[removed:2]" {
		t.Errorf("todo.clear answered %v, want removed 2", data["answer"])
	}
	c.end()
}

// TestDocsUIViewsPageAloneTeachesAGoCounter runs testdata/docs-ui-views-counter-go/extension.go, a Go extension written
// from the page alone by a reader without the source: a text view with a binding for the terminal, a markdown
// alternative for the web page, and for a client that draws the scene profile a drawing of one shape whose region raises
// the intent.
func TestDocsUIViewsPageAloneTeachesAGoCounter(t *testing.T) {
	if testing.Short() {
		t.Skip("starts pig and builds a Go extension")
	}
	source, err := os.ReadFile(filepath.Join("testdata", "docs-ui-views-counter-go", "extension.go"))
	if err != nil {
		t.Fatal(err)
	}
	root := docsUIViewsGoModule(t, "counter", string(source))
	c := startDocsUIViews(t, "-e", root)
	terminal, mount := docsUIViewsAttach(t, c, "terminal", docsUIViewsTerminal)
	docsUIViewsWant(t, "the terminal's counter", mount.view, "Count: 0. Press a to add one.")
	if mount.snapshot["alternative"] != nil || mount.snapshot["drawing"] != nil || mount.snapshot["title"] != "Counter" {
		t.Errorf("the terminal was sent %v, want the view titled Counter without a drawing", mount.snapshot)
	}
	redrawn, data := docsUIViewsRaise(t, c, terminal, mount, "", "counter.add", `{}`)
	docsUIViewsWant(t, "the terminal's counter after the binding's intent", redrawn[0].view, "Count: 1.")
	if fmt.Sprint(data["answer"]) != "map[count:1]" {
		t.Errorf("counter.add answered %v, want count 1", data["answer"])
	}
	c.end()

	c = startDocsUIViews(t, "-e", root)
	_, mount = docsUIViewsAttach(t, c, "web", docsUIViewsWeb)
	docsUIViewsWant(t, "the web page's counter", mount.view, `# Counter\n\nThe count is **0**.`)
	if mount.snapshot["alternative"] != 1.0 || mount.snapshot["drawing"] != nil {
		t.Errorf("the web page was sent %v, want alternative 1 without a drawing", mount.snapshot)
	}
	c.end()

	c = startDocsUIViews(t, "-e", root)
	able, mount := docsUIViewsAttach(t, c, "able", docsUIViewsDrawingClient)
	width := func(mount docsUIViewsMount) any {
		drawing, _ := mount.snapshot["drawing"].(map[string]any)
		shapes, _ := drawing["shapes"].([]any)
		regions, _ := drawing["regions"].([]any)
		if mount.snapshot["alternative"] != 1.0 || drawing["node"] != "chart" || len(shapes) != 1 || len(regions) != 1 {
			t.Fatalf("the client that draws scenes was sent %v, want alternative 1 with a drawing of one shape and one region in the node chart", mount.snapshot)
		}
		return shapes[0].(map[string]any)["width"]
	}
	if width(mount) != 10.0 {
		t.Errorf("the chart's bar is %v wide, want 10", width(mount))
	}
	redrawn, _ = docsUIViewsRaise(t, c, able, mount, "", "counter.add", `{}`)
	if width(redrawn[0]) != 20.0 {
		t.Errorf("after the region's intent the bar is %v wide, want 20", width(redrawn[0]))
	}
	c.end()
}

// docsUIViewsGoModule writes a Go extension, source in package name, as a module that takes the SDK of this tree, and
// returns its directory.
func docsUIViewsGoModule(t *testing.T, name, source string) string {
	t.Helper()
	sdkRoot, err := filepath.Abs(filepath.Join("..", "..", "extensions", "sdk"))
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(root, 0o750); err != nil {
		t.Fatal(err)
	}
	goMod := "module example.com/" + name + "\n\ngo 1.26\n\nrequire github.com/MichaelKinsy/PiG/extensions/sdk v0.0.0\n\nreplace github.com/MichaelKinsy/PiG/extensions/sdk => " + sdkRoot + "\n"
	for file, text := range map[string]string{"go.mod": goMod, "extension.go": source} {
		if err := os.WriteFile(filepath.Join(root, file), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// TestDocsUIViewsWeekExampleHidesAndOpens runs the page's grid example: the client is sent the grid under its title, a
// chosen cell is redrawn as the selected one, the bound intent hides the mount, and the whenHidden intent, raised on the
// hidden mount, shows it again.
func TestDocsUIViewsWeekExampleHidesAndOpens(t *testing.T) {
	if testing.Short() {
		t.Skip("starts pig and a Node extension")
	}
	_, examples, _ := docsUIViewsSamples(t)
	c := startDocsUIViews(t, "-e", docsUIViewsExtension(t, "week.mjs", examples["week.mjs"]))
	terminal, mount := docsUIViewsAttach(t, c, "terminal", docsUIViewsTerminal)
	docsUIViewsWant(t, "the grid's view", mount.view, `"kind":"grid"`, `"columns":7`, `"selected":"Wed"`, `"mark":"current"`, `"text":"Demo"`)
	if mount.snapshot["title"] != "Week" {
		t.Errorf("the mount's title = %v, want Week", mount.snapshot["title"])
	}
	redrawn, _ := docsUIViewsRaise(t, c, terminal, mount, "days", "week.pick", `{"value":"Fri"}`)
	docsUIViewsWant(t, "the grid after the choice", redrawn[0].view, `"selected":"Fri"`)
	hidden, _ := docsUIViewsRaiseAny(t, c, terminal, redrawn[0], "", "week.hide", `{}`)
	if len(hidden) != 1 || hidden[0].snapshot["state"] != "hidden" {
		t.Fatalf("week.hide wrote %v before its reply, want the hidden mount", hidden)
	}
	shown, _ := docsUIViewsRaiseAny(t, c, terminal, hidden[0], "", "week.open", `{}`)
	if len(shown) != 1 || shown[0].snapshot["state"] != "live" {
		t.Fatalf("week.open on the hidden mount wrote %v before its reply, want the live mount", shown)
	}
	c.end()
}

// TestDocsUIViewsPageAloneTeachesACounter runs testdata/docs-ui-views-counter.mjs, an extension written from the page
// alone by a reader without the source: a text view with a button for the terminal, a markdown alternative for the web
// page, and for a client that draws the scene profile a drawing of one shape whose region raises an intent.
func TestDocsUIViewsPageAloneTeachesACounter(t *testing.T) {
	if testing.Short() {
		t.Skip("starts pig and a Node extension")
	}
	counter, err := filepath.Abs(filepath.Join("testdata", "docs-ui-views-counter.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	c := startDocsUIViews(t, "-e", counter)
	terminal, mount := docsUIViewsAttach(t, c, "terminal", docsUIViewsTerminal)
	docsUIViewsWant(t, "the terminal's counter", mount.view, "Count: 0", "[ Add ]")
	if mount.snapshot["alternative"] != nil || mount.snapshot["drawing"] != nil {
		t.Errorf("the terminal was sent %v, want the view without a drawing", mount.snapshot)
	}
	redrawn, data := docsUIViewsRaise(t, c, terminal, mount, "add", "counter.add", `{}`)
	docsUIViewsWant(t, "the terminal's counter after the button", redrawn[0].view, "Count: 1")
	if fmt.Sprint(data["answer"]) != "map[count:1]" {
		t.Errorf("counter.add answered %v, want count 1", data["answer"])
	}
	c.end()

	c = startDocsUIViews(t, "-e", counter)
	_, mount = docsUIViewsAttach(t, c, "web", docsUIViewsWeb)
	docsUIViewsWant(t, "the web page's counter", mount.view, `# Counter\n\nThe count is **0**.`, "[ Add ]")
	if mount.snapshot["alternative"] != 1.0 || mount.snapshot["drawing"] != nil {
		t.Errorf("the web page was sent %v, want alternative 1 without a drawing", mount.snapshot)
	}
	c.end()

	c = startDocsUIViews(t, "-e", counter)
	able, mount := docsUIViewsAttach(t, c, "able", docsUIViewsDrawingClient)
	width := func(mount docsUIViewsMount) any {
		drawing, _ := mount.snapshot["drawing"].(map[string]any)
		shapes, _ := drawing["shapes"].([]any)
		regions, _ := drawing["regions"].([]any)
		if mount.snapshot["alternative"] != 1.0 || drawing["node"] != "chart" || len(shapes) != 1 || len(regions) != 1 {
			t.Fatalf("the client that draws scenes was sent %v, want alternative 1 with a drawing of one shape and one region in the node chart", mount.snapshot)
		}
		return shapes[0].(map[string]any)["width"]
	}
	if width(mount) != 10.0 {
		t.Errorf("the chart's bar is %v wide, want 10", width(mount))
	}
	redrawn, _ = docsUIViewsRaise(t, c, able, mount, "", "counter.add-chart", `{}`)
	if width(redrawn[0]) != 20.0 {
		t.Errorf("after the region's intent the bar is %v wide, want 20", width(redrawn[0]))
	}
	c.end()
}

// TestDocsUIViewsMeterExampleDrawsForAnAbleClient runs the page's drawing example: a client that states the scene
// profile is sent the drawing and its region's intent redraws it, a client that states none is sent text, and the same
// drawing mounted for the client that states none fails with code unsupported.
func TestDocsUIViewsMeterExampleDrawsForAnAbleClient(t *testing.T) {
	if testing.Short() {
		t.Skip("starts pig and a Node extension")
	}
	_, examples, _ := docsUIViewsSamples(t)
	bar := func(mount docsUIViewsMount) any {
		drawing, _ := mount.snapshot["drawing"].(map[string]any)
		shapes, _ := drawing["shapes"].([]any)
		if drawing["profile"] != "scene" || drawing["node"] != "canvas" || len(shapes) != 3 {
			t.Fatalf("the drawing = %v, want the scene of three shapes in the node canvas", mount.snapshot["drawing"])
		}
		return shapes[1].(map[string]any)["width"]
	}

	c := startDocsUIViews(t, "-e", docsUIViewsExtension(t, "meter.mjs", examples["meter.mjs"]))
	able, drawn := docsUIViewsAttach(t, c, "able", docsUIViewsDrawingClient)
	if bar(drawn) != 30.0 {
		t.Errorf("the bar is %v wide, want 30", bar(drawn))
	}
	// A region names its intent and no node, so the client raises the intent without a nodeKey.
	redrawn, data := docsUIViewsRaise(t, c, able, drawn, "", "meter.raise", `{}`)
	if bar(redrawn[0]) != 40.0 || fmt.Sprint(data["answer"]) != "map[level:4]" {
		t.Errorf("after meter.raise the bar is %v wide and the answer %v, want 40 and level 4", bar(redrawn[0]), data["answer"])
	}
	c.end()

	c = startDocsUIViews(t, "-e", docsUIViewsExtension(t, "meter.mjs", examples["meter.mjs"]))
	plain, mount := docsUIViewsAttach(t, c, "plain", docsUIViewsTerminal)
	if mount.snapshot["drawing"] != nil {
		t.Errorf("a client that draws no profile was sent the drawing %v", mount.snapshot["drawing"])
	}
	docsUIViewsWant(t, "the view of a client that draws no profile", mount.view, "Level 3 of 10. Press r to raise it.")
	// The binding of r raises the intent without a nodeKey, as the region does.
	redrawn, _ = docsUIViewsRaise(t, c, plain, mount, "", "meter.raise", `{}`)
	docsUIViewsWant(t, "the view after the binding's intent", redrawn[0].view, "Level 4 of 10")
	c.end()

	const asks = `scene = abilities.drawing?.profiles.includes("scene") ?? false;`
	forced := strings.Replace(examples["meter.mjs"], asks, "scene = true;", 1)
	if forced == examples["meter.mjs"] {
		t.Fatalf("meter.mjs no longer holds the line %q", asks)
	}
	c = startDocsUIViews(t, "-e", docsUIViewsExtension(t, "meter.mjs", forced))
	c.send(`{"id":"refused","type":"ui_view_attach","abilities":` + docsUIViewsTerminal + `}`)
	c.until("the example's report of the rejected mount", func(record map[string]any) bool {
		if record["type"] == "ui_view" {
			t.Fatalf("a drawing was mounted for a client that draws no profile: %v", record)
		}
		return record["type"] == "extension_ui_request" && record["message"] == "meter: unsupported"
	})
	c.end()
}

// TestDocsUIViewsPigletLoadsTheExample runs the page's Piglet: the session loads todo.mjs from it and the extension
// mounts for the client that attaches.
func TestDocsUIViewsPigletLoadsTheExample(t *testing.T) {
	if testing.Short() {
		t.Skip("starts pig and a Node extension")
	}
	_, examples, piglet := docsUIViewsSamples(t)
	extension := docsUIViewsExtension(t, "todo.mjs", examples["todo.mjs"])
	path := filepath.Join(filepath.Dir(extension), "views.yaml")
	if err := os.WriteFile(path, []byte(piglet), 0o600); err != nil {
		t.Fatal(err)
	}
	c := startDocsUIViews(t, "--piglet", path)
	_, mount := docsUIViewsAttach(t, c, "web", docsUIViewsWeb)
	docsUIViewsWant(t, "the view of the Piglet's extension", mount.view, "# Todo")
	c.end()
}

// TestDocsUIViewsPageIsInTheBundle pins what a session can read: `pig docs show ui-views` prints the page with its
// mount call, its alternatives example and its drawing section, the bundle directory holds the same file, and the
// bundle's README names it.
func TestDocsUIViewsPageIsInTheBundle(t *testing.T) {
	if testing.Short() {
		t.Skip("starts pig")
	}
	_, examples, _ := docsUIViewsSamples(t)
	env := append(os.Environ(), listenEnv(t, listenTempDir(t))...)
	pig := func(args ...string) string {
		t.Helper()
		cmd := exec.Command(buildPigBinaryForSignalTest(t), args...)
		cmd.Env = env
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("pig %v: %v\n%s", args, err, stderr.String())
		}
		return string(out)
	}
	shown := pig("docs", "show", "ui-views")
	docsUIViewsWant(t, "pig docs show ui-views", shown, "# Extension views", "ctx.ui.mountView({ key", `alternatives: [{ when: { any: [{ all: [{ claim: "client.kind", op: "equals", value: "web" }] }] }, view: rich() }]`, "## Drawings", "## What the clients draw",
		strings.TrimSuffix(examples["todo.mjs"], "\n"), strings.TrimSuffix(examples["todo/extension.go"], "\n"), strings.TrimSuffix(examples["week.mjs"], "\n"), strings.TrimSuffix(examples["meter.mjs"], "\n"))
	if !strings.Contains(pig("docs", "list"), "ui-views.md\n") {
		t.Error("pig docs list does not name ui-views.md")
	}
	dir := strings.TrimSpace(pig("docs", "path"))
	if onDisk, err := os.ReadFile(filepath.Join(dir, "ui-views.md")); err != nil || string(onDisk) != shown {
		t.Errorf("%s/ui-views.md differs from pig docs show ui-views (%v)", dir, err)
	}
	readme, err := os.ReadFile(filepath.Join(dir, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	docsUIViewsWant(t, "the bundle's README", string(readme), "(ui-views.md)")
}
