package app

import (
	"slices"
	"testing"
)

// Ports the worker rows of packages/chord/test/node/reload/failed.test.ts (Pi main c23b9ec1), "a reload whose new code fails". The
// two web-plugin rows (a web plugin whose new build fails, or whose new script throws in the page, on its old component and state)
// are in reload_web_test.go. Through a real page in Chromium (reload_web_support_test.go), as Pi's.

func TestAReloadWhoseNewCodeFailsKeepsAWorkerPluginWhoseNewBuildEvaluationSetupFailsOnItsOldCodeThePageUntouched(t *testing.T) {
	for _, broken := range []string{"build", "evaluation", "setup"} {
		t.Run(broken, func(t *testing.T) {
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
			settled := len(tab.states("alpha"))
			frames := len(tab.frames("alpha"))

			result := s.editAndApply(t, "counter", counterPlugin(t, counterCode{Code: "v2", Placement: "worker", Broken: broken}))
			// Reported to the app: the worker kept the plugin's old code, and it stays dirty for the next apply.
			expectEqual(t, "failures", rolloutFailures(result), []string{"project/project|p1: @test/counter"})
			if !slices.Contains(s.dirty(), "@test/counter") {
				t.Fatalf("dirty = %v, want @test/counter in it", s.dirty())
			}

			// The page: nothing moved, calls answered by the old code, the document as it was.
			expectEqual(t, "code", tab.result(tab.call("alpha", "code")), okResult("v1"))
			expectEqual(t, "add 1", tab.result(tab.call("alpha", "add", 1)), okResult("v1:3"))
			tab.waitText("alpha", ".label", "v1:3")
			expectEqual(t, "states", tab.states("alpha")[settled:], []string{})
			expectEqual(t, "frames", tab.frames("alpha")[frames:], []any{"v1:3"})
			expectEqual(t, "visits", tab.result(tab.call("alpha", "visits")), okResult(2))
			expectEqual(t, "draft", tab.value("alpha", ".draft"), "half-typed")
			expectEqual(t, "loads", tab.loads(), 1)
		})
	}
}

// A case the table leaves out (the audit's "new code that fails at activation"): the new code passes staging, so the commit applies
// it; its native then fails to open. A runtime state (§21: failed opens stay per object after the commit): the old code is gone, the
// page shows the failure with the last values, and the next fix applies.
func TestAReloadWhoseNewCodeFailsShowsAFailureWithTheLastValuesWhenNewCodePassesStagingAndFailsAtActivationUntilAFix(t *testing.T) {
	tab := startReloadTab(t)
	s := startReload(t, map[string]map[string]string{
		"latch":   latchPlugin(t),
		"counter": counterPlugin(t, counterCode{Code: "v1", Placement: "worker", Native: true}),
		"alpha":   reloadWebPlugin(t, webCode{Name: "alpha", Code: "1", Counter: reloadWebCounterV1}),
	})
	tab.signIn(s, "alpha")
	expectEqual(t, "add 2", tab.result(tab.call("alpha", "add", 2)), okResult("v1:2"))
	tab.waitText("alpha", ".label", "v1:2")
	result := s.editAndApply(t, "counter", counterPlugin(t, counterCode{Code: "v2", Placement: "worker", Native: true, Broken: "open"}))
	expectEqual(t, "failures", rolloutFailures(result), []string{})
	if !slices.Contains(rolloutFates(result), "project/project|p1: reload.counter(p1) restarted") {
		t.Fatalf("fates = %v, want reload.counter(p1) restarted among them", rolloutFates(result))
	}
	expectEqual(t, "dirty", s.dirty(), []string{})
	tab.waitText("alpha", ".state", "failed")
	expectEqual(t, "label", tab.shows("alpha", ".label"), "v1:2")
	if got := tab.result(tab.call("alpha", "code")); got.Status != "error" {
		t.Fatalf("a call on the failed reference = %+v, want an error", got)
	}

	s.editAndApply(t, "counter", counterPlugin(t, counterCode{Code: "v3", Placement: "worker", Native: true}))
	tab.waitText("alpha", ".state", "live")
	tab.waitText("alpha", ".label", "v3:2")
	expectEqual(t, "code", tab.result(tab.call("alpha", "code")), okResult("v3"))
}
