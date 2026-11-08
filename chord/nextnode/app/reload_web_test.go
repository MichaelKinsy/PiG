package app

import (
	"maps"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// Ports packages/chord/test/node/reload/{base,web}.test.ts and the two web-plugin rows of failed.test.ts (Pi main c23b9ec1) over a real
// Chromium (reload_web_support_test.go: the suite's base bundle, `ReloadPage` as `reloadTab`). Pi's inputs and expectations as they
// are; the two rows Pi marks as open design points are asserted as they are today.

func TestAChangeOfBaseCodeReloadsThePageEndsCallsInFlightWithTheLinkLosesPageStateAndKeepsServerState(t *testing.T) {
	tab := startReloadTab(t)
	port := freePort(t)
	entry := reloadEntry(t, "1", "")
	first := startReloadWebApp(t, entry, port, map[string]map[string]string{
		"latch":   latchPlugin(t),
		"counter": counterPlugin(t, counterCode{Code: "v1", Placement: "worker"}),
		"alpha":   reloadWebPlugin(t, webCode{Name: "alpha", Code: "1", Counter: reloadWebCounterV1}),
	}, nil)
	tab.signIn(first, "alpha")
	tab.waitText("alpha", ".state", "live")
	expectEqual(t, "add 2", tab.result(tab.call("alpha", "add", 2)), okResult("v1:2"))
	tab.waitText("alpha", ".label", "v1:2")
	tab.typeInto("alpha", ".kept", "kept")
	tab.browser.Run(`globalThis.marked = true`)
	hand := latchesOf(t, first, "worker")
	running := tab.call("alpha", "slowAdd", "running")
	hand.entered("running")

	// Base code changes; the app restarts its hosts.
	reloadEntry(t, "2", filepath.Dir(entry))
	if err := first.app.Close(background); err != nil {
		t.Fatal(err)
	}
	// The call in flight ends with the link.
	if got := tab.result(running); got.Status != "error" {
		t.Fatalf("the call in flight = %+v, want an error", got)
	}
	second := startReloadWebApp(t, entry, port, nil, func(o *AppOptions) { o.DataDir, o.Root = first.dataDir, first.root })
	if second.app.Web.Base() == first.app.Web.Base() {
		t.Fatal("another entry gave the same base hash")
	}

	// The page reloads for the new base bundle: what was on screen and in page state is gone.
	tab.browser.Wait(`globalThis.version === "2" && globalThis.page?.connection === "connected"`, 60*time.Second)
	if evalAs[bool](tab, `globalThis.marked ?? false`) {
		t.Fatal("a mark on the page survived the base reload")
	}
	tab.waitText("alpha", ".state", "live")
	expectEqual(t, "kept", tab.value("alpha", ".kept"), "")
	// Server state unchanged: the count as committed before (the cut call never committed).
	expectEqual(t, "label", tab.shows("alpha", ".label"), "v1:2")
	expectEqual(t, "visits", tab.result(tab.call("alpha", "visits")), okResult(1))
}

func TestAChangeOfBaseCodeFlagsAChangeOfTheWebEntryAsBaseCodePluginsStateBase(t *testing.T) {
	entry := reloadEntry(t, "1", "")
	s := startReloadWebApp(t, entry, freePort(t), map[string]map[string]string{"alpha": reloadWebPlugin(t, webCode{Name: "alpha", Code: "1"})}, nil)
	reloadEntry(t, "2", filepath.Dir(entry))
	untilBase(t, s.app)
}

// webCounterApp is the app with the counter on the workers and the given web plugins.
func webCounterApp(t *testing.T, web map[string]map[string]string) started {
	t.Helper()
	files := map[string]map[string]string{
		"latch":   latchPlugin(t),
		"counter": counterPlugin(t, counterCode{Code: "v1", Placement: "worker"}),
	}
	maps.Copy(files, web)
	return startReload(t, files)
}

func TestAReloadOfAWebPluginRemountsOnlyTheReloadedPluginsComponentsKeepingPageStateTheOtherPluginsAndTheScroll(t *testing.T) {
	tab := startReloadTab(t)
	s := webCounterApp(t, map[string]map[string]string{
		"alpha": reloadWebPlugin(t, webCode{Name: "alpha", Code: "1", Counter: reloadWebCounterV1}),
		"beta":  reloadWebPlugin(t, webCode{Name: "beta", Code: "1"}),
	})
	tab.signIn(s, "alpha", "beta")
	tab.waitText("alpha", ".state", "live")
	expectEqual(t, "add 2", tab.result(tab.call("alpha", "add", 2)), okResult("v1:2"))
	tab.waitText("alpha", ".label", "v1:2")
	tab.typeInto("alpha", ".draft", "alpha draft")
	tab.typeInto("alpha", ".kept", "alpha kept")
	tab.typeInto("beta", ".draft", "beta draft")
	tab.typeInto("beta", ".kept", "beta kept")
	tab.browser.Run(`document.getElementById("base-draft").focus()`)
	tab.browser.Type("base draft")
	// Marks on the DOM nodes that must survive, and a scroll position.
	tab.browser.Run(`document.querySelector("[data-view=beta] .draft").marked = true; document.querySelector("[data-view=alpha] .draft").marked = true; window.scrollTo(0, 1000)`)
	expectEqual(t, "scrollY", evalAs[float64](tab, `window.scrollY`), 1000.0)

	result := s.editAndApply(t, "alpha", reloadWebPlugin(t, webCode{Name: "alpha", Code: "2", Counter: reloadWebCounterV1}))
	expectEqual(t, "failures", rolloutFailures(result), []string{})
	names := []string{}
	for _, step := range result.Steps {
		names = append(names, step.Name)
	}
	expectEqual(t, "steps", names, []string{"server", "project", "web"})

	tab.waitText("alpha", ".code", "alpha:2")
	// The reloaded plugin: a new component (its state starts over), its page state kept, its counter shown again.
	expectEqual(t, "alpha draft", tab.value("alpha", ".draft"), "")
	expectEqual(t, "alpha draft node", evalAs[bool](tab, `document.querySelector("[data-view=alpha] .draft").marked ?? false`), false)
	expectEqual(t, "alpha kept", tab.value("alpha", ".kept"), "alpha kept")
	tab.waitText("alpha", ".label", "v1:2")
	tab.waitText("alpha", ".state", "live")
	expectEqual(t, "add 1", tab.result(tab.call("alpha", "add", 1)), okResult("v1:3"))
	// Everything else untouched: the other plugin's component (the same DOM node), the base's input, the scroll.
	expectEqual(t, "beta code", tab.shows("beta", ".code"), "beta:1")
	expectEqual(t, "beta draft", tab.value("beta", ".draft"), "beta draft")
	expectEqual(t, "beta draft node", evalAs[bool](tab, `document.querySelector("[data-view=beta] .draft").marked ?? false`), true)
	expectEqual(t, "beta kept", tab.value("beta", ".kept"), "beta kept")
	expectEqual(t, "base draft", evalAs[string](tab, `document.getElementById("base-draft").value`), "base draft")
	expectEqual(t, "scrollY", evalAs[float64](tab, `window.scrollY`), 1000.0)
	expectEqual(t, "loads", tab.loads(), 1)
}

// Open design point (2026-11-04, with the owner): `useHold` releases the old component's reference when a plugin reload remounts it,
// and the release aborts its calls in flight on the worker; yet "a component unmounted by a reload has not left" (§21). Asserted as
// it is today.
func TestAReloadOfAWebPluginEndsACallTheReloadedPluginsOldComponentMadeReleasedNothingCommittedOpenUseHoldsReleaseOnAReloadsUnmountAbortsIt(t *testing.T) {
	tab := startReloadTab(t)
	s := webCounterApp(t, map[string]map[string]string{"alpha": reloadWebPlugin(t, webCode{Name: "alpha", Code: "1", Counter: reloadWebCounterV1})})
	hand := latchesOf(t, s, "worker")
	tab.signIn(s, "alpha")
	tab.waitText("alpha", ".state", "live")
	running := tab.call("alpha", "slowAdd", "running")
	hand.entered("running")
	s.editAndApply(t, "alpha", reloadWebPlugin(t, webCode{Name: "alpha", Code: "2", Counter: reloadWebCounterV1}))
	tab.waitText("alpha", ".code", "alpha:2")
	hand.open("running")
	got := tab.result(running)
	if got.Status != "error" || got.Code != "released" {
		t.Fatalf("the call in flight = %+v, want an error with code released", got)
	}
	// Aborted on the worker: nothing committed.
	tab.waitText("alpha", ".state", "live")
	expectEqual(t, "label", tab.shows("alpha", ".label"), "v1:0")
	expectEqual(t, "visits", tab.result(tab.call("alpha", "visits")), okResult(0))
}

// A case the table leaves out (the audit's "a plugin whose contributions change shape"): a web plugin's new code contributes no
// view, then contributes it again; the other plugin's component stays mounted throughout.
func TestAReloadOfAWebPluginRemovesAndRestoresAViewWhosePluginsContributionsChangeTheOtherPluginsUntouched(t *testing.T) {
	tab := startReloadTab(t)
	s := startReload(t, map[string]map[string]string{
		"alpha": reloadWebPlugin(t, webCode{Name: "alpha", Code: "1"}),
		"beta":  reloadWebPlugin(t, webCode{Name: "beta", Code: "1"}),
	})
	tab.signIn(s, "alpha", "beta")
	tab.typeInto("beta", ".draft", "beta draft")
	tab.typeInto("alpha", ".kept", "alpha kept")
	s.editAndApply(t, "alpha", reloadWebPlugin(t, webCode{Name: "alpha", Code: "2", Hidden: true}))
	tab.browser.Wait(`document.querySelector("[data-view=alpha]") === null`)
	expectEqual(t, "beta draft", tab.value("beta", ".draft"), "beta draft")
	s.editAndApply(t, "alpha", reloadWebPlugin(t, webCode{Name: "alpha", Code: "3"}))
	tab.waitText("alpha", ".code", "alpha:3")
	expectEqual(t, "alpha kept", tab.value("alpha", ".kept"), "alpha kept")
	expectEqual(t, "beta draft", tab.value("beta", ".draft"), "beta draft")
	expectEqual(t, "loads", tab.loads(), 1)
}

// webFailedApp is the app with the counter, a web plugin `alpha` showing it and a web plugin `beta`, alpha's inputs typed in.
func webFailedApp(t *testing.T, tab *reloadTab) started {
	t.Helper()
	s := webCounterApp(t, map[string]map[string]string{
		"alpha": reloadWebPlugin(t, webCode{Name: "alpha", Code: "1", Counter: reloadWebCounterV1}),
		"beta":  reloadWebPlugin(t, webCode{Name: "beta", Code: "1"}),
	})
	tab.signIn(s, "alpha", "beta")
	tab.waitText("alpha", ".label", "v1:0")
	tab.typeInto("alpha", ".draft", "half-typed")
	tab.typeInto("alpha", ".kept", "kept")
	return s
}

// oldComponentKept checks the old component still mounted with its state, its reference untouched since settled states, calls answered.
func oldComponentKept(t *testing.T, tab *reloadTab, settled int) {
	t.Helper()
	expectEqual(t, "code", tab.shows("alpha", ".code"), "alpha:1")
	expectEqual(t, "draft", tab.value("alpha", ".draft"), "half-typed")
	expectEqual(t, "kept", tab.value("alpha", ".kept"), "kept")
	expectEqual(t, "states", tab.states("alpha")[settled:], []string{})
	expectEqual(t, "add 1", tab.result(tab.call("alpha", "add", 1)), okResult("v1:1"))
	expectEqual(t, "loads", tab.loads(), 1)
}

func TestAReloadWhoseNewCodeFailsKeepsAWebPluginWhoseNewBuildFailsOnItsOldCodeItsComponentAndStateOnScreen(t *testing.T) {
	tab := startReloadTab(t)
	s := webFailedApp(t, tab)
	settled := len(tab.states("alpha"))
	result := s.editAndApply(t, "alpha", reloadWebPlugin(t, webCode{Name: "alpha", Code: "2", Counter: reloadWebCounterV1, Broken: "build"}))
	// Reported to the app: the web step kept the plugin's old script, and it stays dirty for the next apply.
	expectEqual(t, "failures", rolloutFailures(result), []string{"web/web: @test/alpha"})
	if !slices.Contains(s.dirty(), "@test/alpha") {
		t.Fatalf("dirty = %v, want @test/alpha in it", s.dirty())
	}
	sleepFor(500 * time.Millisecond)
	oldComponentKept(t, tab, settled)
	expectEqual(t, "page errors", tab.errors(), []pageError{})
}

// Open design point (2026-11-04, with the owner): pages don't report kept-old plugins back. The server's web step publishes the set
// without waiting for clients, so it marks the plugin applied (no longer dirty) although the page kept its old code; decision "Per
// plugin at startup and reload" (5) says a plugin kept old on any host stays dirty.
func TestAReloadWhoseNewCodeFailsKeepsAWebPluginWhoseNewScriptThrowsInThePageOnItsOldCodeReportedInThePageTheServerMarksItAppliedOpenPagesDontReportKeptOldPluginsBack(t *testing.T) {
	tab := startReloadTab(t)
	s := webFailedApp(t, tab)
	settled := len(tab.states("alpha"))
	s.editAndApply(t, "alpha", reloadWebPlugin(t, webCode{Name: "alpha", Code: "2", Counter: reloadWebCounterV1, Broken: "evaluation"}))
	// The page evaluates the script: it keeps the old one and reports it (`startPage`'s `onError`).
	tab.browser.Wait(`globalThis.page.errors.length > 0`)
	expectEqual(t, "page errors", tab.errors(), []pageError{{Kind: "plugins", Plugins: []string{"@test/alpha"}}})
	oldComponentKept(t, tab, settled)
	// Today's behaviour, the open point above: the server considers it applied.
	if dirty := s.dirty(); len(dirty) != 0 {
		t.Fatalf("dirty = %v, want none", dirty)
	}
}
