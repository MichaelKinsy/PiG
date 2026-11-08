package app

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins"
)

// Ports packages/chord/test/node/reload/pure.test.ts (Pi main c23b9ec1), "a reload of pure code" (design §21 "What a user sees
// during a reload"): a restart, as for code holding natives. While the old code drains its running call the view stays `live`
// with its last values; a call arriving meanwhile waits at the gate and runs on the new code, never `not_ready`; then `loading`
// briefly and a fresh snapshot from the new code; documents and tables unchanged. For a counter on a project's worker and on the
// server. Through a real page in Chromium (reload_web_support_test.go), as Pi's.

func expectEqual(t testing.TB, what string, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s = %#v, want %#v", what, got, want)
	}
}

// isFailedFrame is whether a frame of the probe's record is a watch failure.
func isFailedFrame(frame any) bool {
	text, ok := frame.(string)
	return ok && strings.HasPrefix(text, "failed")
}

func TestAReloadOfPureCodeStaysLiveWhileARunningCallFinishesOnTheOldCodeThenShowsTheNewCodesSnapshot(t *testing.T) {
	for _, placement := range []string{"worker", "server"} {
		t.Run("on the "+placement, func(t *testing.T) {
			tab := startReloadTab(t)
			s := startReload(t, map[string]map[string]string{
				"latch":   latchPlugin(t),
				"counter": counterPlugin(t, counterCode{Code: "v1", Placement: placement}),
				"alpha":   reloadWebPlugin(t, webCode{Name: "alpha", Code: "1", Counter: &reloadWebCounter{Placement: placement, Contract: "v1"}}),
			})
			tab.signIn(s, "alpha")
			tab.waitText("alpha", ".label", "v1:0")
			expectEqual(t, "add 2", tab.result(tab.call("alpha", "add", 2)), okResult("v1:2"))
			tab.waitText("alpha", ".label", "v1:2")
			tab.typeInto("alpha", ".draft", "half-typed")
			settled := len(tab.states("alpha"))
			hand := latchesOf(t, s, placement)
			running := tab.call("alpha", "slow", "running")
			hand.entered("running")

			// The new code computes the label differently (its code name) and the count the same.
			s.edit(t, "counter", counterPlugin(t, counterCode{Code: "v2", Placement: placement}))
			type applied struct {
				result plugins.RolloutResult
				err    error
			}
			applying := make(chan applied, 1)
			go func() {
				result, err := s.app.ApplyChanges(background)
				applying <- applied{result, err}
			}()
			// Calls sent while the rollout runs: each answered by the old or the new code, never `not_ready`.
			var during []int
			var done applied
		rollout:
			for {
				select {
				case done = <-applying:
					break rollout
				default:
					during = append(during, tab.call("alpha", "code"))
					sleepFor(5 * time.Millisecond)
				}
			}
			if done.err != nil {
				t.Fatal(done.err)
			}
			expectEqual(t, "failures", rolloutFailures(done.result), []string{})
			step := "server/server"
			if placement == "worker" {
				step = "project/project|p1"
			}
			expectEqual(t, "fates", rolloutFates(done.result), []string{step + ": reload.store restarted", step + ": reload.counter(p1) restarted"})

			// Arriving after the commit, while the old call still runs: it waits at the gate; the view stays live with the last values.
			arriving := tab.call("alpha", "add", 1)
			sleepFor(300 * time.Millisecond)
			expectEqual(t, "the arriving call", tab.peek(arriving), pendingResult)
			expectEqual(t, "states while draining", tab.states("alpha")[settled:], []string{})
			expectEqual(t, "label while draining", tab.shows("alpha", ".label"), "v1:2")
			hand.open("running")
			expectEqual(t, "the running call", tab.result(running), okResult("v1:running"))
			expectEqual(t, "the arriving call's answer", tab.result(arriving), okResult("v2:3"))
			for _, index := range during {
				answer := tab.result(index)
				if answer != okResult("v1") && answer != okResult("v2") {
					t.Fatalf("a call during the rollout was answered %+v", answer)
				}
			}

			tab.waitText("alpha", ".label", "v2:3")
			tab.waitText("alpha", ".state", "live")
			// `loading` once the old attempt ended, then the new code's fresh snapshot; never a failure.
			expectEqual(t, "states", tab.states("alpha")[settled:], []string{"loading", "live"})
			frames := tab.frames("alpha")
			for _, frame := range frames {
				if isFailedFrame(frame) {
					t.Fatalf("a failed frame: %v", frames)
				}
			}
			expectEqual(t, "last frame", frames[len(frames)-1], "v2:3")
			// Documents and tables unchanged: the count went on from 2, the table kept the old code's rows.
			expectEqual(t, "count", tab.shows("alpha", ".count"), "3")
			expectEqual(t, "visits", tab.result(tab.call("alpha", "visits")), okResult(2))
			// The page itself was not touched.
			expectEqual(t, "draft", tab.value("alpha", ".draft"), "half-typed")
			expectEqual(t, "loads", tab.loads(), 1)
		})
	}
}

// The audit's F1, as decided (decision "Reload restarts every affected object"): the server object restarts; its successor's hold of
// the worker object starts `loading`, so the view shows `loading` once, then the new code.
func TestAReloadOfPureCodeShowsLoadingOnceThenTheNewCodeForAPureServerObjectOverAWorkerObjectWhenOnlyItReloads(t *testing.T) {
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
	result := s.editAndApply(t, "summary", summaryPlugin(t, "s2"))
	expectEqual(t, "failures", rolloutFailures(result), []string{})
	tab.waitText("alpha", ".label", "s2(0)")
	expectEqual(t, "code", tab.result(tab.call("alpha", "code")), okResult("s2"))
	expectEqual(t, "states", tab.states("alpha")[settled:], []string{"loading", "live"})
	expectEqual(t, "fates", rolloutFates(result), []string{"server/server: reload.summary(p1) restarted"})
}
