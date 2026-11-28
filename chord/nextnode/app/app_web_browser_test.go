package app

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/sqlite/node"
	"github.com/MichaelKinsy/PiG/chord/telemetrysqlite"
	"github.com/MichaelKinsy/PiG/internal/browsertest"
	"github.com/MichaelKinsy/PiG/telemetry"
)

// Ports packages/chord/test/node/react/hooks.test.ts, web/language.test.ts and the first two cases of web/page.test.ts (Pi main
// c23b9ec1) over a real Chromium (the DevTools driver, internal/browsertest; skipped without one: `CHROMIUM_BIN`).
// The page's third case (a click as the root of one trace through the server into the worker) runs the vendored fixture web/telemetry.ts
// against the app's SQLite telemetry store; web/styles.test.ts in app_web_styles_test.go.
//
// Forced differences: the browser is driven from Go over the DevTools protocol (Pi's `ws` driver, ported as the Go driver); the
// tests wait on the plugins' state through its watch (Pi polls it every 10 ms).

// arabic is a web plugin contributing an Arabic catalog of the page's messages.
var arabic = map[string]string{
	"web.ts": `import { defineFacet, Translations } from "@test/app/v1";
export default defineFacet((_host, self) => self.contribute(Translations, [{ namespace: "test.page", locale: "ar", messages: { hello: "مرحبا" } }]));`,
}

func TestReactBindingsHoldWhileMountedReRenderOnChangeReleaseOnUnmount(t *testing.T) {
	browser := browsertest.Start(t)
	s := webBrowserApp(t, versionedEntry(t, "1", ""), freePort(t), map[string]map[string]string{"notes": panelPlugin("Notes", "#123456")}, nil)
	signIn(browser, s.app.OwnerLink())
	// useRegistry: the plugin's panel.
	browser.Wait(`document.querySelector(".panel")?.textContent === "Notes"`)
	// useHold + useRefState: the counter held and live.
	browser.Wait(`document.getElementById("count")?.dataset.state === "live"`)
	if got := browser.String(`document.getElementById("count").textContent`); got != "0" {
		t.Fatalf("count = %q, want 0", got)
	}
	// useWatch: a change re-renders.
	browser.Run(`document.getElementById("count").click()`)
	browser.Wait(`document.getElementById("count").textContent === "1"`)
	// Unmounted: the reference is released.
	browser.Run(`document.getElementById("toggle").click()`)
	browser.Wait(`document.getElementById("count") === null && globalThis.counterState() === "ended"`)
	// Mounted again: held again.
	browser.Run(`document.getElementById("toggle").click()`)
	browser.Wait(`document.getElementById("count")?.dataset.state === "live"`)
}

func TestReactBindingsKeepPageStateAcrossARemountByANewFunctionANewKeyStartingFresh(t *testing.T) {
	browser := browsertest.Start(t)
	s := webBrowserApp(t, versionedEntry(t, "1", ""), freePort(t), nil, nil)
	signIn(browser, s.app.OwnerLink())
	browser.Run(`document.getElementById("note").focus()`)
	browser.Type("oat mi")
	browser.Wait(`document.getElementById("note").value === "oat mi"`)
	browser.Run(`void (globalThis.mounted = document.getElementById("note"))`)
	// A new component function at the same place: React mounts it anew (a new element), the key keeps the value.
	browser.Run(`document.getElementById("remount").click()`)
	browser.Wait(`document.getElementById("note") !== globalThis.mounted`)
	if got := browser.String(`document.getElementById("note").value`); got != "oat mi" {
		t.Fatalf("note = %q after a remount, want the kept value", got)
	}
	// Another key starts at its initial value.
	browser.Run(`document.getElementById("remount").click()`)
	browser.Wait(`document.getElementById("note").dataset.key === "other"`)
	if got := browser.String(`document.getElementById("note").value`); got != "" {
		t.Fatalf("note = %q for another key, want a fresh one", got)
	}
	// Back to the first key: still kept.
	browser.Run(`document.getElementById("remount").click()`)
	browser.Wait(`document.getElementById("note").dataset.key === "note"`)
	if got := browser.String(`document.getElementById("note").value`); got != "oat mi" {
		t.Fatalf("note = %q back at the first key, want the kept value", got)
	}
}

