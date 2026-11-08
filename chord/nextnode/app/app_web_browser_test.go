package app

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/nextnode/internal/browsertest"
)

// Ports packages/chord/test/node/react/hooks.test.ts, web/language.test.ts and the first two cases of web/page.test.ts (Pi main
// c23b9ec1) over a real Chromium (the DevTools driver, chord/nextnode/internal/browsertest; skipped without one: `CHROMIUM_BIN`).
// The page's third case (a click as the root of one trace) is in app_web_trace_test.go and web/styles.test.ts in
// app_web_styles_test.go.
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

func TestAPageReloadsWhenTheServerComesBackWithAnotherBaseBundle(t *testing.T) {
	browser := browsertest.Start(t)
	port := freePort(t)
	entry := versionedEntry(t, "1", "")
	first := webBrowserApp(t, entry, port, map[string]map[string]string{"notes": panelPlugin("Notes", "#123456")}, nil)
	signIn(browser, first.app.OwnerLink())
	browser.Wait(`globalThis.version === "1" && globalThis.connection === "connected"`)
	firstBase := first.app.Web.Base
	if err := first.app.Close(background); err != nil {
		t.Fatal(err)
	}
	browser.Wait(`globalThis.connection === "waiting" || globalThis.connection === "connecting"`)
	versionedEntry(t, "2", filepath.Dir(entry))
	second := webBrowserApp(t, entry, port, nil, func(o *AppOptions) { o.DataDir, o.Root = first.dataDir, first.root })
	if second.app.Web.Base == firstBase {
		t.Fatal("another entry gave the same base hash")
	}
	browser.Wait(`globalThis.version === "2" && globalThis.connection === "connected"`)
	// The panel follows the connection by a frame or two (Opus L6).
	browser.Wait(`document.querySelector(".panel")?.textContent === "Notes"`)
}
