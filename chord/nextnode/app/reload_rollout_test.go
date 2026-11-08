package app

import (
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins"
)

// Ports packages/chord/test/node/reload/rollout.test.ts (Pi main c23b9ec1), "a rollout across hosts" (design §21 "What a user sees
// during a reload": "the server, then every running worker by the same rules, then the clients' scripts") and the cases the table
// leaves out: a worker not running during the rollout, a page connecting while a restart drains, two rollouts back to back, a rollout
// while the page's link is down. Through real pages in Chromium (reload_web_support_test.go), as Pi's.

// rolloutApp is the app with the counter on the workers and `alpha` showing p1's, signed in, its counter at 2.
func rolloutApp(t *testing.T, native bool) (started, *reloadTab) {
	t.Helper()
	tab := startReloadTab(t)
	s := startReload(t, map[string]map[string]string{
		"latch":   latchPlugin(t),
		"counter": counterPlugin(t, counterCode{Code: "v1", Placement: "worker", Native: native}),
		"alpha":   reloadWebPlugin(t, webCode{Name: "alpha", Code: "1", Counter: reloadWebCounterV1}),
	})
	tab.signIn(s, "alpha")
	tab.waitText("alpha", ".state", "live")
	expectEqual(t, "add 2", tab.result(tab.call("alpha", "add", 2)), okResult("v1:2"))
	tab.waitText("alpha", ".label", "v1:2")
	return s, tab
}

func TestARolloutAcrossHostsReloadsTheServerThenTheRunningWorkersThenTheClientsAWorkerNotRunningStartsOnTheNewCode(t *testing.T) {
	s, tab := rolloutApp(t, false)
	result := s.editAndApply(t, "counter", counterPlugin(t, counterCode{Code: "v2", Placement: "worker"}))
	expectEqual(t, "failures", rolloutFailures(result), []string{})
	type step struct {
		Name  string
		Hosts []string
	}
	got := []step{}
	for _, each := range result.Steps {
		hosts := []string{}
		for _, entry := range each.Hosts {
			hosts = append(hosts, entry.Host)
		}
		got = append(got, step{each.Name, hosts})
	}
	expectEqual(t, "steps", got, []step{{"server", []string{"server"}}, {"project", []string{"project|p1"}}, {"web", []string{"web"}}})
	expectEqual(t, "fates", rolloutFates(result), []string{"project/project|p1: reload.store restarted", "project/project|p1: reload.counter(p1) restarted"})
	// Project p2 had no worker during the rollout: its first hold starts one on the current set.
	tab.hold("p2", "alpha", 10*time.Second, "p2", "label")
	tab.waitState("p2", "live")
	expectEqual(t, "p2 code", tab.result(tab.call("p2", "code")), okResult("v2"))
	expectEqual(t, "p2 add", tab.result(tab.call("p2", "add", 1)), okResult("v2:1"))
	expectEqual(t, "alpha code", tab.result(tab.call("alpha", "code")), okResult("v2"))
}

func TestARolloutAcrossHostsBindsAPageThatConnectsWhileARestartDrainsToTheNewCodeOnceItIsLive(t *testing.T) {
	s, tab := rolloutApp(t, true)
	hand := latchesOf(t, s, "worker")
	t.Cleanup(func() { hand.open("running") })
	late := startReloadTab(t)
	running := tab.call("alpha", "slowAdd", "running")
	hand.entered("running")
	s.editAndApply(t, "counter", counterPlugin(t, counterCode{Code: "v2", Placement: "worker", Native: true}))
	// A second page signs in while the old code drains.
	late.signInAgain(s, "alpha")
	call := late.call("alpha", "code")
	hand.open("running")
	expectEqual(t, "the running call", tab.result(running), okResult("v2:12"))
	expectEqual(t, "the late page's call", late.result(call), okResult("v2"))
	late.waitText("alpha", ".label", "v2:12")
	late.waitText("alpha", ".state", "live")
	tab.waitText("alpha", ".label", "v2:12")
}

