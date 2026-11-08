package app

import (
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
)

// Ports the worker rows of packages/chord/test/node/reload/failed.test.ts (Pi main c23b9ec1), "a reload whose new code fails". The
// two web-plugin rows (a web plugin whose new build fails, or whose new script throws in the page, on its old component and state)
// need the web step and a page's DOM: the browser rows.

func TestAReloadWhoseNewCodeFailsKeepsAWorkerPluginWhoseNewBuildEvaluationSetupFailsOnItsOldCodeThePageUntouched(t *testing.T) {
	for _, broken := range []string{"build", "evaluation", "setup"} {
		t.Run(broken, func(t *testing.T) {
			s := startReloadApp(t, map[string]map[string]string{
				"latch":   latchPlugin(t),
				"counter": counterPlugin(t, counterCode{Code: "v1", Placement: "worker"}),
			})
			page := reloadSignIn(t, s)
			page.hold("alpha", counterContract(t, "worker", "v1"), "p1", 0, "label")
			expectEqual(t, "add 2", page.result(page.call("alpha", "add", 2.0)), okResult("v1:2"))
			page.waitLabel("alpha", "v1:2")
			page.waitState("alpha", "live")
			settled := len(page.states("alpha"))
			frames := len(page.frames("alpha"))

			result := s.editAndApply(t, "counter", counterPlugin(t, counterCode{Code: "v2", Placement: "worker", Broken: broken}))
			// Reported to the app: the worker kept the plugin's old code, and it stays dirty for the next apply.
			expectEqual(t, "failures", rolloutFailures(result), []string{"project/project|p1: @test/counter"})
			if !slices.Contains(s.dirty(), "@test/counter") {
				t.Fatalf("dirty = %v, want @test/counter in it", s.dirty())
			}

			// The page: nothing moved, calls answered by the old code, the document as it was.
			expectEqual(t, "code", page.result(page.call("alpha", "code", nil)), okResult("v1"))
			expectEqual(t, "add 1", page.result(page.call("alpha", "add", 1.0)), okResult("v1:3"))
			page.waitLabel("alpha", "v1:3")
			expectEqual(t, "states", page.states("alpha")[settled:], []string{})
			expectEqual(t, "frames", page.frames("alpha")[frames:], []delta.JsonValue{"v1:3"})
			expectEqual(t, "visits", page.result(page.call("alpha", "visits", nil)), okResult(2))
		})
	}
}

// A case the table leaves out (the audit's "new code that fails at activation"): the new code passes staging, so the commit applies
// it; its native then fails to open. A runtime state (§21: failed opens stay per object after the commit): the old code is gone, the
// page shows the failure with the last values, and the next fix applies.
func TestAReloadWhoseNewCodeFailsShowsAFailureWithTheLastValuesWhenNewCodePassesStagingAndFailsAtActivationUntilAFix(t *testing.T) {
	s := startReloadApp(t, map[string]map[string]string{
		"latch":   latchPlugin(t),
		"counter": counterPlugin(t, counterCode{Code: "v1", Placement: "worker", Native: true}),
	})
	page := reloadSignIn(t, s)
	page.hold("alpha", counterContract(t, "worker", "v1"), "p1", 0, "label")
	expectEqual(t, "add 2", page.result(page.call("alpha", "add", 2.0)), okResult("v1:2"))
	page.waitLabel("alpha", "v1:2")
	result := s.editAndApply(t, "counter", counterPlugin(t, counterCode{Code: "v2", Placement: "worker", Native: true, Broken: "open"}))
	expectEqual(t, "failures", rolloutFailures(result), []string{})
	if !slices.Contains(rolloutFates(result), "project/project|p1: reload.counter(p1) restarted") {
		t.Fatalf("fates = %v, want reload.counter(p1) restarted among them", rolloutFates(result))
	}
	expectEqual(t, "dirty", s.dirty(), []string{})
	page.waitState("alpha", "failed")
	expectEqual(t, "label", page.labelText("alpha"), "v1:2")
	if got := page.result(page.call("alpha", "code", nil)); got.Status != "error" {
		t.Fatalf("a call on the failed reference = %+v, want an error", got)
	}

	s.editAndApply(t, "counter", counterPlugin(t, counterCode{Code: "v3", Placement: "worker", Native: true}))
	page.waitState("alpha", "live")
	page.waitLabel("alpha", "v3:2")
	expectEqual(t, "code", page.result(page.call("alpha", "code", nil)), okResult("v3"))
}
