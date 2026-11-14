//go:build !pig_strip_node_extensions

package subprocess

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A pig that exits through TerminateProcesses (the exit of every mode) leaves no packed log behind, whether the cell
// had started or was still starting: the log is kept only for a failure the user is told about, and the exit of the host
// process is not one. The host that created the log removes it before the exit, not a goroutine the exit outruns.
func TestPackedStderrLogTerminateProcessesLeavesNoLog(t *testing.T) {
	for _, phase := range []string{"started", "starting"} {
		t.Run(phase, func(t *testing.T) {
			tmp := privatePackedLogTemp(t)
			source := `export default function(pi) { pi.registerCommand("up", {handler: async () => {}}); }`
			if phase == "starting" {
				// The factory never settles, so the start is still waiting for its member when the exit comes.
				source = `export default async function(pi) { await new Promise(() => {}); }`
			}
			entry := filepath.Join(t.TempDir(), "member.mjs")
			if err := os.WriteFile(entry, []byte(source), 0o600); err != nil {
				t.Fatal(err)
			}
			configs := []ExtConfig{{Name: "member", Source: entry, Enabled: true}}
			h := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
			t.Cleanup(func() { h.Shutdown("test done") })
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			loaded := make(chan struct{})
			go func() {
				defer close(loaded)
				_, _ = h.LoadAll(ctx, configs)
			}()
			deadline := time.Now().Add(60 * time.Second)
			for {
				h.mu.Lock()
				spawned := false
				for _, process := range h.packedProcesses {
					spawned = spawned || (process.processTree != nil && process.stderrLog != nil)
				}
				h.mu.Unlock()
				if spawned {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("the packed process never started")
				}
				time.Sleep(2 * time.Millisecond)
			}
			if phase == "started" {
				select {
				case <-loaded:
				case <-time.After(60 * time.Second):
					t.Fatal("the load did not end")
				}
			}
			h.TerminateProcesses()
			assertPackedLogs(t, tmp)
			cancel()
			select {
			case <-loaded:
			case <-time.After(60 * time.Second):
				t.Fatal("the load did not end after the exit")
			}
			assertPackedLogs(t, tmp)
		})
	}
}
