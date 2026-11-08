package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord/nextnode/internal/browsertest"
)

// Ports packages/chord/test/node/web/styles.test.ts (Pi main c23b9ec1): a page's plugin stylesheets in a real browser, every frame
// recorded where it matters. A new stylesheet is fetched inactive before its plugin reloads and applied with its new code (a slow
// one with a renamed class: no frame unstyled), beside its plugin's old one (the cascade between plugins kept); a plugin whose new
// script fails never shows its new stylesheet, not even while another plugin's loads; a stylesheet that fails is reported, the new
// code applied, and loaded again by the next update; one past its 10 s that loads later applies; a stalled one ends the wait at
// 10 s, and at the client's stop, which removes what it prepared; a reconnect tries a failed plugin's script again with its
// stylesheet; and the known limitation for UI an object contributes while it restarts. Requests for plugin stylesheets are held
// back through DevTools (`Fetch`).

// partPlugin is a web plugin contributing a part `<p id="<id>">` with text, of the CSS module's class css names first; throws
// makes its script throw unless the page set `globalThis.partReady`.
func partPlugin(id, text, css string, throws ...bool) map[string]string {
	name := cssClass(css)
	lines := []string{
		`import { defineFacet } from "@test/app/v1";`,
		`import { Parts } from "@test/app/v1/web";`,
		`import { createElement } from "react";`,
		`import styles from "./part.module.css";`,
	}
	if len(throws) > 0 && throws[0] {
		lines = append(lines, `if (!(globalThis as Record<string, unknown>).partReady) throw new Error("not ready");`)
	}
	lines = append(lines,
		`export default defineFacet((_host, self) =>`,
		"\tself.contribute(Parts, () => createElement(\"p\", { id: "+quoted(id)+", className: styles."+name+" }, "+quoted(text)+")),",
		`);`,
	)
	return map[string]string{"web.ts": strings.Join(lines, "\n"), "part.module.css": css}
}

// cssClass is the first class a stylesheet names.
func cssClass(css string) string {
	start := strings.Index(css, ".") + 1
	end := start
	for end < len(css) && css[end] >= 'a' && css[end] <= 'z' {
		end++
	}
	return css[start:end]
}

// sharedPlugin is a web plugin contributing a part `<p id="<id>" class="shared">` styled by its global stylesheet css.
func sharedPlugin(id, css string) map[string]string {
	return map[string]string{
		"web.ts": strings.Join([]string{
			`import { defineFacet } from "@test/app/v1";`,
			`import { Parts } from "@test/app/v1/web";`,
			`import { createElement } from "react";`,
			`import "./shared.css";`,
			`export default defineFacet((_host, self) =>`,
			"\tself.contribute(Parts, () => createElement(\"p\", { id: " + quoted(id) + ", className: \"shared\" }, \"shared\")),",
			`);`,
		}, "\n"),
		"shared.css": css,
	}
}

// objectPlugin is a web plugin whose part `<p id="<id>">` an object contributes, the object's routine taking a second to end when
// the object restarts (its old contribution stays until its successor is live, design §21).
func objectPlugin(id, text, css string) map[string]string {
	name := cssClass(css)
	return map[string]string{
		"web.ts": strings.Join([]string{
			`import { defineFacet, local } from "@test/app/v1";`,
			`import { Parts } from "@test/app/v1/web";`,
			`import { createElement } from "react";`,
			`import styles from "./part.module.css";`,
			`const Part = local("test.object-part", { version: 1, params: {}, members: {} });`,
			`export default defineFacet((host) => host.serve(Part, (_params, self) => {`,
			`	self.onLive(async (ctx) => {`,
			`		await new Promise((resolve) => ctx.abortSignal.addEventListener("abort", () => setTimeout(resolve, 1000), { once: true }));`,
			`	});`,
			"\tself.contribute(Parts, () => createElement(\"p\", { id: " + quoted(id) + ", className: styles." + name + " }, " + quoted(text) + "));",
			`	return {};`,
			`}));`,
		}, "\n"),
		"part.module.css": css,
	}
}

