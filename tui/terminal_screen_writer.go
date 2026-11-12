// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-License-Identifier: MIT

package tui

import (
	"io"
	"os"
	"sync"
	"sync/atomic"

	"github.com/MichaelKinsy/PiG/internal/screenwriter"
)

// The screen writer (D624, internal/screenwriter) sits under ProcessTerminal.write: the renderers, the terminal's own control writes
// and PI_TUI_WRITE_LOG are above it and see the bytes they always wrote. PIG_TUI_SCREEN_WRITER=1 turns it on; unset, or any other
// value, the terminal is sent the renderers' bytes as they are. PIG_TUI_SCREEN_WRITER_LOG names a file that receives, with the writer
// on, every write as it came and as it was sent on (screenwriter.NextRecord reads it): what the parity suite compares screen by screen.
const (
	screenWriterEnv    = "PIG_TUI_SCREEN_WRITER"
	screenWriterLogEnv = "PIG_TUI_SCREEN_WRITER_LOG"
)

// screenWriters are the screen writers of the terminals that have one, each with the file its terminal writes to, for the writes
// that go to that file past the terminal.
var screenWriters atomic.Pointer[[]screenWriterOutput]

type screenWriterOutput struct {
	file   *os.File
	writer *screenwriter.Writer
}

// newTerminalScreenWriter is the screen writer under t's write, nil unless the environment asks for one.
func newTerminalScreenWriter(t *ProcessTerminal, getenv func(string) string) *screenwriter.Writer {
	// pig additive (D624): with PIG_TUI_SCREEN_WRITER=1 the terminal is sent only what changes the screen; unset it is sent Pi's bytes.
	if getenv(screenWriterEnv) != "1" {
		return nil
	}
	options := screenwriter.Options{
		Size:   func() (int, int) { return t.Columns(), t.Rows() },
		Lock:   t.outMu.Lock,
		Unlock: t.outMu.Unlock,
		// The settle write is a write of the terminal's like any other: none after the exit restore.
		Emit: func(data string) {
			if t.out != nil && !t.exitRestored.Load() {
				writeTerminalText(t.out, data)
			}
		},
	}
	if path := getenv(screenWriterLogEnv); path != "" {
		if file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600); err == nil {
			log := &screenWriterLog{file: file}
			options.Record = log.append
		}
	}
	writer := screenwriter.New(options)
	if file, ok := t.out.(*os.File); ok {
		for {
			old := screenWriters.Load()
			var list []screenWriterOutput
			if old != nil {
				list = append(list, *old...)
			}
			list = append(list, screenWriterOutput{file, writer})
			if screenWriters.CompareAndSwap(old, &list) {
				break
			}
		}
	}
	return writer
}

// screenWriterLog appends the screen writer's records to a file, one write a record.
type screenWriterLog struct {
	mu   sync.Mutex
	file *os.File
	buf  []byte
}

func (l *screenWriterLog) append(kind byte, in, out string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.buf = screenwriter.AppendRecord(l.buf[:0], kind, in, out)
	_, _ = l.file.Write(l.buf)
}

// ScreenWriterActive reports whether the terminal's frames pass through the screen writer (D624) now: the terminal has one and the
// alternate screen is shown. It is one atomic load and takes no lock, so a render loop may ask before every frame.
func (t *ProcessTerminal) ScreenWriterActive() bool {
	return t.screen != nil && t.screen.Active()
}

// screenWriterWrittenPast tells the screen writer of every terminal that writes to out about data, which went to out past it.
func screenWriterWrittenPast(out io.Writer, data string) {
	writers := screenWriters.Load()
	if writers == nil {
		return
	}
	if file, ok := out.(*os.File); ok {
		for _, entry := range *writers {
			if entry.file == file {
				entry.writer.Outside(data)
			}
		}
	}
}
