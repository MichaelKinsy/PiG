//go:build unix

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Upstream print mode awaits session.prompt, which resolves only after every
// agent event's extension handlers ran, and only then disposes the runtime
// (session_shutdown). Pig's print mode returned as soon as Send did, while the
// session was still dispatching the turn's tail to extensions, so message_end,
// turn_end, and agent_end reached a closed extension connection after
// session_shutdown ("connection closed").
func TestPrintModeDeliversTurnEventsBeforeSessionShutdown(t *testing.T) {
	bin := buildPigBinaryForSignalTest(t)
	fixture, err := filepath.Abs(filepath.Join("testdata", "event-order.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	eventLog := filepath.Join(t.TempDir(), "events.log")
	cmd := exec.Command(bin, "--model", "test-faux/faux-1", "--no-session", "-e", fixture, "--print", "What is 20+22?")
	cmd.Dir = t.TempDir()
	cmd.Stdin = nil
	cmd.Env = append(os.Environ(), "PIG_HOME="+t.TempDir(), "PIG_TEST_FAUX=1", "PIG_TEST_FAUX_SCENARIO=parity-basic", "PIG_TEST_EVENT_LOG="+eventLog)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("pig --print: %v\n%s", err, out)
	}
	if strings.Contains(string(out), "Extension error") {
		t.Fatalf("extension handlers failed:\n%s", out)
	}
	data, err := os.ReadFile(eventLog)
	if err != nil {
		t.Fatalf("no events recorded: %v\n%s", err, out)
	}
	got := strings.Fields(string(data))
	shutdown := slices.Index(got, "session_shutdown")
	end := slices.Index(got, "agent_end")
	if shutdown < 0 || end < 0 || end > shutdown || shutdown != len(got)-1 || !slices.Contains(got[:shutdown], "turn_end") {
		t.Fatalf("events = %v, want every turn event (through agent_end) before session_shutdown", got)
	}
}
