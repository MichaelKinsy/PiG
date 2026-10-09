package tui

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// PI_TUI_WRITE_LOG (terminal.ts writeLogPath and write): a file path receives every byte the terminal writes, appended; an
// existing directory gets a unique tui-<local date>_<time>-<pid>.log per process; any other value is used as a file path
// whether or not it exists yet; empty or unset logs nothing.
func TestTerminalWriteLogPath(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "plain.log")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 3, 4, 5, 6, 7, 0, time.Local)
	for _, tc := range []struct{ name, value, want string }{
		{"unset", "", ""},
		{"existing file", file, file},
		{"missing path is used as given", filepath.Join(dir, "missing", "x.log"), filepath.Join(dir, "missing", "x.log")},
		{"directory", dir, filepath.Join(dir, "tui-2026-03-04_05-06-07-4242.log")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := terminalWriteLogPath(tc.value, now, 4242); got != tc.want {
				t.Fatalf("terminalWriteLogPath(%q) = %q, want %q", tc.value, got, tc.want)
			}
		})
	}
}

// Every Write of a ProcessTerminal reaches both the terminal and the log, in order, appended to what the file already holds.
func TestProcessTerminalWritesAreLoggedWhenTheEnvironmentAsksForIt(t *testing.T) {
	log := filepath.Join(t.TempDir(), "tui.log")
	if err := os.WriteFile(log, []byte("earlier\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PI_TUI_WRITE_LOG", log)
	var out bytes.Buffer
	terminal := NewProcessTerminalWithOutput(nil, nil, &out)
	terminal.Write("one ")
	terminal.Write("two \x1b[2K ✓")
	got, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	want := "earlier\none two \x1b[2K ✓"
	if string(got) != want || out.String() != want[len("earlier\n"):] {
		t.Fatalf("log = %q, terminal = %q, want %q", got, out.String(), want)
	}
}

// Upstream's cursor, clear, title, progress, move, start and stop methods write to process.stdout directly, so only Write reaches
// the log.
func TestProcessTerminalControlMethodsAreNotLogged(t *testing.T) {
	log := filepath.Join(t.TempDir(), "tui.log")
	t.Setenv("PI_TUI_WRITE_LOG", log)
	var out bytes.Buffer
	terminal := NewProcessTerminalWithOutput(nil, nil, &out)
	terminal.HideCursor()
	terminal.ShowCursor()
	terminal.MoveBy(2)
	terminal.MoveBy(-1)
	terminal.ClearLine()
	terminal.ClearFromCursor()
	terminal.ClearScreen()
	terminal.SetTitle("title")
	terminal.SetProgress(true)
	terminal.SetProgress(false)
	terminal.Write("logged")
	if got, _ := os.ReadFile(log); string(got) != "logged" {
		t.Fatalf("log = %q, want only the Write", got)
	}
	if out.String() != "\x1b[?25l\x1b[?25h\x1b[2B\x1b[1A\x1b[K\x1b[J\x1b[2J\x1b[H\x1b]0;title\x07"+terminalProgressActiveSeq+terminalProgressClearSeq+"logged" {
		t.Fatalf("terminal = %q", out.String())
	}
}

// A directory value creates the per-process file on the first write and keeps appending to it.
func TestProcessTerminalWriteLogInADirectoryUsesOneUniqueFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PI_TUI_WRITE_LOG", dir)
	terminal := NewProcessTerminalWithOutput(nil, nil, io.Discard)
	other := NewProcessTerminalWithOutput(nil, nil, io.Discard)
	terminal.Write("a")
	other.Write("b")
	terminal.Write("c")
	matches, err := filepath.Glob(filepath.Join(dir, "tui-*.log"))
	if err != nil || len(matches) != 1 {
		t.Fatalf("log files = %v, %v; want one", matches, err)
	}
	if ok, _ := regexp.MatchString(fmt.Sprintf(`tui-\d{4}-\d{2}-\d{2}_\d{2}-\d{2}-\d{2}-%d\.log$`, os.Getpid()), matches[0]); !ok {
		t.Fatalf("log name = %s", matches[0])
	}
	if got, _ := os.ReadFile(matches[0]); string(got) != "abc" {
		t.Fatalf("log = %q, want abc", got)
	}
}

// A log that cannot be written never disturbs the terminal (upstream: "Ignore logging errors").
func TestTerminalWriteLogErrorsAreIgnored(t *testing.T) {
	t.Setenv("PI_TUI_WRITE_LOG", filepath.Join(t.TempDir(), "missing-dir", "x.log"))
	var out bytes.Buffer
	NewProcessTerminalWithOutput(nil, nil, &out).Write("still written")
	if out.String() != "still written" {
		t.Fatalf("terminal = %q", out.String())
	}
}

