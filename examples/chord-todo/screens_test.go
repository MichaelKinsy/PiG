package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image/png"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/internal/browsertest"
	"github.com/MichaelKinsy/PiG/telemetry"
)

// The tutorial's screenshots (docs/chord/tutorial.md), as Pi's `screenshots.ts` and its test case take them: the page and the trace
// viewer in headless Chromium, a figure at each chapter's end. The test is skipped without a Chromium (`CHROMIUM_BIN`). It checks
// what Pi's test checks: every console output the book shows (`<!-- output: screenshots -->`) is among the lines the steps print, in
// order. It also checks the figures themselves: each screen the book shows (`screens/<name>.png`) is a PNG of the size it was taken
// at, and the one this run took has the size too. `TODO_SCREENS_OUT=<dir>` writes the run's screens there (the book's are made so).

// screens are the viewport sizes of the screens the book shows, by file.
var screens = map[string][2]int{
	"page.png": {560, 430}, "harbor.png": {560, 430}, "edited.png": {560, 430}, "french.png": {560, 430},
	"click-before.png": {560, 430}, "click-after.png": {560, 430}, "traces.png": {900, 640}, "traces-ada.png": {900, 640},
}

// screenshots takes the screens into out and prints what the book shows of them.
func screenshots(t *testing.T, out string, printed *bytes.Buffer) {
	t.Helper()
	browser := browsertest.Start(t)
	ctx := context.Background()
	say := func(format string, args ...any) { fmt.Fprintf(printed, format+"\n", args...) }
	base := t.TempDir()
	dataDir, root := filepath.Join(base, "data"), filepath.Join(base, "plugins")
	if err := os.CopyFS(root, os.DirFS(filepath.Join(here, "plugins"))); err != nil {
		t.Fatal(err)
	}
	store, err := openTelemetry(telemetryFile(dataDir))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	server, err := startTodo(ctx, dataDir, root, store)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = server.Close(ctx) }()
	link, err := url.Parse(server.OwnerLink())
	if err != nil {
		t.Fatal(err)
	}
	s := &session{ctx: ctx, out: printed, app: server, dataDir: dataDir, store: store, root: root, base: base, origin: link.Scheme + "://" + link.Host}

	// The list as the tutorial leaves it after chapter VII: the owner's todos, one by the server, one by Ada (a trace of the server's root
	// acting as her, which the viewer lists for her).
	as := func(principal data.Principal, trace string, texts ...string) {
		t.Helper()
		options := host.RootOptions{Principal: principal}
		if trace != "" {
			options.Trace = &host.RootTrace{Name: trace}
		}
		_, err := server.Host.Root(func(ctx host.Ctx) (any, error) {
			list, err := ctx.Get(todos, delta.JsonObjectOf("list", "groceries"), host.GetOptions{})
			for _, text := range texts {
				if err == nil {
					_, err = list.Call("add", text, ctx)
				}
			}
			return nil, err
		}, ctx, options)
		if err != nil {
			t.Fatal(err)
		}
	}
	as(ownerPrincipal, "", "milk", "eggs")
	as(serverPrincipal, "", "bread")
	as(ownerPrincipal, "", "butter")
	as(delta.JsonObjectOf("kind", "user", "id", "u1", "name", "ada"), "server.add", "coffee")
	if err := s.asRoot(ownerPrincipal, func(ctx host.Ctx) error {
		list, err := ctx.Get(todos, delta.JsonObjectOf("list", "groceries"), host.GetOptions{})
		if err == nil {
			_, err = list.Call("tick", delta.JsonObjectOf("id", 1, "done", true), ctx)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}

	call := func(method string, params any) json.RawMessage {
		t.Helper()
		raw, err := browser.Call(ctx, method, params)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	viewport := func(width, height int) {
		call("Emulation.setDeviceMetricsOverride", map[string]any{"width": width, "height": height, "deviceScaleFactor": 1, "mobile": false})
	}
	shoot := func(name string) {
		t.Helper()
		time.Sleep(300 * time.Millisecond)
		var shot struct{ Data string }
		if err := json.Unmarshal(call("Page.captureScreenshot", map[string]any{"format": "png"}), &shot); err != nil {
			t.Fatal(err)
		}
		bytes, err := base64.StdEncoding.DecodeString(shot.Data)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(out, name), bytes, 0o644); err != nil {
			t.Fatal(err)
		}
		say("wrote %s", filepath.Join(out, name))
	}
	boolean := func(expression string) bool {
		t.Helper()
		raw, err := browser.Evaluate(ctx, expression)
		if err != nil {
			t.Fatal(err)
		}
		var value bool
		_ = json.Unmarshal(raw, &value)
		return value
	}
	viewport(560, 430)

	// 1. The owner opens the owner link: the kit's page signs them in and goes to the app's page.
	browser.Open(server.OwnerLink())
	browser.Wait(`document.querySelectorAll('.lists-panel li').length === 5`)
	shoot("page.png")

	// 2. The Harbor theme plugin added and applied; the browser prefers dark, so the page takes Harbor.
	if err := os.CopyFS(filepath.Join(root, "harbor"), os.DirFS(filepath.Join(here, "later", "harbor"))); err != nil {
		t.Fatal(err)
	}
	if err := until(func() (bool, error) { return server.Plugins.Component.Marks().Set, nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := server.ApplyChanges(ctx); err != nil {
		t.Fatal(err)
	}
	browser.Emulate("prefers-color-scheme", "dark")
	browser.Wait(`getComputedStyle(document.documentElement).getPropertyValue('--bg').trim() === '#12232e'`)
	shoot("harbor.png")

	// 3. Half a todo typed, then an edit of the lists plugin's web facet applied: a count of the open todos. The panel remounts (new
	//    code), and the half-typed todo is still there: the page keeps it (`usePageState`).
	browser.Run(`document.querySelector('.lists-panel input').focus()`)
	browser.Type("oat mi")
	// A mark on the page's window: a page that reloaded would have lost it.
	browser.Run(`window.marked = true`)
	typed := browser.String(`document.querySelector('.lists-panel input').value`)
	const before = "h(\n\t\t\t\"ul\","
	const after = "h(\"p\", { className: \"lists-open\" }, t(\"open\", items.filter((item) => !item.done).length)),\n\t\th(\n\t\t\t\"ul\","
	file := filepath.Join(root, "lists", "web.ts")
	source, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(strings.Replace(string(source), before, after, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := until(func() (bool, error) { return containsDirty(server.Plugins.Component.Marks().Dirty, "@todo/lists"), nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := server.ApplyChanges(ctx); err != nil {
		t.Fatal(err)
	}
	browser.Wait(`document.querySelector('.lists-open') !== null`)
	browser.Run(`document.querySelector('.lists-panel input').focus()`)
	shoot("edited.png")
	afterEdit := browser.String(`document.querySelector('.lists-panel input').value`)
	say("typed %q; after the edit: %q", typed, afterEdit)
	say("the page did not reload: %t", boolean(`window.marked === true`))

	// 4. The owner chooses French and clears the done todos: the page re-renders in French with the list's notice, the half-typed
	//    todo still there.
	if err := s.chooses(ownerPrincipal, "fr"); err != nil {
		t.Fatal(err)
	}
	if err := s.asRoot(ownerPrincipal, func(ctx host.Ctx) error {
		list, err := ctx.Get(todos, delta.JsonObjectOf("list", "groceries"), host.GetOptions{})
		if err == nil {
			_, err = list.Call("clear", nil, ctx)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	browser.Wait(`document.querySelector('.lists-notice')?.textContent === 'owner a retiré les tâches faites'`)
	browser.Run(`document.querySelector('.lists-panel input').focus()`)
	shoot("french.png")
	say("after choosing French: lang=%q, %q still typed", browser.String(`document.documentElement.lang`), browser.String(`document.querySelector('.lists-panel input').value`))
	say("the page did not reload: %t", boolean(`window.marked === true`))

	// 5. The page records (`startPage({ telemetry: true })`): the owner adds honey, then clicks again with nothing typed; each click is
	//    a trace of the page's, the call under it through the server's relay into the list's worker. The page sends its spans every few
	//    seconds, the worker every second, the server writes every second: read until the worker's span of each click is there, print
	//    honey's trace, read the store with `trace`, then open the viewer.
	browser.Run(`document.querySelector('.lists-panel input').select()`)
	browser.Type("honey")
	shoot("click-before.png")
	const submit = `document.querySelector('.lists-panel form').requestSubmit()`
	browser.Run(submit)
	browser.Wait(`[...document.querySelectorAll('.lists-panel li')].some((item) => item.textContent.startsWith('honey'))`)
	browser.Run(`document.querySelector('.lists-panel input').blur()`)
	shoot("click-after.png")
	browser.Run(submit)
	traces, err := s.written("page.add", 2, "todos.validate")
	if err != nil {
		t.Fatal(err)
	}
	refused, honey := traces[0], traces[1]
	say("adding honey from the page:")
	s.printTelemetry(telemetry.RenderTrace(honey.Spans))
	read := func(args ...string) string {
		var text bytes.Buffer
		if err := traceCommand(args, &text); err != nil {
			t.Fatal(err)
		}
		return text.String()
	}
	say("trace <data directory> --name page.add --principal owner:")
	s.printTelemetry(read(dataDir, "--name", "page.add", "--principal", "owner"))
	say("trace <data directory> <the refused one's id>:")
	s.printTelemetry(read(dataDir, refused.TraceID))
	viewport(900, 640)
	browser.Open(s.origin + "/traces#" + url.PathEscape(honey.TraceID))
	browser.Wait(fmt.Sprintf(`document.querySelectorAll('.traces-tree li').length === %d`, len(honey.Spans)))
	shoot("traces.png")
	hosts := browser.String(`String(new Set([...document.querySelectorAll('.traces-tree .traces-host')].map((tag) => tag.textContent)).size)`)
	say("the viewer shows page.add: %d spans on %s hosts", len(honey.Spans), hosts)

	// 6. The traces for Ada (`user:u1`, the app's `principalAttributes` names her): her todo of the start, a call of the server's root
	//    acting as her, recorded by the list's worker with her name.
	browser.Run(`document.querySelector('.traces-filter').focus()`)
	browser.Type("user:u1")
	browser.Wait(`document.querySelector('.traces nav a small')?.textContent.includes('ada')`)
	browser.Run(`document.querySelector('.traces nav a').click()`)
	browser.Wait(`document.querySelectorAll('.traces-tree li').length > 0`)
	shoot("traces-ada.png")
	say("the viewer for user:u1 lists %s", browser.String(`[...new Set([...document.querySelectorAll('.traces nav a')].map((a) => a.querySelector('span').textContent + ' for ' + a.querySelectorAll('small')[0].textContent.split(' · ').at(-1)))].join(', ')`))
}

func containsDirty(dirty []string, name string) bool {
	return slices.Contains(dirty, name)
}

// pngSize is the size of the PNG file at path.
func pngSize(t *testing.T, path string) [2]int {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	config, err := png.DecodeConfig(file)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return [2]int{config.Width, config.Height}
}

func TestTheTutorialTakesTheScreenshots(t *testing.T) {
	out := os.Getenv("TODO_SCREENS_OUT")
	if out == "" {
		out = t.TempDir()
	}
	var printed bytes.Buffer
	screenshots(t, out, &printed)
	shown := strings.ReplaceAll(printed.String(), out, "docs/chord/screens")
	if os.Getenv("TODO_SCREENS_OUT") != "" {
		if err := os.WriteFile(filepath.Join(out, "printed.txt"), []byte(shown), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	outputs, values := marked(bookBlocks(t), "output")
	count := 0
	for i, b := range outputs {
		if values[i] == "screenshots" {
			expectPrinted(t, b, shown)
			count++
		}
	}
	if count == 0 {
		t.Fatal("the book shows no output of the screenshots")
	}
	for name, size := range screens {
		if got := pngSize(t, filepath.Join(out, name)); got != size {
			t.Errorf("%s taken at %v, want %v", name, got, size)
		}
		if got := pngSize(t, filepath.Join("..", "..", "docs", "chord", "screens", name)); got != size {
			t.Errorf("the book's %s is %v, want %v", name, got, size)
		}
	}
}
