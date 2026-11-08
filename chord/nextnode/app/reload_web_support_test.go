package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/nextnode/internal/browsertest"
	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins"
)

// The reload suite's browser kit (Pi's test/node/fixtures/reload/{app,plugins}.ts over the vendored base bundle
// fixtures/reload/{main,entry,web-api}.ts): the suite's app with the web surface, a real Chromium signed in as the owner, the web
// plugins' source, and `reloadTab`, Pi's `ReloadPage` over the DevTools driver (chord/nextnode/internal/browsertest). The tests are
// skipped without a Chromium (`CHROMIUM_BIN`).

// reloadWebCounter is the counter as a web plugin shows it.
type reloadWebCounter struct{ Placement, Contract string }

// webCode is how a version of a web plugin is written (Pi's WebCode).
type webCode struct {
	// Name is its section's `data-view` and its probe name; Code what its view says it is (`<name>:<code>`).
	Name, Code string
	// Counter is the counter it shows, through its own copy of the contract (an old page keeps its copy); nil: no reference.
	Counter *reloadWebCounter
	// Summary shows the server's summary of the counter instead, through its own contract copy.
	Summary bool
	// Broken is a broken version: "build" (its build fails) or "evaluation" (its script throws).
	Broken string
	// Hidden contributes no view (its contributions changed shape: none now).
	Hidden bool
}

// reloadWebPlugin is a web plugin's files: its view (the counter's label, count and state, a draft and a kept input) and contract copy.
func reloadWebPlugin(t testing.TB, code webCode) map[string]string {
	t.Helper()
	name := strconv.Quote(code.Name)
	lines := []string{"// The " + code.Name + " web plugin, code " + code.Code + ".",
		`import { createElement as h, useEffect, useState } from "react";`,
		`import { defineFacet } from "@test/app/v1";`,
		`import { probe, useHold, usePageState, useRefState, useWatch, Views } from "@test/app/v1/web";`}
	if code.Counter != nil {
		counter := "Counter"
		if code.Summary {
			counter = "Summary as Counter"
		}
		lines = append(lines, `import { `+counter+` } from "./contract.ts";`)
	}
	if code.Broken == "build" {
		lines = append(lines, "const broken = ;")
	}
	if code.Broken == "evaluation" {
		lines = append(lines, `throw new Error("the web plugin's new code throws when evaluated");`)
	}
	if code.Counter != nil {
		lines = append(lines, "probe.contracts["+name+"] = Counter;")
	}
	lines = append(lines, "function Shown() {")
	if code.Counter == nil {
		lines = append(lines, "\treturn null;")
	} else {
		lines = append(lines,
			"\tconst ref = useHold(Counter, { id: \"p1\" });",
			"\tconst label = useWatch(ref?.label);",
			"\tconst count = useWatch(ref?.count);",
			"\tconst state = useRefState(ref);",
			"\tuseEffect(() => { if (ref !== undefined) probe.track("+name+", ref, \"label\"); }, [ref]);",
			"\treturn h(\"span\", null,",
			"\t\th(\"span\", { className: \"label\" }, label === undefined ? \"none\" : typeof label === \"string\" ? label : JSON.stringify(label)),",
			"\t\th(\"span\", { className: \"count\" }, String(count ?? \"none\")),",
			"\t\th(\"span\", { className: \"state\" }, state?.kind ?? \"none\"),",
			"\t);")
	}
	lines = append(lines, "}", "function View() {",
		"\tconst [draft, setDraft] = useState(\"\");",
		"\tconst [kept, setKept] = usePageState("+strconv.Quote(code.Name+".kept")+", \"\");",
		"\treturn h(\"div\", null,",
		"\t\th(\"span\", { className: \"code\" }, "+strconv.Quote(code.Name+":"+code.Code)+"),",
		"\t\th(Shown),",
		"\t\th(\"input\", { className: \"draft\", value: draft, onChange: (event: { target: { value: string } }) => setDraft(event.target.value) }),",
		"\t\th(\"input\", { className: \"kept\", value: kept, onChange: (event: { target: { value: string } }) => setKept(event.target.value) }),",
		"\t);", "}")
	if code.Hidden {
		lines = append(lines, "export default defineFacet(() => {});")
	} else {
		lines = append(lines, "export default defineFacet((_host, self) => self.contribute(Views, { name: "+name+", component: View }));")
	}
	files := map[string]string{"web.ts": strings.Join(lines, "\n")}
	if code.Counter != nil {
		if code.Summary {
			files["contract.ts"] = reloadTemplate(t, "summary-contract.ts", nil)
		} else {
			files["contract.ts"] = reloadTemplate(t, "counter-contract.ts", map[string]any{"Placement": code.Counter.Placement, "Version": code.Counter.Contract})
		}
	}
	return files
}

// reloadWebCounterV1 is the counter on the workers, as the web plugins show it.
var reloadWebCounterV1 = &reloadWebCounter{Placement: "worker", Contract: "v1"}

