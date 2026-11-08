package app

import (
	"testing"
	"time"
)

// Ports packages/chord/test/node/reload/restart.test.ts (Pi main c23b9ec1), "a reload restarting an object with natives" (design §21
// "What a user sees during a reload", as corrected 2026-11-04): while the old code drains its running calls (up to `closeTimeout`)
// the page stays `live` with its last values and whatever the draining old code commits; calls sent meanwhile wait at the gate;
// then `loading` briefly, then a fresh snapshot from the new code, which answers the waiting calls; documents and tables
// unchanged, natives reopened. For a counter holding a native on a project's worker and on the server. Through a real page in
// Chromium (reload_web_support_test.go), as Pi's.

func TestAReloadRestartingAnObjectWithNativesStaysLiveWhileTheOldCodeDrainsThenShowsLoadingAndAFreshSnapshotFromTheNewCode(t *testing.T) {
	for _, placement := range []string{"worker", "server"} {
		t.Run("on the "+placement, func(t *testing.T) {
			tab := startReloadTab(t)
			s := startReload(t, map[string]map[string]string{
				"latch":   latchPlugin(t),
				"counter": counterPlugin(t, counterCode{Code: "v1", Placement: placement, Native: true}),
				"alpha":   reloadWebPlugin(t, webCode{Name: "alpha", Code: "1", Counter: &reloadWebCounter{Placement: placement, Contract: "v1"}}),
			})
			hand := latchesOf(t, s, placement)
			// A failed expectation leaves the call waiting: open it so the close does not wait for the drain.
			t.Cleanup(func() { hand.open("running") })
			tab.signIn(s, "alpha")
			tab.waitText("alpha", ".state", "live")
			expectEqual(t, "add 2", tab.result(tab.call("alpha", "add", 2)), okResult("v1:2"))
			tab.waitText("alpha", ".label", "v1:2")
			settled := len(tab.states("alpha"))
			opensBefore, closesBefore := hand.natives()
			// A running call that commits once the test lets it go on.
			running := tab.call("alpha", "slowAdd", "running")
			hand.entered("running")

			result := s.editAndApply(t, "counter", counterPlugin(t, counterCode{Code: "v2", Placement: placement, Native: true}))
			expectEqual(t, "failures", rolloutFailures(result), []string{})
			step := "server/server"
			if placement == "worker" {
				step = "project/project|p1"
			}
			expectEqual(t, "fates", rolloutFates(result), []string{step + ": reload.store restarted", step + ": reload.counter(p1) restarted"})

			// The drain: still `live` with the last values; a call sent now waits at the gate.
			arriving := tab.call("alpha", "add", 1)
			sleepFor(300 * time.Millisecond)
			expectEqual(t, "the arriving call", tab.peek(arriving), pendingResult)
			expectEqual(t, "the running call", tab.peek(running), pendingResult)
			expectEqual(t, "states while draining", tab.states("alpha")[settled:], []string{})
			expectEqual(t, "state while draining", tab.shows("alpha", ".state"), "live")
			expectEqual(t, "label while draining", tab.shows("alpha", ".label"), "v1:2")

			// The running call commits through the store (swapped: its new code answers) and finishes on the old counter; the page
			// sees that commit through the old code before `loading`.
			hand.open("running")
			expectEqual(t, "the running call's answer", tab.result(running), okResult("v2:12"))
			expectEqual(t, "the arriving call's answer", tab.result(arriving), okResult("v2:13"))
			tab.waitText("alpha", ".label", "v2:13")
			tab.waitText("alpha", ".state", "live")
			expectEqual(t, "states", tab.states("alpha")[settled:], []string{"loading", "live"})
			frames := tab.frames("alpha")
			from := 0
			for i, frame := range frames {
				if frame == "v1:2" {
					from = i
				}
			}
			since := frames[from:]
			expectEqual(t, "frames since v1:2", since[:2], []any{"v1:2", "v1:12"})
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
			expectEqual(t, "count", tab.shows("alpha", ".count"), "13")
			expectEqual(t, "visits", tab.result(tab.call("alpha", "visits")), okResult(3))
		})
	}
}

func TestAReloadRestartingAnObjectWithNativesCutsARunningCallOffAtTheReloadDrainBoundThenRunsTheWaitingCallOnTheNewCode(t *testing.T) {
	for _, placement := range []string{"worker", "server"} {
		t.Run("on the "+placement, func(t *testing.T) {
			tab := startReloadTab(t)
			s := startReload(t, map[string]map[string]string{
				"latch":   latchPlugin(t),
				"counter": counterPlugin(t, counterCode{Code: "v1", Placement: placement, Native: true}),
				"alpha":   reloadWebPlugin(t, webCode{Name: "alpha", Code: "1", Counter: &reloadWebCounter{Placement: placement, Contract: "v1"}}),
			})
			tab.signIn(s, "alpha")
			tab.waitText("alpha", ".state", "live")
			hand := latchesOf(t, s, placement)
			stuck := tab.call("alpha", "slow", "stuck")
			hand.entered("stuck")
			started := time.Now()
			s.editAndApply(t, "counter", counterPlugin(t, counterCode{Code: "v2", Placement: placement, Native: true}))
			arriving := tab.call("alpha", "code")
			cut := tab.result(stuck)
			// The kit passes `reloadDrainTimeout` to the server and the workers: the cut comes before `closeTimeout`.
			if elapsed := time.Since(started); elapsed < reloadTimeouts.ReloadDrain-100*time.Millisecond || elapsed >= reloadTimeouts.Close {
				t.Fatalf("the cut came after %v, want within [%v, %v)", elapsed, reloadTimeouts.ReloadDrain-100*time.Millisecond, reloadTimeouts.Close)
			}
			if cut.Status != "error" || cut.Code != "released" {
				t.Fatalf("the cut call = %+v, want an error released", cut)
			}
			expectEqual(t, "the waiting call", tab.result(arriving), okResult("v2"))
			tab.waitText("alpha", ".state", "live")
		})
	}
}
