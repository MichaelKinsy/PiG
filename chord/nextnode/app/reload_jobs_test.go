package app

import (
	"slices"
	"testing"
	"time"
)

// Ports packages/chord/test/node/reload/jobs.test.ts (Pi main c23b9ec1), "a job running across a reload" (the audit's "jobs running
// across a reload"; design §15 "Versions"): a job run started from the page, running on a worker while its object restarts (with or
// without natives: every reload restarts). The in-memory run ends `aborted { reload }` at the old attempt's seal and `finish` runs on
// the new code. The page watches the finished runs' entries.

// untilFrames returns once the tasks view's last frame satisfies ok.
func (p *reloadPage) untilFrames(what string, ok func(last string) bool) {
	p.t.Helper()
	p.until(what, reloadWait, func() bool {
		frames := p.frames("tasks")
		return len(frames) > 0 && ok(jsonText(frames[len(frames)-1]))
	})
}

func TestAJobRunningAcrossAReloadAcrossARestartOf(t *testing.T) {
	for _, change := range []struct {
		name   string
		native bool
	}{{"pure code", false}, {"code holding natives", true}} {
		t.Run(change.name, func(t *testing.T) {
			entry := "v2-finish(aborted:reload)"
			s := startReloadApp(t, map[string]map[string]string{"latch": latchPlugin(t), "tasks": tasksPlugin(t, "v1", change.native)})
			hand := latchesOf(t, s, "worker")
			page := signIn(t, s)
			page.hold("tasks", tasksContract, "p1", 10*time.Second, "entries")
			page.waitState("tasks", "live")
			started := page.result(page.call("tasks", "start", "a"))
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
			page.untilFrames("a finished run's entry", func(last string) bool { return last != "[]" })
			frames := page.frames("tasks")
			expectEqual(t, "entries", jsonText(frames[len(frames)-1]), jsonText([]any{entry}))
			// A run started now runs on the new code.
			page.call("tasks", "start", "b")
			hand.entered("b")
			hand.open("b")
			page.untilFrames("two entries", func(last string) bool { return last == jsonText([]any{entry, "v2-finish(done:run-v2:b)"}) })
		})
	}
}
