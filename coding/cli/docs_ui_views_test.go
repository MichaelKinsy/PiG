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

var docsUIViewsFence = regexp.MustCompile("(?ms)^```(js|yaml)\n(.*?)\n```$")

var docsUIViewsExampleName = regexp.MustCompile(`^// ([a-z-]+\.mjs): `)

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
		if name := docsUIViewsExampleName.FindStringSubmatch(m[2]); m[1] == "js" && name != nil {
			examples[name[1]] = m[2] + "\n"
		}
		if m[1] == "yaml" {
			piglet = m[2] + "\n"
		}
	}
	if examples["todo.mjs"] == "" || examples["meter.mjs"] == "" || piglet == "" {
		t.Fatalf("%s holds the examples %v and the Piglet %q, want todo.mjs, meter.mjs and a Piglet", docsUIViewsPagePath, len(examples), piglet)
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

const docsUIViewsNodes = `["container","box","vstack","hstack","text","truncated-text","markdown","lines","spacer","dynamic-border","select-list","settings-list","input","editor","mouse-region","scroll-view"]`

// The claims and limits below are the terminal's (internal/codingagent/view_client.go) and the reference page's
// (examples/ui-view-web/client.js). docsUIViewsDrawingClient is the page's with the scene profile, which no client of
// this tree states.
const (
	docsUIViewsTerminal = `{"slots":["widget-above","widget-below","panel"],"nodes":` + docsUIViewsNodes + `,"markdown":true,"imageFormats":[],` +
		`"limits":{"nodes":512,"depth":16,"snapshotBytes":262144,"textBytes":65536,"mounts":16,"intentArgBytes":4096},"input":{"keyboard":true,"pointer":false,"text":true},` +
		`"claims":[{"name":"client.kind","value":"terminal"},{"name":"size.columns","value":80}],"slotClaims":[{"slot":"panel","claims":[{"name":"size.rows","value":12}]}]}`
	docsUIViewsWebHead = `{"slots":["notify","status","widget-above","widget-below","header","footer","dialog","overlay","panel","tool-card","message","entry"],"nodes":` + docsUIViewsNodes + `,"markdown":true,"imageFormats":["png","jpeg","gif","webp"],` +
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

// raise raises intent on mount and returns the snapshots written before the reply and the reply's data.
func docsUIViewsRaise(t *testing.T, c *rpcLines, attachment float64, mount docsUIViewsMount, node, intent, arg string) ([]docsUIViewsMount, map[string]any) {
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
	if len(before) != 1 || before[0].rev != mount.rev+1 {
		t.Fatalf("intent %s: %d snapshots were written before the reply, want the handler's redraw at rev %v: %v", intent, len(before), mount.rev+1, before)
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
	docsUIViewsWant(t, "the terminal's view", mount.view, "Todo: 2 open", "[ ] Buy milk", "[ Clear done ]")
	if strings.Contains(mount.view, "markdown") {
		t.Errorf("the terminal's view = %s, want no markdown node", mount.view)
	}
	if a11y, _ := mount.snapshot["a11y"].([]any); len(a11y) != 2 {
		t.Errorf("the terminal was sent the a11y records %v, want those of the list and the button", mount.snapshot["a11y"])
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
	docsUIViewsWant(t, "the web page's view", mount.view, `# Todo\n\n**1** of 3 open.`, "hstack", "[x] Buy milk")
	_, data := docsUIViewsRaise(t, c, web, mount, "clear", "todo.clear", `{}`)
	if fmt.Sprint(data["answer"]) != "map[removed:2]" {
		t.Errorf("todo.clear answered %v, want removed 2", data["answer"])
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
	// A region names its intent and no node, so the client raises it without a nodeKey.
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
	docsUIViewsWant(t, "the view of a client that draws no profile", mount.view, "Level 3 of 10", "[ Raise ]")
	redrawn, _ = docsUIViewsRaise(t, c, plain, mount, "raise", "meter.raise", `{}`)
	docsUIViewsWant(t, "the view after the button", redrawn[0].view, "Level 4 of 10")
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
		strings.TrimSuffix(examples["todo.mjs"], "\n"), strings.TrimSuffix(examples["meter.mjs"], "\n"))
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
