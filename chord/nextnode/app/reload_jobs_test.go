package app

import (
	"slices"
	"testing"
	"time"
)

// Ports packages/chord/test/node/reload/jobs.test.ts (Pi main c23b9ec1), "a job running across a reload" (the audit's "jobs running
// across a reload"; design §15 "Versions"): a job run started from the page, running on a worker while its object restarts (with or
// without natives: every reload restarts). The in-memory run ends `aborted { reload }` at the old attempt's seal and `finish` runs on
// the new code. The page watches the finished runs' entries. Through a real page in Chromium (reload_web_support_test.go), as Pi's.

func TestAJobRunningAcrossAReloadAcrossARestartOf(t *testing.T) {
	for _, change := range []struct {
		name   string
		native bool
	}{{"pure code", false}, {"code holding natives", true}} {
		t.Run(change.name, func(t *testing.T) {
			entry := "v2-finish(aborted:reload)"
			tab := startReloadTab(t)
			s := startReload(t, map[string]map[string]string{"latch": latchPlugin(t), "tasks": tasksPlugin(t, "v1", change.native)})
			hand := latchesOf(t, s, "worker")
			t.Cleanup(func() { hand.open("a"); hand.open("b") })
			tab.signIn(s)
			tab.hold("tasks", "tasks", 10*time.Second, "p1", "entries")
			tab.waitState("tasks", "live")
			started := tab.result(tab.call("tasks", "start", "a"))
			if started.Status != "ok" {
				t.Fatalf("start a = %+v", started)
			}
			hand.entered("a")

			result := s.editAndApply(t, "tasks", tasksPlugin(t, "v2", change.native))
			expectEqual(t, "failures", rolloutFailures(result), []string{})
			fates := rolloutFates(result)
			if want := "project/project|p1: reload.runner restarted"; !slices.Contains(fates, want) {
				t.Fatalf("fates = %v, want %q among them", fates, want)
			}
			hand.open("a")
			tab.browser.Wait(`globalThis.reload.frames.tasks?.some((frame) => Array.isArray(frame) && frame.length > 0)`)
			frames := tab.frames("tasks")
			expectEqual(t, "entries", frames[len(frames)-1], []any{entry})
			// A run started now runs on the new code.
			tab.call("tasks", "start", "b")
			hand.entered("b")
			hand.open("b")
			tab.browser.Wait(`globalThis.reload.frames.tasks?.at(-1)?.length === 2`)
			frames = tab.frames("tasks")
			expectEqual(t, "two entries", frames[len(frames)-1], []any{entry, "v2-finish(done:run-v2:b)"})
		})
	}
}
