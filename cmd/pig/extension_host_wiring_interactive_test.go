//go:build linux

package main

import (
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

// waitReportLines polls report until it holds at least n lines and returns them.
func waitReportLines(t *testing.T, report string, n int, output *ptyOutput) []string {
	t.Helper()
	deadline := time.Now().Add(testbudget.Wait(t))
	for {
		if data, err := os.ReadFile(report); err == nil {
			if lines := strings.Split(strings.TrimSpace(string(data)), "\n"); strings.TrimSpace(string(data)) != "" && len(lines) >= n {
				return lines
			}
		}
		if time.Now().After(deadline) {
			data, _ := os.ReadFile(report)
			t.Fatalf("interactive mode wrote %q, want %d lines; screen tail: %q", data, n, tail(output.since(0), 1500))
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// Interactive mode binds the same Session actions as the headless modes (agent-session.ts:3275-3408 _bindExtensionCore through interactive-mode.ts's session.bindExtensions), and builds its Session through the same factory, so the fixtures record what they record in print, JSON and RPC mode, and the Python extension's virtual model reaches the Session's model runtime before session_start.
func TestInteractiveExtensionHostWiringMatchesHeadlessModes(t *testing.T) {
	if testing.Short() {
		t.Skip("starts the pig binary and extension processes")
	}
	t.Parallel()
	binary := buildPigBinaryForSignalTest(t)
	home, cwd := shortTempDir(t), t.TempDir()
	report := filepath.Join(t.TempDir(), "report.jsonl")
	args := []string{"--no-extensions", "--no-skills", "--no-prompt-templates", "--no-approve", "--session-dir", filepath.Join(home, "sessions")}
	for _, fixture := range []string{"host-wiring-session-actions.mjs", "host-wiring-virtual-model.py"} {
		path, err := filepath.Abs(filepath.Join("testdata", fixture))
		if err != nil {
			t.Fatal(err)
		}
		args = append(args, "-e", path)
	}
	seedFirstRunDone(t, filepath.Join(home, "agent"))
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
	// Six records from the Node extension's session_start and one from the Python extension's.
	waitReportLines(t, report, 7, output)
	time.Sleep(300 * time.Millisecond)
	if _, err := master.Write([]byte("/quit")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	if _, err := master.Write([]byte("\r")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-exited:
	case <-time.After(testbudget.Wait(t)):
		t.Fatalf("/quit did not exit; screen tail: %q", tail(output.since(0), 1500))
	}
	got := waitReportLines(t, report, 8, output)
	var nodeRecords, virtualModel []string
	for _, line := range got {
		if strings.Contains(line, `"virtual_model"`) {
			virtualModel = append(virtualModel, line)
		} else {
			nodeRecords = append(nodeRecords, line)
		}
	}
	wantNode := []string{
		`{"event":"trusted","value":false}`,
		`{"event":"setModelNoAuth","value":false}`,
		`{"event":"model_select","model":"probe/probe-model-2"}`,
		`{"event":"setModel","value":true}`,
		`{"event":"compact","error":"Nothing to compact (session too small)"}`,
		`{"event":"thinking_level_select","level":"high"}`,
		`{"event":"entries","value":["model_change:probe/probe-model","thinking_level_change:medium","model_change:probe/probe-model-2","thinking_level_change:high"]}`,
	}
	if !slices.Equal(nodeRecords, wantNode) {
		t.Errorf("session action records:\n%s\nwant:\n%s", strings.Join(nodeRecords, "\n"), strings.Join(wantNode, "\n"))
	}
	if want := []string{`{"event":"virtual_model","value":"auto"}`}; !slices.Equal(virtualModel, want) {
		t.Errorf("virtual model records %v, want %v", virtualModel, want)
	}
}
