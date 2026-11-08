package app

import (
	"testing"
	"time"
)

// Ports packages/chord/test/node/reload/contracts.test.ts (Pi main c23b9ec1): "a contract change the new code still `accepts`" and
// "a contract change not accepted, or a type removed" (design §21 "What a user sees during a reload"). Accepted: the old page keeps
// working through the mapping, its calls answered through it. Not accepted or removed: the reference waits with its last values
// (`failed { waiting }`: `version`, `not_served`) until the page's code updates or the type returns; a call on it fails at once
// with the cause (`contract_mismatch`, `unavailable` not served); state unchanged. The page's web plugin keeps its own copy of the
// contract (an old page) while the counter on the worker moves. Through a real page in Chromium (reload_web_support_test.go), as Pi's.

// parkFor is the page's short reference's `readyTimeout`: a call failing at once answers well before it.
const parkFor = 1500 * time.Millisecond

// oldPage is the app with the counter at v1 on worker `p1`, the page showing it (`alpha`) with a count of 2.
func oldPage(t *testing.T) (started, *reloadTab) {
	t.Helper()
	tab := startReloadTab(t)
	s := startReload(t, map[string]map[string]string{
		"latch":   latchPlugin(t),
		"counter": counterPlugin(t, counterCode{Code: "v1", Placement: "worker"}),
		"alpha":   reloadWebPlugin(t, webCode{Name: "alpha", Code: "1", Counter: reloadWebCounterV1}),
	})
	tab.signIn(s, "alpha")
	expectEqual(t, "add 2", tab.result(tab.call("alpha", "add", 2)), okResult("v1:2"))
	tab.waitText("alpha", ".label", "v1:2")
	tab.typeInto("alpha", ".draft", "half-typed")
	return s, tab
}

func TestAReloadChangingAContractKeepsAnOldPageWorkingThroughTheMappingOfAContractTheNewCodeAccepts(t *testing.T) {
	s, tab := oldPage(t)
	settled := len(tab.states("alpha"))
	result := s.editAndApply(t, "counter", counterPlugin(t, counterCode{Code: "v2", Placement: "worker", Contract: "v2", Accepts: true}))
	expectEqual(t, "failures", rolloutFailures(result), []string{})
	// The v1 page reads v2's label mapped back, and its v1 call is mapped forwards and its result back.
	tab.waitText("alpha", ".label", "v2:2")
	expectEqual(t, "add 1", tab.result(tab.call("alpha", "add", 1)), okResult("v2:3"))
	tab.waitText("alpha", ".label", "v2:3")
	expectEqual(t, "code", tab.result(tab.call("alpha", "code")), okResult("v2"))
	// The counter restarted: `loading` once its old attempt ended, then the new code's fresh snapshot.
	expectEqual(t, "states", tab.states("alpha")[settled:], []string{"loading", "live"})
	expectEqual(t, "count", tab.shows("alpha", ".count"), "3")
	expectEqual(t, "visits", tab.result(tab.call("alpha", "visits")), okResult(2))
	expectEqual(t, "draft", tab.value("alpha", ".draft"), "half-typed")
}

func TestAReloadChangingAContractKeepsTheLastValuesWhileTheVersionIsRefusedAndBindsOnceThePagesCodeUpdates(t *testing.T) {
	s, tab := oldPage(t)
	result := s.editAndApply(t, "counter", counterPlugin(t, counterCode{Code: "v2", Placement: "worker", Contract: "v2"}))
	expectEqual(t, "failures", rolloutFailures(result), []string{})

	// Unbound `version` (§3): `failed { waiting }` with `contract_mismatch`.
	tab.waitStateWithin("alpha", "failed:waiting:contract_mismatch", 5*time.Second)
	// The last values stay on screen.
	expectEqual(t, "label", tab.shows("alpha", ".label"), "v1:2")
	expectEqual(t, "count", tab.shows("alpha", ".count"), "2")
	expectEqual(t, "state", tab.shows("alpha", ".state"), "failed")

	// The page's code updates (its contract copy at v2): the remounted view binds the new version.
	s.editAndApply(t, "alpha", reloadWebPlugin(t, webCode{Name: "alpha", Code: "2", Counter: &reloadWebCounter{Placement: "worker", Contract: "v2"}}))
	tab.waitText("alpha", ".code", "alpha:2")
	tab.waitText("alpha", ".label", `{"text":"v2:2","code":"v2"}`)
	tab.waitText("alpha", ".state", "live")
	expectEqual(t, "add {n:1}", tab.result(tab.call("alpha", "add", map[string]any{"n": 1})), okResult(map[string]any{"text": "v2:3"}))
	expectEqual(t, "visits", tab.result(tab.call("alpha", "visits")), okResult(2))
}

func TestAReloadChangingAContractKeepsTheLastValuesWhileTheTypeIsRemovedAndBindsAgainWhenTheTypeReturns(t *testing.T) {
	s, tab := oldPage(t)
	result := s.removeAndApply(t, "counter")
	expectEqual(t, "failures", rolloutFailures(result), []string{})

	// Unbound `not_served` (§3): `failed { waiting }` with `unavailable` ("X is not served here").
	tab.waitStateWithin("alpha", "failed:waiting:unavailable", 5*time.Second)
	expectEqual(t, "label", tab.shows("alpha", ".label"), "v1:2")
	expectEqual(t, "count", tab.shows("alpha", ".count"), "2")

	// The type returns: the same reference binds again, the persisted count as it was.
	s.editAndApply(t, "counter", counterPlugin(t, counterCode{Code: "v1", Placement: "worker"}))
	tab.waitState("alpha", "live")
	tab.waitText("alpha", ".state", "live")
	expectEqual(t, "label", tab.shows("alpha", ".label"), "v1:2")
	expectEqual(t, "add 1", tab.result(tab.call("alpha", "add", 1)), okResult("v1:3"))
	expectEqual(t, "visits", tab.result(tab.call("alpha", "visits")), okResult(2))
	expectEqual(t, "draft", tab.value("alpha", ".draft"), "half-typed")
}

func TestAReloadChangingAContractFailsACallAtOnceWithTheCauseWhile(t *testing.T) {
	for _, change := range []struct{ name, waiting, code string }{
		{"the version is refused", "failed:waiting:contract_mismatch", "contract_mismatch"},
		{"the type is removed", "failed:waiting:unavailable", "unavailable"},
	} {
		t.Run(change.name, func(t *testing.T) {
			s, tab := oldPage(t)
			tab.hold("short", "alpha", parkFor, "p1", "label")
			tab.waitState("short", "live")
			if change.name == "the type is removed" {
				s.removeAndApply(t, "counter")
			} else {
				s.editAndApply(t, "counter", counterPlugin(t, counterCode{Code: "v2", Placement: "worker", Contract: "v2"}))
			}
			tab.waitStateWithin("short", change.waiting, 5*time.Second)
			started := time.Now()
			failed := tab.result(tab.call("short", "add", 1))
			if failed.Status != "error" || failed.Code != change.code {
				t.Fatalf("the call = %+v, want an error %s", failed, change.code)
			}
			// Not parked: well before the reference's `readyTimeout`.
			if elapsed := time.Since(started); elapsed >= parkFor {
				t.Fatalf("the call failed after %v, want before %v", elapsed, parkFor)
			}
		})
	}
}
