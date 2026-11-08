package app

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins"
)

// Ports packages/chord/test/node/reload/rollout.test.ts (Pi main c23b9ec1), "a rollout across hosts". The row "brings a page whose
// link was down during the rollout onto the new scripts and the new code" (the page's script drops its link and holds the
// reconnect, a web plugin updates its script, the kept input survives) and the rollout's `web` step need the web surface and a
// browser: the browser rows. Here the rollout is the server step and the project workers' step.

// rolloutApp is the app with the counter on the workers and `alpha` showing p1's, signed in, its counter at 2.
func rolloutApp(t *testing.T, native bool) (started, *reloadPage) {
	t.Helper()
	s := startReloadApp(t, map[string]map[string]string{
		"latch":   latchPlugin(t),
		"counter": counterPlugin(t, counterCode{Code: "v1", Placement: "worker", Native: native}),
	})
	page := reloadSignIn(t, s)
	page.hold("alpha", counterContract(t, "worker", "v1"), "p1", 0, "label")
	page.waitState("alpha", "live")
	expectEqual(t, "add 2", page.result(page.call("alpha", "add", 2.0)), okResult("v1:2"))
	page.waitLabel("alpha", "v1:2")
	return s, page
}

func TestARolloutAcrossHostsReloadsTheServerThenTheRunningWorkersThenTheClientsAWorkerNotRunningStartsOnTheNewCode(t *testing.T) {
	s, page := rolloutApp(t, false)
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
	// Pi's rollout ends with the clients' `web` step; the Go app has no web surface in this branch (the browser rows).
	expectEqual(t, "steps", got, []step{{"server", []string{"server"}}, {"project", []string{"project|p1"}}})
	expectEqual(t, "fates", rolloutFates(result), []string{"project/project|p1: reload.store restarted", "project/project|p1: reload.counter(p1) restarted"})
	// Project p2 had no worker during the rollout: its first hold starts one on the current set.
	page.hold("p2", counterContract(t, "worker", "v1"), "p2", 0, "label")
	page.waitState("p2", "live")
	expectEqual(t, "p2 code", page.result(page.call("p2", "code", nil)), okResult("v2"))
	expectEqual(t, "p2 add", page.result(page.call("p2", "add", 1.0)), okResult("v2:1"))
	expectEqual(t, "alpha code", page.result(page.call("alpha", "code", nil)), okResult("v2"))
}

func TestARolloutAcrossHostsBindsAPageThatConnectsWhileARestartDrainsToTheNewCodeOnceItIsLive(t *testing.T) {
	s, page := rolloutApp(t, true)
	hand := latchesOf(t, s, "worker")
	running := page.call("alpha", "slowAdd", "running")
	hand.entered("running")
	s.editAndApply(t, "counter", counterPlugin(t, counterCode{Code: "v2", Placement: "worker", Native: true}))
	// A second page signs in while the old code drains.
	late := reloadSignIn(t, s)
	late.hold("alpha", counterContract(t, "worker", "v1"), "p1", 0, "label")
	call := late.call("alpha", "code", nil)
	hand.open("running")
	expectEqual(t, "the running call", page.result(running), okResult("v2:12"))
	expectEqual(t, "the late page's call", late.result(call), okResult("v2"))
	late.waitLabel("alpha", "v2:12")
	late.waitState("alpha", "live")
	page.waitLabel("alpha", "v2:12")
}

func TestARolloutAcrossHostsEndsTwoRolloutsBackToBackOnTheSecondsCodeThePageFollowingWithoutFailing(t *testing.T) {
	s, page := rolloutApp(t, false)
	settled := len(page.states("alpha"))
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
	page.waitLabel("alpha", "v3:2")
	expectEqual(t, "add 1", page.result(page.call("alpha", "add", 1.0)), okResult("v3:3"))
	// Each restart shows `loading` once its old attempt ended, never a failure.
	states := page.states("alpha")[settled:]
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

// A case the table leaves out (the audit's "a contract change between hosts, consumer first"): the counter's contract moves to v2
// without `accepts`, and the server's summary, which holds it, changes with it (it copies the contract file). The server step runs
// first: the summary's new request meets the old worker serving v1. The documented gap (decision "The rollout order stays; no
// `loading` grace", design §21 "Rollout across hosts"): a consumer on an earlier step waits for its provider's later step; the recipe
// is two applies, the provider first. Pi marks it `it.fails`.
func TestARolloutAcrossHostsKeepsTheViewOfAServerConsumerBoundWhileItsWorkerProviderMovesToANewVersionWithIt(t *testing.T) {
	expectFailure(t, func(t testing.TB) {
		s := startReloadApp(t, map[string]map[string]string{
			"latch":   latchPlugin(t),
			"counter": counterPlugin(t, counterCode{Code: "v1", Placement: "worker"}),
			"summary": summaryPlugin(t, "s1"),
		})
		page := reloadSignIn(t, s)
		page.hold("alpha", summaryContract, "p1", 0, "label")
		page.waitLabel("alpha", "s1(0)")
		page.waitState("alpha", "live")
		settled := len(page.states("alpha"))
		// The summary imports the counter's contract file: the edit marks it too, and the rollout must find it dirty (it applies what the
		// watcher had marked when it runs; one applied before the summary's mark restarts nothing on the server and the row's gap does not show).
		result := s.editAndApply(t, "counter", counterPlugin(t, counterCode{Code: "v2", Placement: "worker", Contract: "v2"}), "@test/summary")
		expectEqual(t, "failures", rolloutFailures(result), []string{})
		page.waitState("alpha", "live")
		expectEqual(t, "code", page.result(page.call("alpha", "code", nil)), okResult("s1"))
		expectEqual(t, "states", page.states("alpha")[settled:], []string{})
	})
}