func TestReactBindingsReRenderMessagesInALanguageChosenLiveKeepingHalfTypedInputs(t *testing.T) {
	browser := browsertest.Start(t)
	s := webBrowserApp(t, versionedEntry(t, "1", ""), freePort(t), nil, nil)
	signIn(browser, s.app.OwnerLink())
	browser.Wait(`document.getElementById("greeting")?.textContent === "Hello, 2 clicks"`)
	browser.Run(`document.getElementById("draft").focus()`)
	browser.Type("oat")
	browser.Run(`document.getElementById("note").focus()`)
	browser.Type("mi")
	browser.Run(`void (globalThis.marked = document.getElementById("draft"))`)
	// The owner chooses German: the page's host follows the pick, the greeting re-renders, nothing reloads.
	chooseLocale(t, s, "de")
	browser.Wait(`document.getElementById("greeting").textContent === "Hallo, 2 Klicks"`)
	if got := browser.String(`document.getElementById("greeting").dataset.locale`); got != "de" {
		t.Fatalf("locale = %q", got)
	}
	if got := browser.String(`document.getElementById("draft").value`); got != "oat" {
		t.Fatalf("draft = %q", got)
	}
	if got := browser.String(`document.getElementById("note").value`); got != "mi" {
		t.Fatalf("note = %q", got)
	}
	if !browserBool(t, browser, `document.getElementById("draft") === globalThis.marked`) {
		t.Fatal("the draft input was replaced: its half-typed text is not kept")
	}
}

// hooks.test.ts "calls the latest listener of useEvents, follows its member and stops at unmount" (Pi 8d30e6306): the page's
// beats, two event members of a local object of the page, subscribed by a component with `useEvents`.
func TestReactBindingsCallTheLatestListenerOfUseEventsFollowItsMemberAndStopAtUnmount(t *testing.T) {
	browser := browsertest.Start(t)
	s := webBrowserApp(t, versionedEntry(t, "1", ""), freePort(t), nil, nil)
	signIn(browser, s.app.OwnerLink())
	browser.Wait(`document.getElementById("beats")?.dataset.state === "live"`)
	beats := func() string { return browser.String(`JSON.stringify(globalThis.beats)`) }
	browser.Run(`globalThis.beat("a", 0)`)
	browser.Wait(`globalThis.beats.length === 1`)
	// A new render's layout effect emits: delivered before its passive effects, to that render's listener. A timer's update
	// is not flushed synchronously, so its passive effects run in a later task.
	browser.Run(`void setTimeout(() => globalThis.relabel("second"))`)
	browser.Wait(`globalThis.beats.length === 2`)
	if got := beats(); got != `["first:0","second:1"]` {
		t.Fatalf("beats = %s, want first:0 then second:1", got)
	}
	// Another member: the first one's subscription stopped.
	browser.Run(`globalThis.switchBeats()`)
	browser.Wait(`document.getElementById("beats").dataset.member === "b"`)
	browser.Run(`globalThis.beat("a", 2); globalThis.beat("b", 3)`)
	browser.Wait(`globalThis.beats.length === 3`)
	if got := browser.String(`globalThis.beats.at(-1)`); got != "second:3" {
		t.Fatalf("the last beat = %q, want second:3", got)
	}
	// Unmounted: nothing more.
	browser.Run(`globalThis.toggleBeats()`)
	browser.Wait(`document.getElementById("beats") === null`)
	browser.Run(`globalThis.beat("b", 4)`)
	time.Sleep(200 * time.Millisecond)
	if got := beats(); got != `["first:0","second:1","second:3"]` {
		t.Fatalf("beats = %s after the unmount, want the three before it", got)
	}
}

// deskMode is the Go form of the desk plugin's local `test.desk.mode` (contracts are data): the mode the desk's admission reads.
var deskMode = mustLocal("test.desk.mode", contracts.LocalDeclaration{Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{
	contracts.Mem("mode", contracts.Str()), contracts.Mem("set", contracts.NewMethod(contracts.Str())),
}})

