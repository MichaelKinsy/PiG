package app

import (
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord/delta"
)

// Ports packages/chord/test/node/reload/contracts.test.ts (Pi main c23b9ec1): "a contract change the new code still `accepts`" and
// "a contract change not accepted, or a type removed" (design §21 "What a user sees during a reload"). Accepted: the old page keeps
// working through the mapping, its calls answered through it. Not accepted or removed: the reference waits with its last values
// (`failed { waiting }`: `version`, `not_served`) until the page's code updates or the type returns; a call on it fails at once
// with the cause (`contract_mismatch`, `unavailable` not served); state unchanged. The page keeps its own copy of the contract (an
// old page) while the counter on the worker moves; "the page's code updates" is the page holding the contract at v2 (the web
// plugin's remount is the browser row).

// parkMs is the page's short reference's `readyTimeout`: a call failing at once answers well before it.
const parkMs = 1500 * time.Millisecond

// oldPage is the app with the counter at v1 on worker `p1`, the page showing it (`alpha`) with a count of 2.
func oldPage(t *testing.T) (started, *reloadPage) {
	t.Helper()
	s := startReloadApp(t, map[string]map[string]string{
		"latch":   latchPlugin(t),
		"counter": counterPlugin(t, counterCode{Code: "v1", Placement: "worker"}),
	})
	page := reloadSignIn(t, s)
	page.hold("alpha", counterContract(t, "worker", "v1"), "p1", 0, "label")
	expectEqual(t, "add 2", page.result(page.call("alpha", "add", 2.0)), okResult("v1:2"))
	page.waitLabel("alpha", "v1:2")
	page.waitState("alpha", "live")
	return s, page
}

func TestAReloadChangingAContractKeepsAnOldPageWorkingThroughTheMappingOfAContractTheNewCodeAccepts(t *testing.T) {
	s, page := oldPage(t)
	settled := len(page.states("alpha"))
	result := s.editAndApply(t, "counter", counterPlugin(t, counterCode{Code: "v2", Placement: "worker", Contract: "v2", Accepts: true}))
	expectEqual(t, "failures", rolloutFailures(result), []string{})
	// The v1 page reads v2's label mapped back, and its v1 call is mapped forwards and its result back.
	page.waitLabel("alpha", "v2:2")
	expectEqual(t, "add 1", page.result(page.call("alpha", "add", 1.0)), okResult("v2:3"))
	page.waitLabel("alpha", "v2:3")
	expectEqual(t, "code", page.result(page.call("alpha", "code", nil)), okResult("v2"))
	// The counter restarted: `loading` once its old attempt ended, then the new code's fresh snapshot.
	expectEqual(t, "states", page.states("alpha")[settled:], []string{"loading", "live"})
	expectEqual(t, "count", page.countText("alpha"), "3")
	expectEqual(t, "visits", page.result(page.call("alpha", "visits", nil)), okResult(2))
}

func TestAReloadChangingAContractKeepsTheLastValuesWhileTheVersionIsRefusedAndBindsOnceThePagesCodeUpdates(t *testing.T) {
	s, page := oldPage(t)
	result := s.editAndApply(t, "counter", counterPlugin(t, counterCode{Code: "v2", Placement: "worker", Contract: "v2"}))
	expectEqual(t, "failures", rolloutFailures(result), []string{})

	// Unbound `version` (§3): `failed { waiting }` with `contract_mismatch`.
	page.waitStateText("alpha", "failed:waiting:contract_mismatch", 5*time.Second)
	// The last values stay on screen.
	expectEqual(t, "label", page.labelText("alpha"), "v1:2")
	expectEqual(t, "count", page.countText("alpha"), "2")
	expectEqual(t, "state", page.stateKind("alpha"), "failed")

	// The page's code updates (its contract copy at v2): the remounted view binds the new version.
	page.hold("alpha", counterContract(t, "worker", "v2"), "p1", 0, "label")
	page.waitLabel("alpha", `{"text":"v2:2","code":"v2"}`)
	page.waitState("alpha", "live")
	expectEqual(t, "add {n:1}", page.result(page.call("alpha", "add", delta.JsonObjectOf("n", 1.0))), okResult(map[string]any{"text": "v2:3"}))
	expectEqual(t, "visits", page.result(page.call("alpha", "visits", nil)), okResult(2))
}

func TestAReloadChangingAContractKeepsTheLastValuesWhileTheTypeIsRemovedAndBindsAgainWhenTheTypeReturns(t *testing.T) {
	s, page := oldPage(t)
	result := s.removeAndApply(t, "counter")
	expectEqual(t, "failures", rolloutFailures(result), []string{})

	// Unbound `not_served` (§3): `failed { waiting }` with `unavailable` ("X is not served here").
	page.waitStateText("alpha", "failed:waiting:unavailable", 5*time.Second)
	expectEqual(t, "label", page.labelText("alpha"), "v1:2")
	expectEqual(t, "count", page.countText("alpha"), "2")

	// The type returns: the same reference binds again, the persisted count as it was.
	s.editAndApply(t, "counter", counterPlugin(t, counterCode{Code: "v1", Placement: "worker"}))
	page.waitStateText("alpha", "live", reloadWait)
	expectEqual(t, "state", page.stateKind("alpha"), "live")
	expectEqual(t, "label", page.labelText("alpha"), "v1:2")
	expectEqual(t, "add 1", page.result(page.call("alpha", "add", 1.0)), okResult("v1:3"))
	expectEqual(t, "visits", page.result(page.call("alpha", "visits", nil)), okResult(2))
}

func TestAReloadChangingAContractFailsACallAtOnceWithTheCauseWhile(t *testing.T) {
	for _, change := range []struct{ name, waiting, code string }{
		{"the version is refused", "failed:waiting:contract_mismatch", "contract_mismatch"},
		{"the type is removed", "failed:waiting:unavailable", "unavailable"},
	} {
		t.Run(change.name, func(t *testing.T) {
			s, page := oldPage(t)
			page.hold("short", counterContract(t, "worker", "v1"), "p1", parkMs, "label")
			page.waitState("short", "live")
			if change.name == "the type is removed" {
				s.removeAndApply(t, "counter")
			} else {
				s.editAndApply(t, "counter", counterPlugin(t, counterCode{Code: "v2", Placement: "worker", Contract: "v2"}))
			}
			page.waitStateText("short", change.waiting, 5*time.Second)
			started := time.Now()
			failed := page.result(page.call("short", "add", 1.0))
			if failed.Status != "error" || failed.Code != change.code {
				t.Fatalf("the call = %+v, want an error %s", failed, change.code)
			}
			// Not parked: well before the reference's `readyTimeout`.
			if elapsed := time.Since(started); elapsed >= parkMs {
				t.Fatalf("the call failed after %v, want before %v", elapsed, parkMs)
			}
		})
	}
}
