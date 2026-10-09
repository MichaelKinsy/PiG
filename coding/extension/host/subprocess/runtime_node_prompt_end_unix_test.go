//go:build unix

package subprocess

import (
	"syscall"
	"testing"
	"time"
)

// The wait after a prompt's last call ends with the process's connection or the process itself: a closed connection or a SIGTERM lets the process exit at once, as it would have without the wait.
func TestNodePromptEndWaitEndsWithTheProcess(t *testing.T) {
	t.Parallel()
	for _, isolation := range []string{"", "isolated"} {
		name := "packed"
		if isolation != "" {
			name = isolation
		}
		for _, end := range []string{"close", "sigterm"} {
			t.Run(name+"/"+end, func(t *testing.T) {
				t.Parallel()
				f := startPromptEnd(t, "print", isolation, false)
				pid := f.pid()
				f.end("agent_settled")
				if end == "close" {
					f.host.mu.Lock()
					me := f.host.exts["prompt-end"]
					f.host.mu.Unlock()
					if err := me.connection().Close("test closes the connection"); err != nil {
						t.Fatal(err)
					}
				} else if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
					t.Fatal(err)
				}
				deadline := time.Now().Add(10 * time.Second)
				for syscall.Kill(pid, 0) == nil {
					if time.Now().After(deadline) {
						t.Fatalf("process %d still runs 10s after the %s", pid, end)
					}
					time.Sleep(10 * time.Millisecond)
				}
			})
		}
	}
}