// Without the variable nothing is wrapped; with it the renderers' frame output is the logging writer around stdout, and cursor
// visibility, which upstream writes to stdout directly, goes around it.
func TestRendererOutputLogsOnlyWhenAsked(t *testing.T) {
	t.Setenv("PI_TUI_WRITE_LOG", "")
	var unlogged bytes.Buffer
	main := NewTuiMainScreen(NewProcessTerminalWithOutput(nil, nil, &unlogged), nil, "")
	main.WriteRaw("frame")
	if unlogged.String() != "frame" {
		t.Fatalf("main-screen output = %q", unlogged.String())
	}
	var altUnlogged bytes.Buffer
	alt := NewTuiAltScreen(NewProcessTerminalWithOutput(nil, nil, &altUnlogged), nil, "")
	alt.WriteRaw("frame")
	if altUnlogged.String() != "frame" {
		t.Fatalf("fullscreen output = %q", altUnlogged.String())
	}
	log := filepath.Join(t.TempDir(), "tui.log")
	t.Setenv("PI_TUI_WRITE_LOG", log)
	var logged bytes.Buffer
	main = NewTuiMainScreen(NewProcessTerminalWithOutput(nil, nil, &logged), nil, "")
	main.WriteRaw("frame")
	if got, _ := os.ReadFile(log); string(got) != "frame" || logged.String() != "frame" {
		t.Fatalf("main-screen log = %q, output = %q", got, logged.String())
	}
	_ = os.Remove(log)
	var altLogged bytes.Buffer
	alt = NewTuiAltScreen(NewProcessTerminalWithOutput(nil, nil, &altLogged), nil, "")
	alt.WriteRaw("frame")
	if got, _ := os.ReadFile(log); string(got) != "frame" || altLogged.String() != "frame" {
		t.Fatalf("fullscreen log = %q, output = %q", got, altLogged.String())
	}
}

// Hiding and showing the cursor bypass the log; a frame write reaches it.
func TestRendererCursorVisibilityIsNotLogged(t *testing.T) {
	log := filepath.Join(t.TempDir(), "tui.log")
	t.Setenv("PI_TUI_WRITE_LOG", log)
	var terminal bytes.Buffer
	ui := NewTuiMainScreen(NewProcessTerminalWithOutput(nil, nil, &terminal), nil, "")
	ui.HideCursor()
	ui.ShowCursor()
	ui.WriteRaw("frame")
	if got, _ := os.ReadFile(log); string(got) != "frame" {
		t.Fatalf("log = %q, want only the frame write", got)
	}
	if terminal.String() != "\x1b[?25l\x1b[?25hframe" {
		t.Fatalf("terminal = %q", terminal.String())
	}
}

// A main-screen frame that places the hardware cursor writes the move through terminal.write and the visibility through
// terminal.showCursor or terminal.hideCursor (tui-main-screen.ts positionHardwareCursor), so the log holds the move but never the
// cursor visibility, whether the cursor is shown, hidden or absent.
func TestMainScreenHardwareCursorVisibilityIsNotLogged(t *testing.T) {
	for _, tc := range []struct {
		name  string
		show  bool
		lines []string
	}{
		{"shown", true, []string{"top", "prompt " + widthx.CursorMarker + "x"}},
		{"hidden", false, []string{"top", "prompt " + widthx.CursorMarker + "x"}},
		{"no cursor", true, []string{"top", "prompt x"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			log := &terminalWriteLog{path: filepath.Join(t.TempDir(), "tui.log")}
			var terminal bytes.Buffer
			ui := NewWithOutput(io.Discard, 80, 10)
			ui.out, ui.direct = &loggingWriter{out: &terminal, log: log}, &terminal
			ui.showHardwareCursor = tc.show
			ui.Add(&fixedLinesComponent{lines: tc.lines})
			ui.doRender()
			got, err := os.ReadFile(log.path)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(got, []byte("\x1b[?25")) {
				t.Fatalf("log records cursor visibility: %q", got)
			}
			if !bytes.Contains(terminal.Bytes(), []byte("\x1b[?25")) {
				t.Fatalf("terminal lacks cursor visibility: %q", terminal.String())
			}
			if strings.Contains(tc.lines[1], widthx.CursorMarker) && !bytes.Contains(got, []byte("\x1b[8G")) {
				t.Fatalf("log lacks the cursor move: %q", got)
			}
		})
	}
}