// reloadEntry writes the suite's base bundle entry (Pi's `versionedMain`) with version in dir (a new directory when empty): its
// path. A new version in the same directory is another base bundle.
func reloadEntry(t testing.TB, version, dir string) string {
	t.Helper()
	if dir == "" {
		dir = t.TempDir()
	}
	main := surfacesFixture(t, "reload", "main.ts")
	text := "import { start } from " + quoted(main) + ";\nglobalThis.version = " + quoted(version) + ";\nstart();\n"
	path := filepath.Join(dir, "main.ts")
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// startReloadWebApp starts the suite's app (project workers, the reload timeouts) with the web surface on port and entry as its base
// bundle's entry; more adjusts the options (a restart names the data directory and the root of the app it follows).
func startReloadWebApp(t testing.TB, entry string, port int, files map[string]map[string]string, more func(*AppOptions)) started {
	t.Helper()
	return webBrowserApp(t, entry, port, files, func(o *AppOptions) {
		o.Workers = []AppWorkerKind{testWorker(t, project, "")}
		o.Timeouts = reloadTimeouts
		// The reload suite's web API (its `Views` registry and the probe) replaces the browser tests' own.
		o.Kinds[len(o.Kinds)-1].Packages = plugins.HostPackages{"react": "", "react/jsx-runtime": "", "@test/app/v1/web": surfacesFixture(t, "reload", "web-api.ts")}
		if more != nil {
			more(o)
		}
	})
}

// startReload starts the suite's app with the web surface (the suite's base bundle at version 1, a free port) and plugins (directory name
// to files; each plugin is `@test/<dir>`) and project workers: Pi's `startReloadApp`.
func startReload(t testing.TB, plugins map[string]map[string]string) started {
	t.Helper()
	return startReloadWebApp(t, reloadEntry(t, "1", ""), freePort(t), plugins, nil)
}

// reloadTab is Pi's `ReloadPage`: the page as the tests see and drive it: its DOM, the probe's records, calls from its references.
type reloadTab struct {
	t       testing.TB
	browser *browsertest.Browser
}

// startReloadTab starts a Chromium for the test.
func startReloadTab(t testing.TB) *reloadTab {
	t.Helper()
	r := &reloadTab{t: t, browser: browsertest.Start(t)}
	// A failed test says what the page showed.
	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var shown string
		if err := r.browser.Evaluate(ctx, `JSON.stringify({ connection: globalThis.page?.connection, errors: globalThis.page?.errors, loads: globalThis.page?.loads, views: document.getElementById("views")?.innerText, states: globalThis.reload?.states, wslog: globalThis.wslog, scripts: [...document.scripts].map((s) => s.src) })`, &shown); err != nil {
			shown = err.Error()
		}
		t.Logf("the page at the end: %s", shown)
	})
	return r
}

// evalAs is expression's value in the page, as a T.
func evalAs[T any](r *reloadTab, expression string) T {
	r.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), reloadWait)
	defer cancel()
	var value T
	if err := r.browser.Evaluate(ctx, expression, &value); err != nil {
		r.t.Fatal(err)
	}
	return value
}

// signIn opens the owner link (signed in) and waits until the page shows its views and is connected.
func (r *reloadTab) signIn(s started, views ...string) {
	r.t.Helper()
	r.browser.Open(s.app.OwnerLink())
	r.browser.Wait(`globalThis.page?.connection === "connected"`)
	for _, view := range views {
		r.browser.Wait(`document.querySelector('[data-view="` + view + `"]')`)
	}
}

// signInAgain signs in with a new owner browser session (the owner link is single use: a second page needs its own session) and
// waits as signIn does, and until each view (each holding the counter) registered its reference.
func (r *reloadTab) signInAgain(s started, views ...string) {
	r.t.Helper()
	sessions, err := s.app.Host.Get(background, Sessions, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		r.t.Fatal(err)
	}
	defer sessions.Release()
	issued, err := sessions.Call("issue", delta.JsonObjectOf("principal", testOwner, "label", "second page", "kind", "browser"), background)
	if err != nil {
		r.t.Fatal(err)
	}
	pair, _, _ := strings.Cut(issued.(*delta.JsonObject).Value("cookie").(string), ";")
	name, value, _ := strings.Cut(pair, "=")
	url := "http://127.0.0.1:" + strconv.Itoa(s.port) + "/"
	ctx, cancel := context.WithTimeout(context.Background(), reloadWait)
	defer cancel()
	if _, err := r.browser.Call(ctx, "Network.setCookie", map[string]any{"name": name, "value": value, "url": url}); err != nil {
		r.t.Fatal(err)
	}
	r.browser.Open(url)
	r.browser.Wait(`globalThis.page?.connection === "connected"`)
	for _, view := range views {
		r.browser.Wait(`document.querySelector('[data-view="` + view + `"]')`)
		r.browser.Wait(`globalThis.reload.refs[` + quoted(view) + `] !== undefined`)
	}
}

// viewSelector is the query of selector within view.
func viewSelector(view, selector string) string {
	return `document.querySelector('[data-view="` + view + `"] ` + selector + `')`
}

// text is the text of selector within view; ok is false when it is absent.
func (r *reloadTab) text(view, selector string) (string, bool) {
	r.t.Helper()
	got := evalAs[*string](r, viewSelector(view, selector)+`?.textContent ?? null`)
	if got == nil {
		return "", false
	}
	return *got, true
}

