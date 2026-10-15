//go:build unix

package subprocess

import (
	"strings"
	"syscall"
	"testing"
	"time"
)

// On Pi's one event loop the immediate and the zero-delay timer an earlier extension's handler queued run before a later handler's 100 ms timer resumes it, however long the OS keeps the process off the CPU (TestNodePromptEndWaitEndsWhenAnotherProcessTurnsTheLoop). With separate processes that order cannot rest on the clock: a loaded host can keep the earlier process from reading the forwarded loop_turned until the later handler's timer has fired. Here the earlier process is stopped across that timer. Before the later process waited for the turn to be taken, its handler resumed and answered first: "first session_shutdown", "second session_shutdown", "second session_shutdown done", "first immediate live", "first timeout stale".
func TestNodeLoopTurnWaitsUntilAStalledProcessTookIt(t *testing.T) {
	t.Parallel()
	for _, layout := range []string{"isolated", "quarantined"} {
		for _, mode := range []string{"print", "json"} {
			for _, event := range []string{"session_shutdown", "agent_settled"} {
				t.Run(layout+"/"+mode+"/"+event, func(t *testing.T) {
					t.Parallel()
					f, loaded := loadLoopTurnPair(t, layout, mode, loopTurnSecondSource)
					runLoopTurnEvent(f, loaded, event, func() {
						pid := f.pid()
						if err := syscall.Kill(pid, syscall.SIGSTOP); err != nil {
							t.Fatal(err)
						}
						// The stall outlasts the later handler's 100 ms timer.
						resumed := make(chan error, 1)
						go func() {
							time.Sleep(300 * time.Millisecond)
							resumed <- syscall.Kill(pid, syscall.SIGCONT)
						}()
						t.Cleanup(func() {
							if err := <-resumed; err != nil {
								t.Errorf("resume first: %v", err)
							}
						})
					})
					want := []string{"first " + event, "second " + event, "first immediate live", "first timeout live", "second " + event + " done"}
					if got := f.lines(len(want)); strings.Join(got, "\n") != strings.Join(want, "\n") {
						t.Fatalf("order:\n got %q\nwant %q", got, want)
					}
				})
			}
		}
	}
}
