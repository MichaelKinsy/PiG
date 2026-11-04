package codingagent

import (
	"os"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// SIGINT ends the process through handleInterruptSignal's os.Exit(130). If the owner loop were asked to return from Run as well, main would reach exit 0 on
// its own and, under load, win against that os.Exit: the session ended with status 0 instead of 130.
func TestInterruptShutdownLeavesTheOwnerLoopRunning(t *testing.T) {
	freshEnding(t)
	agentDir := t.TempDir()
	disposals := 0
	runner := inproc.NewRunner([]extension.Extension{{Path: "cleanup", Handlers: map[string][]extension.HandlerFn{
		"session_shutdown": {func(...any) (any, error) { disposals++; return nil, nil }},
	}}}, t.TempDir())
	m := &InteractiveMode{newRunner: runner, opts: InteractiveModeOptions{AgentDir: agentDir}}
	if !m.interruptShutdown() {
		t.Fatal("a running session ignored SIGINT")
	}
	if m.requestExit.Load() {
		t.Fatal("SIGINT asked the owner loop to return from Run; main would exit 0 before the handler's os.Exit(130)")
	}
	if disposals != 1 {
		t.Fatalf("session_shutdown emitted %d times, want 1", disposals)
	}
	if data, _ := os.ReadFile(ExitLogPath(agentDir)); !strings.Contains(string(data), "received SIGINT (exit 130)") {
		t.Fatalf("exit.log = %q", data)
	}
	if !m.signalShutdownDone.Load() {
		t.Fatal("the shutdown did not retain its entered state")
	}
}

func TestInterruptShutdownIsIgnoredWhileSuspended(t *testing.T) {
	agentDir := t.TempDir()
	m := &InteractiveMode{opts: InteractiveModeOptions{AgentDir: agentDir}}
	m.suspended.Store(true)
	if m.interruptShutdown() {
		t.Fatal("a suspended session took SIGINT")
	}
	if data, _ := os.ReadFile(ExitLogPath(agentDir)); len(data) != 0 {
		t.Fatalf("exit.log = %q, want none", data)
	}
}
