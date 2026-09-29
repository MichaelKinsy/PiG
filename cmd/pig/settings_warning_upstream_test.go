//go:build linux

package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/7829-invalid-settings-warning.test.ts:15
// The binary caller must render the warning on the terminal transcript, not stderr.
func TestInteractiveStartupSettingsWarningReachesTranscript(t *testing.T) {
	binary := buildPigBinaryForSignalTest(t)
	agentDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte("{ invalid json"), 0o600); err != nil {
		t.Fatal(err)
	}
	master, slave := openPTY(t, 40, 160)
	defer func() { _ = slave.Close(); _ = master.Close() }()
	ctx, cancel := context.WithTimeout(t.Context(), testbudget.Wait(t))
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "--offline", "--no-extensions", "--model", "test-faux/faux-1")
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(), "PIG_HOME="+t.TempDir(), "PIG_CODING_AGENT_DIR="+agentDir, "PIG_TEST_FAUX=1", "PIG_TEST_FAUX_SCENARIO=parity-basic", "TERM=xterm-256color")
	cmd.Stdin, cmd.Stdout = slave, slave
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output := &ptyOutput{}
	readDone := make(chan struct{})
	go func() { _, _ = io.Copy(output, master); close(readDone) }()
	if err := cmd.Start(); err != nil {
		_ = slave.Close()
		_ = master.Close()
		<-readDone
		t.Fatal(err)
	}
	_ = slave.Close()
	waitDone := make(chan error, 1)
	go func() { waitDone <- cmd.Wait() }()
	waited := false
	defer func() {
		cancel()
		if !waited {
			<-waitDone
		}
		_ = master.Close()
		<-readDone
	}()
	needle := []byte("Warning: Invalid settings file")
	output.waitQuiet(0, needle, 50*time.Millisecond, testbudget.Wait(t))
	if !bytes.Contains(output.since(0), needle) {
		t.Fatalf("warning not rendered on terminal: %q", output.since(0))
	}
	cancel()
	<-waitDone
	waited = true
	if strings.Contains(stderr.String(), "Warning: Invalid settings file") {
		t.Fatalf("warning escaped the transcript to stderr: %q", stderr.String())
	}
}
