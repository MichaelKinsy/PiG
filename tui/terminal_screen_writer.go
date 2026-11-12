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
// are: byte mode, Pi's bytes. PIG_TUI_SCREEN_WRITER decides when it is set: `pi` or `0` is byte mode, any other value is the writer.
// Unset or empty, the terminal.screenWriter setting decides once interactive mode has applied it (SetScreenWriter); until then a
// terminal is built with the writer, the default, because a writer can be dropped from a terminal that exists and cannot be added
// to one. PIG_TUI_SCREEN_WRITER_LOG names a file that receives, with the writer on, every write as it came
// and as it was sent on (screenwriter.NextRecord reads it): what the parity suite compares screen by screen.
const (
	screenWriterEnv    = "PIG_TUI_SCREEN_WRITER"
	screenWriterLogEnv = "PIG_TUI_SCREEN_WRITER_LOG"
)

// What the process has been told about the screen writer. The environment outranks the setting; nothing outranks screenWriterNone.
const (
	screenWriterUnset int32 = iota // nothing applied yet: the writer, the setting's default
	screenWriterOn                 // terminal.screenWriter is "writer"
	screenWriterOff                // terminal.screenWriter is "pi"
	screenWriterNone               // the process shows no screen (NoScreenWriter)
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
	if choice == screenWriterNone {
		return false
	}
	// pig divergence (D624): fullscreen frames go through the screen writer, which sends only what changes the screen; Pi writes every row a frame holds. PIG_TUI_SCREEN_WRITER=pi and terminal.screenWriter "pi" select Pi's bytes.
	switch getenv(screenWriterEnv) {
	case "pi", "0":
		return false
	case "":
		return choice != screenWriterOff
	}
	return true
}

// SetScreenWriter applies the terminal.screenWriter setting to the process: writer is whether it selects the screen writer. A
// terminal built afterwards follows it. With writer false the terminals built so far that write to a file, the process's terminal
// among them, lose their screen writer for good: they are sent the renderers' bytes from their next write on. PIG_TUI_SCREEN_WRITER,
// when set, decides alone and the call changes nothing. The setting is read once, at the start of interactive mode, before the
// alternate screen is entered, so no screen sees both kinds of frame.
func SetScreenWriter(writer bool) {
	choice := screenWriterOff
	if writer {
		choice = screenWriterOn
	}
	chooseScreenWriter(choice)
}

// NoScreenWriter is for a process that shows no screen (print, JSON and RPC mode): no terminal has a screen writer, whatever the
// environment and the setting say.
func NoScreenWriter() {
	chooseScreenWriter(screenWriterNone)
}

func chooseScreenWriter(choice int32) {
	screenWriterMu.Lock()
	defer screenWriterMu.Unlock()
	screenWriterChoice = choice
	if screenWriterWanted(os.Getenv, choice) {
		return
	}
	if writers := screenWriters.Swap(nil); writers != nil {
		for _, entry := range *writers {
			entry.writer.Drop()
		}
	}
}

// newTerminalScreenWriter is the screen writer under t's write, nil when the environment or the setting selects byte mode.
func newTerminalScreenWriter(t *ProcessTerminal, getenv func(string) string) *screenwriter.Writer {
	screenWriterMu.Lock()
	defer screenWriterMu.Unlock()
	if !screenWriterWanted(getenv, screenWriterChoice) {
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

// screenWriterLog appends the screen writer's records to a file, one write a record. The file is opened by the first record, so a
// writer that reads no write leaves no file.
type screenWriterLog struct {
	mu     sync.Mutex
	path   string
	file   *os.File
	failed bool
	buf    []byte
}

func (l *screenWriterLog) append(kind byte, in, out string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil && !l.failed {
		file, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		l.file, l.failed = file, err != nil
	}
	if l.file == nil {
		return
	}
	l.buf = screenwriter.AppendRecord(l.buf[:0], kind, in, out)
	_, _ = l.file.Write(l.buf)
}

// ScreenWriterActive reports whether the terminal's frames pass through the screen writer (D624) now: the terminal has one, it was
// not dropped, and the alternate screen is shown. It takes no lock, so a render loop may ask before every frame: t.screen is set
// once, by the constructor, before the terminal is shared, and dropping the writer changes the writer, not the field; what is read
// of the writer is one atomic load.
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