// deskPlugin is Pi's DESK_PLUGIN (hooks.test.ts): a server plugin serving the fixture's `Desk`, a reader or an editor by `DeskMode`
// (`reader` at first), else denied.
var deskPlugin = map[string]string{
	"server.ts": strings.Join([]string{
		`import { allow, defineFacet, deny, grantable, hostKind, local, method, remote, str } from "@test/app/v1";`,
		`const DeskMode = local("test.desk.mode", { version: 1, params: {}, members: { mode: str(), set: method(str()) } });`,
		`const Desk = remote("test.desk", { version: 1, params: {}, hostAddress: () => hostKind("server")(), state: { text: str() }, methods: { write: grantable(method(str())) } });`,
		`export default defineFacet((h) => {`,
		`	h.serve(DeskMode, (_params, self) => {`,
		`		const mode = self.writable("reader");`,
		`		return { mode: self.derive([mode], ([value]) => value), set: async (next) => { mode.set(next); return null; } };`,
		`	});`,
		`	h.serve(Desk, (_params, _principal, _admitted, self) => {`,
		`		const text = self.writable("a desk");`,
		`		const shown = self.derive([text], ([value]) => value);`,
		`		return { text: shown, write: async (next) => { text.set(next); return null; } };`,
		`	}, {`,
		`		admit: (_params, _principal, self) => self.derive([self.get(DeskMode, {}).mode], ([mode]) =>`,
		`			mode === "editor" ? allow({ granted: ["write"] }) : mode === "reader" ? allow({}) : deny("no access")),`,
		`	});`,
		`});`,
	}, "\n"),
}

