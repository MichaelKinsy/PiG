package app

import (
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord/delta"
)

// Ports packages/chord/test/node/reload/restart.test.ts (Pi main c23b9ec1), "a reload restarting an object with natives" (design §21
// "What a user sees during a reload", as corrected 2026-11-04): while the old code drains its running calls (up to `closeTimeout`)
// the page stays `live` with its last values and whatever the draining old code commits; calls sent meanwhile wait at the gate;
// then `loading` briefly, then a fresh snapshot from the new code, which answers the waiting calls; documents and tables
// unchanged, natives reopened. For a counter holding a native on a project's worker and on the server.

func TestAReloadRestartingAnObjectWithNativesStaysLiveWhileTheOldCodeDrainsThenShowsLoadingAndAFreshSnapshotFromTheNewCode(t *testing.T) {
	for _, placement := range []string{"worker", "server"} {
		t.Run("on the "+placement, func(t *testing.T) {
			s := startReloadApp(t, map[string]map[string]string{
				"latch":   latchPlugin(t),
				"counter": counterPlugin(t, counterCode{Code: "v1", Placement: placement, Native: true}),
			})
			hand := latchesOf(t, s, placement)
			page := reloadSignIn(t, s)
			page.hold("alpha", counterContract(t, placement, "v1"), "p1", 0, "label")
			page.waitState("alpha", "live")
			expectEqual(t, "add 2", page.result(page.call("alpha", "add", 2.0)), okResult("v1:2"))
			page.waitLabel("alpha", "v1:2")
			settled := len(page.states("alpha"))
			opensBefore, closesBefore := hand.natives()
			// A running call that commits once the test lets it go on.
			running := page.call("alpha", "slowAdd", "running")
			hand.entered("running")

			result := s.editAndApply(t, "counter", counterPlugin(t, counterCode{Code: "v2", Placement: placement, Native: true}))
			expectEqual(t, "failures", rolloutFailures(result), []string{})
			step := "server/server"
			if placement == "worker" {
				step = "project/project|p1"
			}
			expectEqual(t, "fates", rolloutFates(result), []string{step + ": reload.store restarted", step + ": reload.counter(p1) restarted"})

			// The drain: still `live` with the last values; a call sent now waits at the gate.
			arriving := page.call("alpha", "add", 1.0)
			sleepFor(300 * time.Millisecond)
			expectEqual(t, "the arriving call", page.peek(arriving), pendingResult)
			expectEqual(t, "the running call", page.peek(running), pendingResult)
			expectEqual(t, "states while draining", page.states("alpha")[settled:], []string{})
			expectEqual(t, "state while draining", page.stateKind("alpha"), "live")
			expectEqual(t, "label while draining", page.labelText("alpha"), "v1:2")

			// The running call commits through the store (swapped: its new code answers) and finishes on the old counter; the page
			// sees that commit through the old code before `loading`.
			hand.open("running")
			expectEqual(t, "the running call's answer", page.result(running), okResult("v2:12"))
			expectEqual(t, "the arriving call's answer", page.result(arriving), okResult("v2:13"))
			page.waitLabel("alpha", "v2:13")
			page.waitState("alpha", "live")
			expectEqual(t, "states", page.states("alpha")[settled:], []string{"loading", "live"})
			frames := page.frames("alpha")
			from := 0
			for i, frame := range frames {
				if frame == "v1:2" {
					from = i
				}
			}
			since := frames[from:]
			expectEqual(t, "frames since v1:2", since[:2], []delta.JsonValue{"v1:2", "v1:12"})
			expectEqual(t, "last frame", since[len(since)-1], "v2:13")
			for _, frame := range since {
				if isFailedFrame(frame) {
					t.Fatalf("a failed frame: %v", since)
				}
			}
			// The native closed for the reload and reopened on the new code; documents and tables unchanged.
			opensAfter, closesAfter := hand.natives()
			expectEqual(t, "opens", opensAfter[len(opensBefore):], []string{"v2"})
			expectEqual(t, "closes", closesAfter[len(closesBefore):], []string{"v1:reload"})
			expectEqual(t, "count", page.countText("alpha"), "13")
			expectEqual(t, "visits", page.result(page.call("alpha", "visits", nil)), okResult(3))
		})
	}
}

func TestAReloadRestartingAnObjectWithNativesCutsARunningCallOffAtTheReloadDrainBoundThenRunsTheWaitingCallOnTheNewCode(t *testing.T) {
	for _, placement := range []string{"worker", "server"} {
		t.Run("on the "+placement, func(t *testing.T) {
			s := startReloadApp(t, map[string]map[string]string{
				"latch":   latchPlugin(t),
				"counter": counterPlugin(t, counterCode{Code: "v1", Placement: placement, Native: true}),
			})
			page := reloadSignIn(t, s)
			page.hold("alpha", counterContract(t, placement, "v1"), "p1", 0, "label")
			page.waitState("alpha", "live")
			hand := latchesOf(t, s, placement)
			stuck := page.call("alpha", "slow", "stuck")
			hand.entered("stuck")
			started := time.Now()
			s.editAndApply(t, "counter", counterPlugin(t, counterCode{Code: "v2", Placement: placement, Native: true}))
			arriving := page.call("alpha", "code", nil)
			cut := page.result(stuck)
			// The kit passes `reloadDrainTimeout` to the server and the workers: the cut comes before `closeTimeout`.
			if elapsed := time.Since(started); elapsed < reloadTimeouts.ReloadDrain-100*time.Millisecond || elapsed >= reloadTimeouts.Close {
				t.Fatalf("the cut came after %v, want within [%v, %v)", elapsed, reloadTimeouts.ReloadDrain-100*time.Millisecond, reloadTimeouts.Close)
			}
			if cut.Status != "error" || cut.Code != "released" {
				t.Fatalf("the cut call = %+v, want an error released", cut)
			}
			expectEqual(t, "the waiting call", page.result(arriving), okResult("v2"))
			page.waitState("alpha", "live")
		})
	}
}
