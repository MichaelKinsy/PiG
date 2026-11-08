//go:build unix

package cli

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// pig additive (D102): exit.log names the cause that ended the process. In RPC mode a single signal during a shutdown that stdin's end started does not end the process (Pi main ace560c30), so it does not claim the line as "received SIGTERM" before an exit 0; the exit that ends the process writes a line that says the shutdown completed after the signal.
func TestRPCExitLogNamesWhatEndedTheProcess(t *testing.T) {
	for _, tc := range []struct {
		name    string
		prepare func(t *testing.T, p *rpcProcess, report string)
		code    int
		want    string
		notWant string
	}{
		{
			name: "one signal during a stdin-end shutdown",
			prepare: func(t *testing.T, p *rpcProcess, report string) {
				p.closeInput()
				awaitRPCReport(t, report, "session_shutdown")
				signalRPC(t, p, syscall.SIGTERM)
			},
			code:    0,
			want:    "received SIGTERM during shutdown, which completed (exit 0)",
			notWant: "received SIGTERM\n",
		},
		{
			name: "a signal that starts the shutdown",
			prepare: func(t *testing.T, p *rpcProcess, report string) {
				signalRPC(t, p, syscall.SIGTERM)
			},
			code: 143,
			want: "received SIGTERM",
		},
		{
			name: "a repeated signal during a stdin-end shutdown",
			prepare: func(t *testing.T, p *rpcProcess, report string) {
				p.closeInput()
				awaitRPCReport(t, report, "session_shutdown")
				signalRPC(t, p, syscall.SIGTERM, syscall.SIGTERM)
			},
			code: 143,
			want: "received SIGTERM again during shutdown (exit 143)",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			agentDir := filepath.Join(t.TempDir(), "agent")
			p, report := startRPCShutdownFixture(t, "PIG_CODING_AGENT_DIR="+agentDir, rpcSlowShutdown, "RPC_SHUTDOWN_RECORD_DONE=1")
			awaitReady(p)
			tc.prepare(t, p, report)
			requireExited(t, waitForRPCTermination(t, p), tc.code)
			data, err := os.ReadFile(codingagent.ExitLogPath(agentDir))
			if err != nil {
				t.Fatal(err)
			}
			lines := strings.Split(strings.TrimSpace(string(data)), "\n")
			if len(lines) != 1 || !strings.Contains(lines[0], tc.want) {
				t.Fatalf("exit.log = %q, want one line with %q", data, tc.want)
			}
			if tc.notWant != "" && strings.HasSuffix(lines[0], strings.TrimSuffix(tc.notWant, "\n")) {
				t.Fatalf("exit.log = %q, want no bare %q", data, tc.notWant)
			}
		})
	}
}
