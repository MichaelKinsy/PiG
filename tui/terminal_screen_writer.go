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
// and PI_TUI_WRITE_LOG are above it and see the bytes they always wrote. Without it the terminal is sent the renderers' bytes as they
// are: byte mode, Pi's bytes. No screen writer is built before interactive mode starts and applies the terminal.screenWriter setting
// (SetScreenWriter): the process's terminal, which is built when the package is initialized, gets its writer then, and a terminal built
// afterwards gets one from its constructor. Print, JSON and RPC mode and `--version` never apply the setting, so they build none,
// whatever the environment says. From then on PIG_TUI_SCREEN_WRITER decides when it is set: `pi` or `0` is byte mode, any other value
// is the writer; unset or empty, the setting decides. PIG_TUI_SCREEN_WRITER_LOG names a file that receives, with the writer on, every
// write as it came and as it was sent on (screenwriter.NextRecord reads it): what the parity suite compares screen by screen.
const (
	screenWriterEnv    = "PIG_TUI_SCREEN_WRITER"
	screenWriterLogEnv = "PIG_TUI_SCREEN_WRITER_LOG"
)

// What the process has been told about the screen writer. Once the setting is applied the environment outranks it.
const (
	screenWriterUnset int32 = iota // no interactive run has applied the setting: no terminal has a screen writer or gets one
	screenWriterOn                 // terminal.screenWriter is "writer"
	screenWriterOff                // terminal.screenWriter is "pi"
)

// screenWriterChoice is what the process has been told, and screenWriters are the screen writers of the terminals that write to a
// file, each with that file, for the writes that go to the file past the terminal. screenWriterMu orders a choice with the
// terminals built meanwhile; the list is read without it.
var (
	screenWriterMu     sync.Mutex
	screenWriterChoice int32
	screenWriters      atomic.Pointer[[]screenWriterOutput]
)

type screenWriterOutput struct {
	file   *os.File
	writer *screenwriter.Writer
}

// screenWriterWanted reports whether a terminal gets a screen writer under choice in the environment getenv reads.
func screenWriterWanted(getenv func(string) string, choice int32) bool {
	if choice == screenWriterUnset {
		return false
	}
	// pig divergence (D624): when terminal.screenWriter or PIG_TUI_SCREEN_WRITER selects it, fullscreen frames go through the screen writer, which sends only what changes the screen; Pi writes every row a frame holds.
	switch getenv(screenWriterEnv) {
	case "pi", "0":
		return false
	case "":
		return choice != screenWriterOff
	}
	return true
}

// ScreenWriterSelected reports whether a run gets the screen writer in the environment getenv reads when its terminal.screenWriter
// setting selects the screen writer (writer) or Pi's bytes: the choice SetScreenWriter makes, PIG_TUI_SCREEN_WRITER before the
// setting, asked without making it. It builds nothing and changes nothing.
func ScreenWriterSelected(writer bool, getenv func(string) string) bool {
	if writer {
		return screenWriterWanted(getenv, screenWriterOn)
	}
	return screenWriterWanted(getenv, screenWriterOff)
}

// SetScreenWriter applies the terminal.screenWriter setting to the process: writer is whether it selects the screen writer.
// PIG_TUI_SCREEN_WRITER, when set, decides in the setting's place. When the two ask for the writer, the process's terminal gets one
// unless it has one, and a terminal built afterwards gets one from its constructor. When they ask for byte mode, no terminal built
// afterwards gets one, and the terminals that have one and write to a file lose it for good: they are sent the renderers' bytes from
// their next write on. The setting is read once, at the start of interactive mode, before the alternate screen is entered, so no
// screen sees both kinds of frame; a process that never calls SetScreenWriter builds no screen writer.
func SetScreenWriter(writer bool) {
	choice := screenWriterOff
	if writer {
		choice = screenWriterOn
	}
	screenWriterMu.Lock()
	defer screenWriterMu.Unlock()
	screenWriterChoice = choice
	if screenWriterWanted(os.Getenv, choice) {
		if processTerminal.screen.Load() == nil {
			processTerminal.screen.Store(buildTerminalScreenWriter(processTerminal, os.Getenv))
		}
		return
	}
	if writers := screenWriters.Swap(nil); writers != nil {
		for _, entry := range *writers {
			entry.writer.Drop()
		}
	}
}

// newTerminalScreenWriter is the screen writer for a terminal that is being built: nil before an interactive run has applied the
// setting and when the environment or the setting selects byte mode.
func newTerminalScreenWriter(t *ProcessTerminal, getenv func(string) string) *screenwriter.Writer {
	screenWriterMu.Lock()
	defer screenWriterMu.Unlock()
	if !screenWriterWanted(getenv, screenWriterChoice) {
		return nil
	}
	return buildTerminalScreenWriter(t, getenv)
}

// buildTerminalScreenWriter builds the screen writer for t's write and registers it for t's file. screenWriterMu is held.
func buildTerminalScreenWriter(t *ProcessTerminal, getenv func(string) string) *screenwriter.Writer {
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
		log := &screenWriterLog{path: path}
		options.Record = log.append
	}
	writer := screenwriter.New(options)
	if file, ok := t.out.(*os.File); ok {
		var list []screenWriterOutput
		if old := screenWriters.Load(); old != nil {
			list = append(list, *old...)
		}
		list = append(list, screenWriterOutput{file, writer})
		screenWriters.Store(&list)
	}
	return writer
}

// screenWriterLog appends the screen writer's records to a file, one write a record. Each append opens, writes, and closes the file
// (appendLogFile, as PI_TUI_WRITE_LOG does), so a writer that reads no write leaves no file and no open descriptor outlives a write.
type screenWriterLog struct {
	mu   sync.Mutex
	path string
	buf  []byte
}

func (l *screenWriterLog) append(kind byte, in, out string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.buf = screenwriter.AppendRecord(l.buf[:0], kind, in, out)
	appendLogFile(l.path, 0o600, l.buf)
}

// ScreenWriterActive reports whether the terminal's frames pass through the screen writer (D624) now: the terminal has one, it was
// not dropped, and the alternate screen is shown. It takes no lock, so a render loop may ask before every frame: t.screen is stored
// once, by the constructor or when the interactive run starts, and dropping the writer changes the writer, not the field; what is
// read is two atomic loads.
func (t *ProcessTerminal) ScreenWriterActive() bool {
	writer := t.screen.Load()
	return writer != nil && writer.Active()
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