// holding is what the page does with a request for a plugin stylesheet: let it through after delay, fail it (a network error),
// answer an HTTP error status, or hold it.
type holding struct {
	kind   string // "delay", "fail", "status", "stall"
	delay  time.Duration
	status int
}

// styleBrowser is a browser whose requests for plugin stylesheets the tests hold back.
type styleBrowser struct {
	t       *testing.T
	browser *browsertest.Browser
	mu      sync.Mutex
	stalled []string
}

func newStyleBrowser(t *testing.T) *styleBrowser {
	return &styleBrowser{t: t, browser: browsertest.Start(t)}
}

// call is a DevTools call that failing is no failure of the test (a request already gone).
func (s *styleBrowser) call(method string, params any) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, _ = s.browser.Call(ctx, method, params)
}

// hold holds back the page's requests for plugin stylesheets whose URL contains plugin as how says; it is let go when the test
// ends too.
func (s *styleBrowser) hold(plugin string, how holding) {
	s.t.Helper()
	s.browser.On("Fetch.requestPaused", func(params json.RawMessage) {
		var paused struct {
			RequestID string `json:"requestId"`
			Request   struct {
				URL string `json:"url"`
			} `json:"request"`
		}
		if json.Unmarshal(params, &paused) != nil {
			return
		}
		// The driver's reading goroutine must not wait for DevTools: each request is handled on its own.
		go func() {
			pass := func() { s.call("Fetch.continueRequest", map[string]any{"requestId": paused.RequestID}) }
			switch {
			case !strings.Contains(paused.Request.URL, plugin):
				pass()
			case how.kind == "fail":
				s.call("Fetch.failRequest", map[string]any{"requestId": paused.RequestID, "errorReason": "Failed"})
			case how.kind == "status":
				s.call("Fetch.fulfillRequest", map[string]any{
					"requestId": paused.RequestID, "responseCode": how.status,
					"responseHeaders": []map[string]string{{"name": "Content-Type", "value": "text/css"}}, "body": "",
				})
			case how.kind == "delay":
				time.AfterFunc(how.delay, pass)
			default:
				s.mu.Lock()
				s.stalled = append(s.stalled, paused.RequestID)
				s.mu.Unlock()
			}
		}()
	})
	s.call("Fetch.enable", map[string]any{"patterns": []map[string]string{{"urlPattern": "*/_chord/web/plugin/*.css"}}})
	s.t.Cleanup(s.release)
}

// release lets plugin stylesheets through again, the held ones too.
func (s *styleBrowser) release() {
	s.mu.Lock()
	stalled := s.stalled
	s.stalled = nil
	s.mu.Unlock()
	for _, id := range stalled {
		s.call("Fetch.continueRequest", map[string]any{"requestId": id})
	}
	s.browser.On("Fetch.requestPaused", nil)
	s.call("Fetch.disable", nil)
}

// started is a web app with plugins at port (a free one when 0), the browser signed in and its parts shown.
func (s *styleBrowser) started(files map[string]map[string]string, port ...int) started {
	s.t.Helper()
	at := 0
	if len(port) > 0 {
		at = port[0]
	}
	if at == 0 {
		at = freePort(s.t)
	}
	app := webBrowserApp(s.t, versionedEntry(s.t, "1", ""), at, files, nil)
	signIn(s.browser, app.app.OwnerLink())
	s.browser.Wait(`document.querySelectorAll("#parts > *").length === ` + strconv.Itoa(len(files)))
	return app
}

