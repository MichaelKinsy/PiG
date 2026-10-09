//go:build linux

package tui

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

const backgroundReadResultEnv = "PIG_TUI_BACKGROUND_READ_RESULT"

// A read from a terminal that is alive but whose foreground belongs to another process group fails with EIO when SIGTTIN is ignored, as for an orphaned background process group. Only a hung-up terminal's EIO is the end of input; this EIO must stay an error, so the interactive loop treats the terminal as gone (interactive-mode.ts terminalErrorHandler -> isDeadTerminalError -> emergencyTerminalExit).
func TestTerminalReaderKeepsEIOFromABackgroundReadAnError(t *testing.T) {
	if result := os.Getenv(backgroundReadResultEnv); result != "" {
		readInTheBackground(result)
		return
	}
	master, slave := openHangupPTY(t)
	defer func() { _ = master.Close() }()
	result := filepath.Join(t.TempDir(), "read")
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	helper := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestTerminalReaderKeepsEIOFromABackgroundReadAnError$", "-test.count=1")
	helper.Env = append(os.Environ(), backgroundReadResultEnv+"="+result)
	helper.Stdin = slave
	// The helper leads a new session whose controlling terminal is the slave.
	helper.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := helper.Start(); err != nil {
		t.Fatal(err)
	}
	_ = slave.Close()
	waited := make(chan error, 1)
	go func() { waited <- helper.Wait() }()
	ready := result + ".ready"
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		select {
		case err := <-waited:
			got, _ := os.ReadFile(result)
			t.Fatalf("helper exited before reading: %v %s", err, got)
		case <-ctx.Done():
			t.Fatal("helper never became ready")
		case <-time.After(5 * time.Millisecond):
		}
	}
	// A complete line makes the cooked terminal readable, so the reader leaves its poll and reads in the background.
	if _, err := master.Write([]byte("x\n")); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-waited:
		if err != nil {
			got, _ := os.ReadFile(result)
			t.Fatalf("helper: %v %s", err, got)
		}
	case <-ctx.Done():
		t.Fatal("background read did not return")
	}
	got, err := os.ReadFile(result)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "EIO" {
		t.Fatalf("background read reported %q, want the EIO error", got)
	}
}

// readInTheBackground moves the session's foreground to another process group, ignores SIGTTIN, and reads stdin through a terminal reader. It writes "EIO" to result when the read fails with EIO, else what the read returned.
func readInTheBackground(result string) {
	write := func(s string) { _ = os.WriteFile(result, []byte(s), 0o600) }
	// The foreground child's tcsetpgrp and this process's background read would otherwise stop the group.
	signal.Ignore(syscall.SIGTTOU, syscall.SIGTTIN)
	foreground := exec.Command("sleep", "30")
	foreground.Stdin = os.Stdin
	foreground.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Foreground: true, Ctty: 0}
	if err := foreground.Start(); err != nil {
		write("start foreground: " + err.Error())
		os.Exit(1)
	}
	defer func() { _ = foreground.Process.Kill(); _ = foreground.Wait() }()
	reader, err := newTerminalReader(context.Background(), os.Stdin)
	if err != nil {
		write("reader: " + err.Error())
		return
	}
	defer reader.close()
	_ = os.WriteFile(result+".ready", nil, 0o600)
	data, err := reader.read(-1)
	switch {
	case errors.Is(err, syscall.EIO):
		write("EIO")
	case err != nil:
		write("error: " + err.Error())
	default:
		write("data: " + string(data))
	}
}