func TestARolloutAcrossHostsEndsTwoRolloutsBackToBackOnTheSecondsCodeThePageFollowingWithoutFailing(t *testing.T) {
	s, tab := rolloutApp(t, false)
	settled := len(tab.states("alpha"))
	type applied struct {
		result plugins.RolloutResult
		err    error
	}
	apply := func() chan applied {
		out := make(chan applied, 1)
		go func() {
			result, err := s.app.ApplyChanges(background)
			out <- applied{result, err}
		}()
		return out
	}
	s.edit(t, "counter", counterPlugin(t, counterCode{Code: "v2", Placement: "worker"}))
	first := apply()
	s.edit(t, "counter", counterPlugin(t, counterCode{Code: "v3", Placement: "worker"}))
	second := apply()
	for _, rollout := range []chan applied{first, second} {
		done := <-rollout
		if done.err != nil {
			t.Fatal(done.err)
		}
		expectEqual(t, "failures", rolloutFailures(done.result), []string{})
	}
	tab.waitText("alpha", ".label", "v3:2")
	expectEqual(t, "add 1", tab.result(tab.call("alpha", "add", 1)), okResult("v3:3"))
	// Each restart shows `loading` once its old attempt ended, never a failure.
	states := tab.states("alpha")[settled:]
	for _, state := range states {
		if strings.HasPrefix(state, "failed") {
			t.Fatalf("states = %v, want no failure", states)
		}
	}
	if states[len(states)-1] != "live" {
		t.Fatalf("states = %v, want live last", states)
	}
	expectEqual(t, "dirty", s.dirty(), []string{})
}

func TestARolloutAcrossHostsBringsAPageWhoseLinkWasDownDuringTheRolloutOntoTheNewScriptsAndTheNewCode(t *testing.T) {
	s, tab := rolloutApp(t, false)
	tab.typeInto("alpha", ".kept", "kept")
	tab.browser.Run(`globalThis.page.holdReconnect(); globalThis.page.dropLink()`)
	tab.browser.Wait(`globalThis.page.connection !== "connected"`)
	tab.waitState("alpha", "loading")
	// The last values stay on screen while the link is down.
	expectEqual(t, "label", tab.shows("alpha", ".label"), "v1:2")

	s.edit(t, "counter", counterPlugin(t, counterCode{Code: "v2", Placement: "worker"}))
	s.edit(t, "alpha", reloadWebPlugin(t, webCode{Name: "alpha", Code: "2", Counter: reloadWebCounterV1}))
	result := s.apply(t)
	expectEqual(t, "failures", rolloutFailures(result), []string{})
	tab.browser.Run(`globalThis.page.release()`)

	tab.browser.Wait(`globalThis.page.connection === "connected"`)
	tab.waitText("alpha", ".code", "alpha:2")
	tab.waitText("alpha", ".label", "v2:2")
	tab.waitText("alpha", ".state", "live")
	expectEqual(t, "kept", tab.value("alpha", ".kept"), "kept")
	expectEqual(t, "add 1", tab.result(tab.call("alpha", "add", 1)), okResult("v2:3"))
	expectEqual(t, "loads", tab.loads(), 1)
}

// A case the table leaves out (the audit's "a contract change between hosts, consumer first"): the counter's contract moves to v2
// without `accepts`, and the server's summary, which holds it, changes with it (it copies the contract file). The server step runs
// first: the summary's new request meets the old worker serving v1. The documented gap (decision "The rollout order stays; no
// `loading` grace", design §21 "Rollout across hosts"): a consumer on an earlier step waits for its provider's later step; the recipe
// is two applies, the provider first. Pi marks it `it.fails`.
func TestARolloutAcrossHostsKeepsTheViewOfAServerConsumerBoundWhileItsWorkerProviderMovesToANewVersionWithIt(t *testing.T) {
	expectFailure(t, func(t testing.TB) {
		tab := startReloadTab(t)
		s := startReload(t, map[string]map[string]string{
			"latch":   latchPlugin(t),
			"counter": counterPlugin(t, counterCode{Code: "v1", Placement: "worker"}),
			"summary": summaryPlugin(t, "s1"),
			"alpha":   reloadWebPlugin(t, webCode{Name: "alpha", Code: "1", Counter: &reloadWebCounter{Placement: "server", Contract: "v1"}, Summary: true}),
		})
		tab.signIn(s, "alpha")
		tab.waitText("alpha", ".label", "s1(0)")
		settled := len(tab.states("alpha"))
		// The summary imports the counter's contract file: the edit marks it too, and the rollout must find it dirty (it applies what the
		// watcher had marked when it runs; one applied before the summary's mark restarts nothing on the server and the row's gap does not show).
		result := s.editAndApply(t, "counter", counterPlugin(t, counterCode{Code: "v2", Placement: "worker", Contract: "v2"}), "@test/summary")
		expectEqual(t, "failures", rolloutFailures(result), []string{})
		tab.waitText("alpha", ".state", "live")
		expectEqual(t, "code", tab.result(tab.call("alpha", "code")), okResult("s1"))
		expectEqual(t, "states", tab.states("alpha")[settled:], []string{})
	})
}