// hooks.test.ts "reads a reference in the variant it is live in, following a change of its variant while mounted" (Pi f57cd9484):
// the fixture's `Desk` on the server, shown by the page on request with `useVariant`.
func TestReactBindingsReadAReferenceInTheVariantItIsLiveInFollowingAChangeOfItsVariantWhileMounted(t *testing.T) {
	browser := browsertest.Start(t)
	s := webBrowserApp(t, versionedEntry(t, "1", ""), freePort(t), map[string]map[string]string{"desk": deskPlugin}, nil)
	admit := func(mode string) {
		t.Helper()
		// The plugin's local lives in the server's plugin host: reached through the app's node, as the plugin tests do.
		modes, err := s.app.Node.Get(background, deskMode, delta.NewJsonObject(0), host.GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		defer modes.Release()
		if _, err := modes.Call("set", mode, background); err != nil {
			t.Fatal(err)
		}
	}
	variant := func(name string) string { return `document.getElementById("desk")?.dataset.mode === "` + name + `"` }
	signIn(browser, s.app.OwnerLink())
	browser.Run(`globalThis.showDesk()`)
	browser.Wait(variant("reader"))
	// A reader's reference is no editor's: the click writes nothing.
	browser.Run(`document.getElementById("desk").click()`)
	// Another allowed value while the component is mounted: the same reference, now the editor's.
	admit("editor")
	browser.Wait(variant("editor"))
	if got := browser.String(`document.getElementById("desk").textContent`); got != "a desk" {
		t.Fatalf("desk = %q, want a desk", got)
	}
	browser.Run(`document.getElementById("desk").click()`)
	browser.Wait(`document.getElementById("desk").textContent === "edited"`)
	// Denied: no variant is live. Allowed again as a reader: the reader's.
	admit("none")
	browser.Wait(variant("none"))
	admit("reader")
	browser.Wait(variant("reader"))
}

func TestThePagesLanguageWritesLangAndDirOnTheRootElementFromTheDeviceThenTheUsersPick(t *testing.T) {
	browser := browsertest.Start(t)
	s := webBrowserApp(t, versionedEntry(t, "1", ""), freePort(t), map[string]map[string]string{"arabic": arabic}, nil)
	signIn(browser, s.app.OwnerLink())
	browser.Wait(`document.getElementById("greeting") !== null`)
	// The browser's own languages (English), matched against the catalogs' locales.
	if got := browser.String(`document.documentElement.lang`); got != "en" {
		t.Fatalf("lang = %q, want en", got)
	}
	chooseLocale(t, s, "ar")
	browser.Wait(`document.documentElement.dir === "rtl"`)
	if got := browser.String(`document.documentElement.lang`); got != "ar" {
		t.Fatalf("lang = %q, want ar", got)
	}
	// The key Arabic lacks falls back to the first catalog, key by key.
	if got := browser.String(`document.getElementById("greeting").textContent`); got != "مرحبا, 2 clicks" {
		t.Fatalf("greeting = %q", got)
	}
}

func TestAPageLoadsThePluginsAppliesAnEditInPlaceKeepingThePagesStateAndFollowsTheTheme(t *testing.T) {
	browser := browsertest.Start(t)
	s := webBrowserApp(t, versionedEntry(t, "1", ""), freePort(t), map[string]map[string]string{"notes": panelPlugin("Notes", "#123456")}, nil)
	emulate := func(scheme string) {
		t.Helper()
		browser.Emulate("prefers-color-scheme", scheme)
	}
	emulate("dark")
	signIn(browser, s.app.OwnerLink())
	browser.Wait(`document.querySelector(".panel")?.textContent === "Notes"`)
	browser.Wait(`globalThis.connection === "connected"`)
	accent := func(color string) string {
		return `getComputedStyle(document.documentElement).getPropertyValue("--accent").trim() === "` + color + `"`
	}
	browser.Wait(accent("#123456"))
	// Component state, then a plugin edit rolled out: the page applies it in place.
	browser.Run(`document.getElementById("draft").focus()`)
	browser.Type("half-typed")
	if err := os.WriteFile(filepath.Join(s.root, "notes", "web.ts"), []byte(panelPlugin("Notes v2", "#654321")["web.ts"]), 0o644); err != nil {
		t.Fatal(err)
	}
	untilDirty(t, s.app, "@test/notes")
	result, err := s.app.ApplyChanges(background)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, step := range result.Steps {
		names = append(names, step.Name)
	}
	if !reflect.DeepEqual(names, []string{"server", "web"}) {
		t.Fatalf("steps = %v", names)
	}
	browser.Wait(`document.querySelector(".panel")?.textContent === "Notes v2"`)
	browser.Wait(accent("#654321"))
	if got := browser.String(`document.getElementById("draft").value`); got != "half-typed" {
		t.Fatalf("draft = %q: the edit did not keep the component's state", got)
	}
	if got := browserNumber(t, browser, `globalThis.loads`); got != 1 {
		t.Fatalf("loads = %v, want the page loaded once", got)
	}
	// The colour scheme changes: the light theme (here the fallback), without a reload.
	emulate("light")
	browser.Wait(accent("#0b62d6"))
	if got := browser.String(`getComputedStyle(document.documentElement).colorScheme`); got != "light" {
		t.Fatalf("colorScheme = %q", got)
	}
}

func TestAPageWritesNoThemeOnRootWithThemesFalseSoTheAppsOwnVariablesStand(t *testing.T) {
	browser := browsertest.Start(t)
	s := webBrowserApp(t, versionedEntry(t, "1", "", "{ themes: false }"), freePort(t), map[string]map[string]string{"notes": panelPlugin("Notes", "#123456")}, nil)
	signIn(browser, s.app.OwnerLink())
	browser.Wait(`document.querySelector(".panel")?.textContent === "Notes"`)
	if got := browser.String(`document.documentElement.style.cssText`); got != "" {
		t.Fatalf("cssText = %q, want none", got)
	}
	if got := browser.String(`getComputedStyle(document.documentElement).getPropertyValue("--accent")`); got != "" {
		t.Fatalf("--accent = %q, want none", got)
	}
}

func TestAPageReloadsWhenTheServerComesBackWithAnotherBaseBundle(t *testing.T) {
	browser := browsertest.Start(t)
	port := freePort(t)
	entry := versionedEntry(t, "1", "")
	first := webBrowserApp(t, entry, port, map[string]map[string]string{"notes": panelPlugin("Notes", "#123456")}, nil)
	signIn(browser, first.app.OwnerLink())
	browser.Wait(`globalThis.version === "1" && globalThis.connection === "connected"`)
	firstBase := first.app.Web.Base()
	if err := first.app.Close(background); err != nil {
		t.Fatal(err)
	}
	browser.Wait(`globalThis.connection === "waiting" || globalThis.connection === "connecting"`)
	versionedEntry(t, "2", filepath.Dir(entry))
	second := webBrowserApp(t, entry, port, nil, func(o *AppOptions) { o.DataDir, o.Root = first.dataDir, first.root })
	if second.app.Web.Base() == firstBase {
		t.Fatal("another entry gave the same base hash")
	}
	browser.Wait(`globalThis.version === "2" && globalThis.connection === "connected"`, time.Minute)
	// The panel follows the connection by a frame or two (Opus L6).
	browser.Wait(`document.querySelector(".panel")?.textContent === "Notes"`)
}

var (
	traceDuration = regexp.MustCompile(` \(\+?[\d.]+ms\)`)
	clickSpanID   = regexp.MustCompile(`^browser:[0-9a-f-]{36}:\d+$`)
)

// clickNotesPlugin is the notes plugin, whose Notes has Pi's `attach` and `file` as the contract the page fixture (web/telemetry.ts)
// declares again: the contract's fingerprint must equal the page's.
func clickNotesPlugin() map[string]string {
	return notesPlugin
}

func TestAPageRecordsAClickAsTheRootOfOneTraceThroughTheServerIntoTheWorkerStoredByTheServer(t *testing.T) {
	browser := browsertest.Start(t)
	db, err := node.OpenNodeSqliteDatabase(filepath.Join(t.TempDir(), "t.sqlite"), node.NodeSqliteDatabaseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	store, err := telemetrysqlite.CreateSqliteTelemetry(db, telemetrysqlite.SqliteTelemetryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	s := webBrowserApp(t, surfacesFixture(t, "web", "telemetry.ts"), freePort(t), map[string]map[string]string{"notes": clickNotesPlugin()}, func(o *AppOptions) {
		o.Workers = []AppWorkerKind{testWorker(t, project, "")}
		o.Telemetry = store
	})
	signIn(browser, s.app.OwnerLink())
	browser.Run(`document.getElementById("write").click()`)
	browser.Wait(`globalThis.written === 1`)
	// The page sends every few seconds, the worker every second, the server writes every second.
	name := "page.write"
	var spans []telemetry.StoredSpan
	err = browsertest.Poll(context.Background(), 30*time.Second, "the click's trace", func(context.Context) (bool, error) {
		if err := store.Flush(); err != nil {
			return false, err
		}
		found, err := store.FindTraces(telemetry.TraceQuery{Name: &name})
		if err != nil || len(found.Traces) == 0 {
			return false, err
		}
		spans = found.Traces[0].Spans
		for _, span := range spans {
			if kind, _ := span.Attributes.Get("type"); kind == "test.store" {
				return true, nil
			}
		}
		return false, nil
	})
	if err != nil {
		all, _ := store.FindTraces(telemetry.TraceQuery{})
		var seen []string
		for _, trace := range all.Traces {
			seen = append(seen, traceDuration.ReplaceAllString(telemetry.RenderTrace(trace.Spans), ""))
		}
		t.Fatalf("%v; stored traces:\n%s\npage console: %v", err, strings.Join(seen, "\n--\n"), browser.Console())
	}
	shown := make([]telemetry.StoredSpan, len(spans))
	for index, span := range spans {
		host, ok := span.Attributes.Get("chord.host")
		if !ok {
			host = "?"
		}
		shown[index] = span
		shown[index].Attributes = telemetry.SpanAttributes{{Name: "host", Value: host}}
	}
	var tree []string
	for line := range strings.SplitSeq(traceDuration.ReplaceAllString(telemetry.RenderTrace(shown), ""), "\n") {
		if !strings.Contains(line, "·") {
			tree = append(tree, line)
		}
	}
	// Forced difference (D460): the worker's Go host is a relay to the NodeHost that runs the plugin's types, so its own
	// chord.relay span sits between the server's and the two chord.method spans Pi's worker host records directly.
	want := []string{
		`page.write host="browser"`,
		`  chord.call host="browser"`,
		`    chord.relay host="server"`,
		`      chord.relay host="project|p1"`,
		`        chord.method host="project|p1"`,
		`          chord.method host="project|p1"`,
	}
	if !reflect.DeepEqual(tree, want) {
		t.Fatalf("the click's trace =\n%s\nwant\n%s", strings.Join(tree, "\n"), strings.Join(want, "\n"))
	}
	click := spans[0]
	if !clickSpanID.MatchString(click.ID) {
		t.Fatalf("the click's span id = %q, want browser:<uuid>:<n>", click.ID)
	}
	if principal, _ := click.Attributes.Get("chord.principal"); principal != `{"kind":"owner"}` {
		t.Fatalf("the click's chord.principal = %q, want {\"kind\":\"owner\"}", principal)
	}
	for _, span := range spans {
		if span.TraceID != click.ID {
			t.Fatalf("span %s %q is in trace %q, want the click's %q", span.ID, span.Name, span.TraceID, click.ID)
		}
	}
}