// writeFiles writes files into plugin dir of app's root.
func writeFiles(t testing.TB, app started, dir string, files map[string]string) {
	t.Helper()
	for name, text := range files {
		if err := os.WriteFile(filepath.Join(app.root, dir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// edit writes files into plugin dir of app's root and applies the changes once the plugin is dirty.
func edit(t testing.TB, app started, dir string, files map[string]string) {
	t.Helper()
	writeFiles(t, app, dir, files)
	untilDirty(t, app.app, "@test/"+dir)
	if _, err := app.app.ApplyChanges(background); err != nil {
		t.Fatal(err)
	}
}

// color is the page expression of the computed colour of the element with id.
func color(id string) string {
	return "getComputedStyle(document.getElementById(" + quoted(id) + ")).color"
}

// recordFrames records in every frame the text and colour of the element with id (`none` without one) into `frames`.
func (s *styleBrowser) recordFrames(id string) {
	s.browser.Run(`globalThis.frames = []; globalThis.recording = true; (function frame() { if (!recording) return; const element = document.getElementById(` + quoted(id) + `); frames.push(element === null ? "none" : element.textContent + " " + getComputedStyle(element).color); requestAnimationFrame(frame); })()`)
}

// framesUntil waits until the recorder sampled sample, then stops it: the frames recorded.
func (s *styleBrowser) framesUntil(sample string) []string {
	s.t.Helper()
	s.browser.Wait("frames.includes(" + quoted(sample) + ")")
	s.browser.Run("new Promise((resolve) => requestAnimationFrame(() => requestAnimationFrame(resolve)))")
	return browserStrings(s.t, s.browser, "(globalThis.recording = false, frames)")
}

// linksOf is the page expression of the `href`s of the stylesheet links of plugin dir.
func linksOf(dir string) string {
	return `Array.from(document.querySelectorAll('link[href*="@test~` + dir + `."]'), (link) => link.getAttribute("href") + (link.media ? " " + link.media : ""))`
}

// distinct is values without repeats, in order of first appearance (`[...new Set(frames)]`).
func distinct(values []string) []string {
	var seen []string
	for _, value := range values {
		if !contains(seen, value) {
			seen = append(seen, value)
		}
	}
	return seen
}

func contains(values []string, value string) bool {
	return slices.Contains(values, value)
}

func count(values []string, value string) int {
	n := 0
	for _, each := range values {
		if each == value {
			n++
		}
	}
	return n
}

func TestAPagesPluginStylesheetsLoadASlowNewStylesheetBeforeTheReloadARenamedClassIsStyledInItsFirstFrameTheOldOneNeverMissing(t *testing.T) {
	s := newStyleBrowser(t)
	app := s.started(map[string]map[string]string{"box": partPlugin("part", "v1", ".box { color: rgb(1, 2, 3); }")})
	s.browser.Wait(color("part") + ` === "rgb(1, 2, 3)"`)
	s.hold("box", holding{kind: "delay", delay: 1500 * time.Millisecond})
	s.recordFrames("part")
	edit(t, app, "box", partPlugin("part", "v2", ".frame { color: rgb(4, 5, 6); }"))
	frames := s.framesUntil("v2 rgb(4, 5, 6)")
	if got := distinct(frames); !reflect.DeepEqual(got, []string{"v1 rgb(1, 2, 3)", "v2 rgb(4, 5, 6)"}) {
		t.Fatalf("frames = %q: a frame was unstyled or missing", got)
	}
	// The old code kept showing (styled) while the new sheet loaded.
	if n := count(frames, "v1 rgb(1, 2, 3)"); n <= 10 {
		t.Fatalf("the old code showed in %d frames while the new sheet loaded, want more than 10", n)
	}
	// The old one goes once the page has drawn the new code.
	s.browser.Wait("(" + linksOf("box") + ").length === 1")
}

func TestAPagesPluginStylesheetsInsertAPluginsNewStylesheetBesideItsOldOneTheCascadeBetweenPluginsStays(t *testing.T) {
	s := newStyleBrowser(t)
	app := s.started(map[string]map[string]string{
		"a": sharedPlugin("part-a", ".shared { color: rgb(200, 0, 0); }"),
		"b": sharedPlugin("part-b", ".shared { color: rgb(0, 0, 200); }"),
	})
	// `b` comes after `a`: its rule wins for every `.shared`.
	s.browser.Wait(color("part-a") + ` === "rgb(0, 0, 200)"`)
	before := browserStrings(t, s.browser, linksOf("a"))[0]
	// Only `a`'s stylesheet changes (its script does not): a new stylesheet all the same.
	edit(t, app, "a", map[string]string{"shared.css": ".shared { color: rgb(200, 0, 0); border-top: 1px solid; }"})
	s.browser.Wait("(" + linksOf("a") + ").length === 1 && (" + linksOf("a") + ")[0] !== " + quoted(before))
	if got := s.browser.String(color("part-a")); got != "rgb(0, 0, 200)" {
		t.Fatalf("part-a colour = %s: the cascade between plugins changed", got)
	}
	if got := s.browser.String(`getComputedStyle(document.getElementById("part-a")).borderTopStyle`); got != "solid" {
		t.Fatalf("border-top-style = %s, want the new sheet's solid", got)
	}
}

func TestAPagesPluginStylesheetsNeverShowTheNewStylesheetOfAPluginWhoseNewScriptFailsNotWhileAnotherPluginsLoadsEither(t *testing.T) {
	s := newStyleBrowser(t)
	app := s.started(map[string]map[string]string{
		"c": partPlugin("part-c", "v1", ".c { color: rgb(7, 7, 7); }"),
		"d": partPlugin("part-d", "v1", ".d { color: rgb(1, 1, 1); }"),
	})
	s.browser.Wait(color("part-c") + ` === "rgb(7, 7, 7)"`)
	before := browserStrings(t, s.browser, linksOf("c"))
	// One rollout: `c`'s new script fails, `d`'s stylesheet takes a second.
	s.hold("@test~d.", holding{kind: "delay", delay: time.Second})
	s.recordFrames("part-c")
	writeFiles(t, app, "c", partPlugin("part-c", "v2", ".c { color: rgb(8, 8, 8); }", true))
	untilDirty(t, app.app, "@test/c")
	edit(t, app, "d", partPlugin("part-d", "v2", ".d { color: rgb(2, 2, 2); }"))
	s.browser.Wait(`document.getElementById("part-d").textContent === "v2"`)
	frames := s.framesUntil("v1 rgb(7, 7, 7)")
	if got := distinct(frames); !reflect.DeepEqual(got, []string{"v1 rgb(7, 7, 7)"}) {
		t.Fatalf("frames = %q: the failed plugin's new sheet showed", got)
	}
	if got := browserStrings(t, s.browser, linksOf("c")); !reflect.DeepEqual(got, before) {
		t.Fatalf("links of c = %q, were %q", got, before)
	}
	if kinds := browserStrings(t, s.browser, "globalThis.errors.map(({ kind }) => kind)"); !contains(kinds, "plugins") {
		t.Fatalf("errors = %q, want one of kind plugins", kinds)
	}
}

func TestAPagesPluginStylesheetsReportAStylesheetThatFailsApplyTheNewCodeAndLoadTheStylesheetAgainWithTheNextUpdate(t *testing.T) {
	s := newStyleBrowser(t)
	app := s.started(map[string]map[string]string{
		"e": partPlugin("part-e", "v1", ".e { color: rgb(1, 1, 1); }"),
		"f": partPlugin("part-f", "v1", ".f { color: rgb(3, 3, 3); }"),
	})
	s.hold("@test~e.", holding{kind: "fail"})
	edit(t, app, "e", partPlugin("part-e", "v2", ".e { color: rgb(2, 2, 2); }"))
	s.browser.Wait(`document.getElementById("part-e").textContent === "v2"`)
	failed := browserStringTable(t, s.browser, `globalThis.errors.filter((error) => error.kind === "stylesheet_failed").map(({ plugin, reason }) => [plugin, reason])`)
	if !reflect.DeepEqual(failed, [][]string{{"@test/e", "error"}}) {
		t.Fatalf("stylesheet failures = %v", failed)
	}
	if got := s.browser.String(color("part-e")); got != "rgb(0, 0, 0)" {
		t.Fatalf("part-e colour = %s, want unstyled", got)
	}
	// The server is reachable again; another plugin's update loads `e`'s stylesheet (the same URL) again.
	s.release()
	edit(t, app, "f", partPlugin("part-f", "v2", ".f { color: rgb(4, 4, 4); }"))
	s.browser.Wait(color("part-e") + ` === "rgb(2, 2, 2)"`)
	s.browser.Wait("(" + linksOf("e") + ").length === 1")
}

func TestAPagesPluginStylesheetsLoadAStylesheetThatFailedAtThePagesFirstLoadHTTP503AgainWithTheNextUpdate(t *testing.T) {
	s := newStyleBrowser(t)
	s.hold("@test~m.", holding{kind: "status", status: 503})
	app := s.started(map[string]map[string]string{
		"m": partPlugin("part-m", "v1", ".m { color: rgb(6, 6, 6); }"),
		"n": partPlugin("part-n", "v1", ".n { color: rgb(1, 1, 1); }"),
	})
	if got := s.browser.String(color("part-m")); got != "rgb(0, 0, 0)" {
		t.Fatalf("part-m colour = %s, want unstyled", got)
	}
	// The server answers again; another plugin's update loads `m`'s stylesheet (the same URL) again.
	s.release()
	edit(t, app, "n", partPlugin("part-n", "v2", ".n { color: rgb(2, 2, 2); }"))
	s.browser.Wait(color("part-m") + ` === "rgb(6, 6, 6)"`)
	s.browser.Wait("(" + linksOf("m") + ").length === 1")
}

func TestAPagesPluginStylesheetsApplyAStylesheetThatLoadsAfterItsTenSecondsWithoutAnotherUpdate(t *testing.T) {
	s := newStyleBrowser(t)
	app := s.started(map[string]map[string]string{"g": partPlugin("part-g", "v1", ".g { color: rgb(1, 1, 1); }")})
	s.hold("@test~g.", holding{kind: "delay", delay: 12 * time.Second})
	edit(t, app, "g", partPlugin("part-g", "v2", ".g { color: rgb(5, 5, 5); }"))
	s.browser.Wait(`document.getElementById("part-g").textContent === "v2"`, 30*time.Second)
	reasons := browserStrings(t, s.browser, `globalThis.errors.filter((error) => error.kind === "stylesheet_failed").map(({ reason }) => reason)`)
	if !reflect.DeepEqual(reasons, []string{"timeout"}) {
		t.Fatalf("stylesheet failures = %q, want [timeout]", reasons)
	}
	s.browser.Wait(color("part-g")+` === "rgb(5, 5, 5)"`, 30*time.Second)
}

func TestAPagesPluginStylesheetsEndTheWaitForAStalledStylesheetAfterTenSecondsAndAtTheClientsStopWhichRemovesWhatItPrepared(t *testing.T) {
	s := newStyleBrowser(t)
	app := s.started(map[string]map[string]string{"h": partPlugin("part-h", "v1", ".h { color: rgb(3, 3, 3); }")})
	s.hold("@test~h.", holding{kind: "stall"})
	start := time.Now()
	edit(t, app, "h", partPlugin("part-h", "v2", ".h { color: rgb(4, 4, 4); }"))
	s.browser.Wait(`document.getElementById("part-h").textContent === "v2"`, 30*time.Second)
	if took := time.Since(start); took <= 9*time.Second {
		t.Fatalf("the wait for a stalled stylesheet ended after %v, want its 10 s", took)
	}
	// Stalled again, then the client stops: its update ends there, the new code never loaded.
	edit(t, app, "h", partPlugin("part-h", "v3", ".h { color: rgb(5, 5, 5); }"))
	s.browser.Wait("(" + linksOf("h") + `).some((link) => link.endsWith(" not all"))`)
	before := browserStrings(t, s.browser, linksOf("h"))
	stopped := browserNumber(t, s.browser, "(async () => { const at = performance.now(); await globalThis.stop(); return performance.now() - at; })()")
	if stopped >= 2000 {
		t.Fatalf("the client's stop took %v ms, want under 2000", stopped)
	}
	// The prepared link went; the stalled requests finishing now change nothing.
	var wantKept []string
	for _, link := range before {
		if !strings.HasSuffix(link, " not all") {
			wantKept = append(wantKept, link)
		}
	}
	kept := browserStrings(t, s.browser, linksOf("h"))
	if !reflect.DeepEqual(kept, wantKept) {
		t.Fatalf("links of h after the stop = %q, want %q", kept, wantKept)
	}
	s.release()
	// Time for the released requests to finish in the page (a half second of its own clock).
	s.browser.Run("new Promise((resolve) => setTimeout(resolve, 500))")
	if got := browserStrings(t, s.browser, linksOf("h")); !reflect.DeepEqual(got, kept) {
		t.Fatalf("links of h after the stalled requests finished = %q, want %q", got, kept)
	}
}

func TestAPagesPluginStylesheetsTryAFailedPluginsScriptAgainAtAReconnectWithItsStylesheetNeverOneWithoutTheOther(t *testing.T) {
	s := newStyleBrowser(t)
	port := freePort(t)
	first := s.started(map[string]map[string]string{"k": partPlugin("part-k", "v1", ".k { color: rgb(1, 1, 1); }")}, port)
	s.recordFrames("part-k")
	edit(t, first, "k", partPlugin("part-k", "v2", ".k { color: rgb(2, 2, 2); }", true))
	s.browser.Wait(`globalThis.errors.some((error) => error.kind === "plugins")`)
	if got := s.browser.String(color("part-k")); got != "rgb(1, 1, 1)" {
		t.Fatalf("part-k colour = %s, want the old code's", got)
	}
	// The script may load now; the server restarts with the same set, and the page reconnects.
	s.browser.Run("globalThis.partReady = true")
	if err := first.app.Close(background); err != nil {
		t.Fatal(err)
	}
	webBrowserApp(t, versionedEntry(t, "1", ""), port, nil, func(o *AppOptions) { o.DataDir, o.Root = first.dataDir, first.root })
	frames := s.framesUntil("v2 rgb(2, 2, 2)")
	for _, frame := range frames {
		if frame != "none" && frame != "v1 rgb(1, 1, 1)" && frame != "v2 rgb(2, 2, 2)" {
			t.Fatalf("a frame showed %q: a script without its stylesheet or the reverse", frame)
		}
	}
}

// A known limitation, kept visible: an object keeps its old contribution until its successor is live (design §21), but the old
// stylesheet is retired once the page drew a frame after the reload. If this starts failing because the old part stays styled,
// retirement learned when a plugin's restarts end: update the README, design §21 and this test.
func TestAPagesPluginStylesheetsShowUIAnObjectContributesUnstyledWhileThatObjectRestartsKnownLimitation(t *testing.T) {
	s := newStyleBrowser(t)
	app := s.started(map[string]map[string]string{"obj": objectPlugin("part-o", "v1", ".old { color: rgb(1, 2, 3); }")})
	s.browser.Wait(color("part-o") + ` === "rgb(1, 2, 3)"`)
	s.recordFrames("part-o")
	edit(t, app, "obj", objectPlugin("part-o", "v2", ".fresh { color: rgb(4, 5, 6); }"))
	frames := s.framesUntil("v2 rgb(4, 5, 6)")
	if !contains(frames, "v1 rgb(0, 0, 0)") {
		t.Fatalf("frames = %q, want one with the old part unstyled (the known limitation)", distinct(frames))
	}
}
