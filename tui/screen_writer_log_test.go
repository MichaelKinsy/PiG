// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-License-Identifier: MIT

package tui

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/screenwriter"
)

const (
	swBegin = "\x1b[?2026h"
	swEnd   = "\x1b[?2026l"
	swReset = "\x1b[0m\x1b]8;;\x07"
	swEnter = "\x1b[?1049h\x1b[?7l" // as the fullscreen renderer enters: autowrap off, without which the writer translates nothing
	swLeave = "\x1b[?1049l"
)

func swAt(row, col int) string { return fmt.Sprintf("\x1b[%d;%dH", row, col) }

func swLine(row int, text string) string { return swAt(row, 1) + "\x1b[2K" + text + swReset }

// swFrame is a synchronized update as the renderers write one: the cursor ends at the top left, hidden.
func swFrame(parts ...string) string {
	return swBegin + strings.Join(parts, "") + swAt(1, 1) + "\x1b[?25l" + swEnd
}

// swStarted is what a session writes first: the alternate screen and two repaints.
func swStarted() []string {
	return []string{swEnter, swFrame("\x1b[2J", swLine(1, "")), swFrame("\x1b[2J", swLine(1, ""))}
}

// PIG_TUI_SCREEN_WRITER_LOG records every write of a terminal with the writer on as it came and as it went on.
func TestScreenWriterLogRecordsEveryWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "writes.bin")
	t.Setenv(screenWriterEnv, "1")
	t.Setenv(screenWriterLogEnv, path)
	setScreenWriterChoice(t, screenWriterOn)
	var out bytes.Buffer
	terminal := NewProcessTerminalWithOutput(nil, nil, &out)
	inputs := append(swStarted(), swFrame(swLine(1, "one")), swFrame(swLine(1, "one")), swFrame(swLine(1, "one!")), "\x1b[?25h")
	for _, data := range inputs {
		terminal.Write(data)
	}
	if writer := terminal.screen.Load(); writer != nil {
		writer.Stop()
	}
	log, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var in, sent strings.Builder
	translated := 0
	for n := 0; ; n++ {
		kind, i, o, rest, ok := screenwriter.NextRecord(log)
		if !ok {
			if n < len(inputs) || len(log) != 0 {
				t.Errorf("%d records and %d bytes left, want a record for each of the %d writes", n, len(log), len(inputs))
			}
			break
		}
		if kind != screenwriter.RecordFrame && kind != screenwriter.RecordOther && kind != screenwriter.RecordSettle {
			t.Errorf("record %d has kind %q", n, kind)
		}
		in.Write(i)
		sent.Write(o)
		if kind == screenwriter.RecordFrame && !bytes.Equal(o, i) {
			translated++
		}
		log = rest
	}
	if in.String() != strings.Join(inputs, "") {
		t.Errorf("the log's inputs are not what was written")
	}
	if sent.String() != out.String() {
		t.Errorf("the log's outputs are %q, the terminal was sent %q", sent.String(), out.String())
	}
	if translated == 0 {
		t.Errorf("no frame in the log was translated: the log would be the same without the writer")
	}
}
