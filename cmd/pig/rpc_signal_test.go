//go:build unix

package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// Pi 0.87.1 rpc-mode.ts:366-378,728-744 disposes the runtime and exits 143/129 without needing stdin EOF.
func TestRPCSignalWithOpenStdin(t *testing.T) {
	for _, sig := range []syscall.Signal{syscall.SIGTERM, syscall.SIGHUP} {
		t.Run(sig.String(), func(t *testing.T) {
			home := t.TempDir()
			marker := filepath.Join(home, "shutdown-marker")
			extension := filepath.Join(home, "shutdown.mjs")
			if err := os.WriteFile(extension, []byte(`import {writeFileSync} from "node:fs";
export default function(pi) { pi.on("session_shutdown", () => writeFileSync(process.env.RPC_SHUTDOWN_MARKER, "disposed")); }
`), 0600); err != nil {
				t.Fatal(err)
			}
			p := startRPCProcessAt(t, t.TempDir(), []string{"HOME=" + home, "PIG_HOME=" + home, "PIG_CODING_AGENT_DIR=" + filepath.Join(home, "agent"), "RPC_SHUTDOWN_MARKER=" + marker}, "--offline", "--no-extensions", "--no-session", "-e", extension)
			p.send(`{"id":"ready","type":"get_state"}`)
			p.await("ready", func(record rpcRecord) bool { return isSuccessResponse(record, "ready") })
			if err := p.cmd.Process.Signal(sig); err != nil {
				t.Fatal(err)
			}
			exited := make(chan error, 1)
			go func() { exited <- p.cmd.Wait() }()
			select {
			case err := <-exited:
				p.exited = true
				var exit *exec.ExitError
				if !errors.As(err, &exit) || exit.ExitCode() != 128+int(sig) {
					t.Fatalf("wait = %v, want %d", err, 128+int(sig))
				}
				if data, err := os.ReadFile(marker); err != nil || string(data) != "disposed" {
					t.Fatalf("runtime cleanup did not finish: %q, %v", data, err)
				}
			case <-time.After(p.budget):
				_ = p.cmd.Process.Kill()
				<-exited
				p.exited = true
				t.Fatal("RPC ignored termination with stdin still open")
			}
		})
	}
}