// shows is the text of selector within view (the test fails when it is absent).
func (r *reloadTab) shows(view, selector string) string {
	r.t.Helper()
	text, ok := r.text(view, selector)
	if !ok {
		r.t.Fatalf("view %s has no %s", view, selector)
	}
	return text
}

// value is the value of the input selector within view.
func (r *reloadTab) value(view, selector string) string {
	r.t.Helper()
	return evalAs[string](r, viewSelector(view, selector)+`.value`)
}

// waitText waits until selector within view shows text.
func (r *reloadTab) waitText(view, selector, text string) {
	r.t.Helper()
	r.browser.Wait(viewSelector(view, selector) + `?.textContent === ` + quoted(text))
}

// typeInto types text into the input selector of view.
func (r *reloadTab) typeInto(view, selector, text string) {
	r.t.Helper()
	r.browser.Run(viewSelector(view, selector) + `.focus()`)
	r.browser.Type(text)
}

// states are every state the reference tracked as name went through.
func (r *reloadTab) states(name string) []string {
	r.t.Helper()
	return evalAs[[]string](r, `globalThis.reload.states[`+quoted(name)+`] ?? []`)
}

// waitState waits until the reference tracked as name is in state (as the probe writes it).
func (r *reloadTab) waitState(name, state string) {
	r.t.Helper()
	r.waitStateWithin(name, state, reloadWait)
}

// waitStateWithin is waitState with its own bound.
func (r *reloadTab) waitStateWithin(name, state string, timeout time.Duration) {
	r.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	err := browsertest.Poll(ctx, timeout, name+" in "+state, func(ctx context.Context) (bool, error) {
		var ok bool
		err := r.browser.Evaluate(ctx, `globalThis.reload.states[`+quoted(name)+`]?.at(-1) === `+quoted(state), &ok)
		return ok, err
	})
	if err != nil {
		r.t.Fatalf("%s never reached %s: %v (states %v)", name, state, err, r.states(name))
	}
}

// frames are every value its watched member had (or `failed:<reason>`), as decoded JSON.
func (r *reloadTab) frames(name string) []any {
	r.t.Helper()
	return evalAs[[]any](r, `globalThis.reload.frames[`+quoted(name)+`] ?? []`)
}

// hold holds the object of project id (the server's, with no params, for an empty id) as the web plugin contract registered its contract, as name, its calls parking readyTimeout,
// its member member watched.
func (r *reloadTab) hold(name, contract string, readyTimeout time.Duration, id, member string) {
	r.t.Helper()
	params := []byte("{}")
	if id != "" {
		params, _ = json.Marshal(map[string]string{"id": id})
	}
	r.browser.Run(`globalThis.reload.hold(` + quoted(name) + `, ` + quoted(contract) + `, ` + string(params) + `, ` + strconv.FormatInt(readyTimeout.Milliseconds(), 10) + `, ` + quoted(member) + `)`)
}

// call calls method with args on the reference tracked as name: the number of its answer.
func (r *reloadTab) call(name, method string, args ...any) int {
	r.t.Helper()
	list := []string{quoted(name), quoted(method)}
	for _, arg := range args {
		text, err := json.Marshal(arg)
		if err != nil {
			r.t.Fatal(err)
		}
		list = append(list, string(text))
	}
	return evalAs[int](r, `globalThis.reload.call(`+strings.Join(list, ", ")+`)`)
}

// browserResult is a call's answer as the page's probe wrote it.
type browserResult struct {
	Status string          `json:"status"`
	Value  json.RawMessage `json:"value"`
	Code   string          `json:"code"`
	Reason string          `json:"reason"`
}

// answer is the call's answer in the form the tests compare: the status, the value as JSON, the error's code and reason.
func (b browserResult) answer() callResult {
	result := callResult{Status: b.Status, Code: b.Code, Reason: b.Reason}
	if b.Status == "ok" {
		result.Value = string(b.Value)
		if result.Value == "" {
			result.Value = "null"
		}
	}
	return result
}

// peek is call index's answer now (pending or not).
func (r *reloadTab) peek(index int) callResult {
	r.t.Helper()
	return evalAs[browserResult](r, `globalThis.reload.results[`+strconv.Itoa(index)+`]`).answer()
}

// result is call index's answer, once it has one.
func (r *reloadTab) result(index int) callResult {
	r.t.Helper()
	r.browser.Wait(`globalThis.reload.results[`+strconv.Itoa(index)+`].status !== "pending"`, reloadWait)
	return r.peek(index)
}

// loads is how often the page loaded (its base bundle ran) since the browser opened it.
func (r *reloadTab) loads() int { return evalAs[int](r, `globalThis.page.loads`) }

// pageErrors are what the page reported (`startPage`'s `onError`): the kind, and the plugins of a `plugins` report.
type pageError struct {
	Kind    string   `json:"kind"`
	Plugins []string `json:"plugins"`
}

func (r *reloadTab) errors() []pageError {
	r.t.Helper()
	return evalAs[[]pageError](r, `globalThis.page.errors`)
}
