//go:build linux

package main

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// Pi's AgentSession emits model_select once per changed selection: setModel with source "set" (agent-session.ts:2411) and cycleModel with source "cycle" (agent-session.ts:2480, 2512), each with the full model and previousModel (agent-session.ts:2372-2384). Interactive /model and the cycle key only call them. Pig's interactive mode emitted a second event of its own for each, with a model that had no provider (`undefined/undefined` to a Node extension), and the cycle's Session event said source "set". The lane review found the /model duplicate with this fixture; the cycle followed the same code.
func TestInteractiveModelSelectionEmitsOneModelSelectEach(t *testing.T) {
	if testing.Short() {
		t.Skip("starts the pig binary and an extension process")
	}
	t.Parallel()
	binary := buildPigBinaryForSignalTest(t)
	home, cwd := shortTempDir(t), t.TempDir()
	report := filepath.Join(t.TempDir(), "report.jsonl")
	fixture, err := filepath.Abs(filepath.Join("testdata", "model-select-probe.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	args := []string{"--no-extensions", "--no-skills", "--no-prompt-templates", "--no-approve", "--session-dir", filepath.Join(home, "sessions"), "-e", fixture}
	master, slave := openPTY(t, 40, 140)
	t.Cleanup(func() { _ = master.Close() })
	cmd := exec.Command(binary, args...)
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(), "HOME="+home, "PIG_HOME="+home, "PIG_CODING_AGENT_DIR="+filepath.Join(home, "agent"), "WIRING_REPORT="+report, "TERM=xterm-256color")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start pig: %v", err)
	}
	_ = slave.Close()
	exited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(exited) }()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-exited
	})
	output := &ptyOutput{}
	go func() { _, _ = io.Copy(output, master) }()
	write := func(text string) {
		t.Helper()
		if _, err := master.Write([]byte(text)); err != nil {
			t.Fatal(err)
		}
	}
	output.waitQuiet(0, []byte("probe-model"), 500*time.Millisecond, testbudget.Wait(t))
	if !bytes.Contains(output.since(0), []byte("probe-model")) {
		t.Fatalf("the footer never showed the starting model; screen tail: %q", tail(output.since(0), 1500))
	}

	write("/model probe/probe-model-2")
	time.Sleep(300 * time.Millisecond)
	// The first Enter accepts the argument completion the editor shows; the second submits. An Enter on an empty editor does nothing.
	write("\r")
	time.Sleep(300 * time.Millisecond)
	write("\r")
	waitReportLines(t, report, 1, output)
	time.Sleep(300 * time.Millisecond)
	write("\x10")
	waitReportLines(t, report, 2, output)
	// A duplicate event follows its first by one extension round trip.
	time.Sleep(700 * time.Millisecond)

	write("/quit")
	time.Sleep(200 * time.Millisecond)
	write("\r")
	select {
	case <-exited:
	case <-time.After(testbudget.Wait(t)):
		t.Fatalf("/quit did not exit; screen tail: %q", tail(output.since(0), 1500))
	}
	data, err := os.ReadFile(report)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Split(strings.TrimSpace(string(data)), "\n")
	want := []string{
		`{"event":"model_select","model":"probe/probe-model-2","previousModel":"probe/probe-model","source":"set"}`,
		`{"event":"model_select","model":"probe/probe-model","previousModel":"probe/probe-model-2","source":"cycle"}`,
	}
	if !slices.Equal(got, want) {
		t.Fatalf("model_select records:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
