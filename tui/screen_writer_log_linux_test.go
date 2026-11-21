// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-License-Identifier: MIT

//go:build linux

package tui

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// PIG_TUI_SCREEN_WRITER_LOG opens, appends, and closes the log file on each record (as PI_TUI_WRITE_LOG does), so no file descriptor remains open after writes complete.
func TestScreenWriterLogClosesFileDescriptorAfterAppend(t *testing.T) {
	path := filepath.Join(t.TempDir(), "records.bin")
	t.Setenv(screenWriterEnv, "1")
	t.Setenv(screenWriterLogEnv, path)
	setScreenWriterChoice(t, screenWriterOn)
	var out bytes.Buffer
	terminal := NewProcessTerminalWithOutput(nil, nil, &out)
	for _, data := range append(swStarted(), swFrame(swLine(1, "one")), swFrame(swLine(1, "two")), swLeave) {
		terminal.Write(data)
	}
	if writer := terminal.screen.Load(); writer != nil {
		writer.Stop()
	}
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatalf("read /proc/self/fd: %v", err)
	}
	for _, entry := range entries {
		target, err := os.Readlink(filepath.Join("/proc/self/fd", entry.Name()))
		if err == nil && target == path {
			t.Fatalf("process still holds open file descriptor %s -> %s after screen writer writes", entry.Name(), target)
		}
	}
}
