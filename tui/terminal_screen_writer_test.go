// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-License-Identifier: MIT

//go:build linux || darwin

package tui

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/MichaelKinsy/PiG/internal/screenwriter"
	"github.com/MichaelKinsy/PiG/internal/screenwriter/gridterm"
)

// The screen writer (D624) under a ProcessTerminal on a real pseudo-terminal: the same script of writes goes to a terminal with the
// writer off (the renderers' bytes, the reference) and to one with it on, and what the terminal's master end reads is shown on
// gridterm's screens of every width model. The two must show the same screen once the settle write is in; where the writer must send
// bytes as they came (leaving the alternate screen, images, writes past it) the bytes are compared.

const (
	swBegin = "\x1b[?2026h"
	swEnd   = "\x1b[?2026l"
	swReset = "\x1b[0m\x1b]8;;\x07"
	swEnter = "\x1b[?1049h"
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

// swScreen is a terminal over a pseudo-terminal, with what its master end read and where the terminal changed size.
type swScreen struct {
	t             *testing.T
	term          *ProcessTerminal
	master, slave *os.File
	cols, rows    int

	mu    sync.Mutex
	data  []byte
	sizes []swSize
}

type swSize struct{ at, cols, rows int }

// newSWScreen is a cols by rows terminal with the screen writer on or off, whose master end is read chunk bytes at a time with a pause
// after each read when slow is set.
func newSWScreen(t *testing.T, writer bool, cols, rows int, chunk int, pause time.Duration) *swScreen {
	t.Helper()
	t.Setenv(screenWriterEnv, map[bool]string{true: "1"}[writer])
	t.Setenv(screenWriterLogEnv, "")
	master, slave := openTestPTY(t)
	if err := unix.IoctlSetWinsize(int(master.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Row: uint16(rows), Col: uint16(cols)}); err != nil {
		t.Fatal(err)
	}
	if _, err := MakeRaw(int(slave.Fd())); err != nil {
		t.Fatal(err)
	}
	s := &swScreen{t: t, master: master, slave: slave, cols: cols, rows: rows, sizes: []swSize{{0, cols, rows}}}
	s.term = NewProcessTerminal(slave, slave)
	if writer != (s.term.screen != nil) {
		t.Fatalf("screen writer on = %v, want %v", s.term.screen != nil, writer)
	}
	go func() {
		buf := make([]byte, chunk)
		for {
			n, err := master.Read(buf)
			if n > 0 {
				s.mu.Lock()
				s.data = append(s.data, buf[:n]...)
				s.mu.Unlock()
			}
			if err != nil {
				return
			}
			if pause > 0 {
				time.Sleep(pause)
			}
		}
	}()
	return s
}

func (s *swScreen) write(data string) { s.term.Write(data) }

func (s *swScreen) writes(list ...string) {
	for _, data := range list {
		s.write(data)
	}
}

// size is how much the master end has read.
func (s *swScreen) size() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.data)
}

// drain waits until the master end has read everything written and nothing more comes for a while.
func (s *swScreen) drain() {
	s.t.Helper()
	last, stable := -1, 0
	for deadline := time.Now().Add(30 * time.Second); stable < 8; {
		if time.Now().After(deadline) {
			s.t.Fatal("the master end never went quiet")
		}
		time.Sleep(10 * time.Millisecond)
		if n := s.size(); n == last {
			stable++
		} else {
			last, stable = n, 0
		}
	}
}

// settle waits for the settle write of a writer, which comes DefaultSettle after the last frame, and for the output to end.
func (s *swScreen) settle() {
	s.t.Helper()
	s.drain()
	time.Sleep(screenwriter.DefaultSettle + 150*time.Millisecond)
	s.drain()
}

// resize changes the pseudo-terminal's size, after what was written so far has been read.
func (s *swScreen) resize(cols, rows int) {
	s.t.Helper()
	s.drain()
	if err := unix.IoctlSetWinsize(int(s.master.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Row: uint16(rows), Col: uint16(cols)}); err != nil {
		s.t.Fatal(err)
	}
	s.mu.Lock()
	s.sizes = append(s.sizes, swSize{len(s.data), cols, rows})
	s.mu.Unlock()
	s.cols, s.rows = cols, rows
}

func (s *swScreen) transcript() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return string(s.data)
}

// screens are what the master end read shown on a terminal of every width model, up to the first limit bytes (all when 0).
func (s *swScreen) screens(limit int) []*gridterm.Terminal {
	s.mu.Lock()
	defer s.mu.Unlock()
	data := s.data
	if limit > 0 {
		data = data[:limit]
	}
	var out []*gridterm.Terminal
	for _, model := range gridterm.Models {
		v := gridterm.New(s.sizes[0].cols, s.sizes[0].rows, model)
		from := 0
		for _, size := range s.sizes[1:] {
			if size.at > len(data) {
				break
			}
			v.Write(string(data[from:size.at]))
			from = size.at
			v.Resize(size.cols, size.rows)
		}
		v.Write(string(data[from:]))
		out = append(out, v)
	}
	return out
}

// sameScreens fails the test when the two terminals show different screens on any width model.
func sameScreens(t *testing.T, what string, reference, writer *swScreen) {
	t.Helper()
	a, b := reference.screens(0), writer.screens(0)
	for i := range a {
		if d := gridterm.Differ(a[i], b[i]); d != "" {
			t.Errorf("%s: widths %s: %s", what, gridterm.Models[i].Name, d)
			return
		}
	}
}

// afterLast is what follows the last occurrence of marker in s, "" when s has none.
func afterLast(s, marker string) string {
	i := strings.LastIndex(s, marker)
	if i < 0 {
		return ""
	}
	return s[i+len(marker):]
}

// both runs script against a terminal without and with the writer, each cols by rows, and returns the two.
func swBoth(t *testing.T, cols, rows int, script func(s *swScreen)) (reference, writer *swScreen) {
	t.Helper()
	reference = newSWScreen(t, false, cols, rows, 4096, 0)
	script(reference)
	reference.settle()
	writer = newSWScreen(t, true, cols, rows, 4096, 0)
	script(writer)
	writer.settle()
	return reference, writer
}

// swScenario is a scenario of the terminal-engine corpus as the writes the alternate screen made and the resizes between them.
func swScenario(t *testing.T, name string) (cols, rows int, steps []swStep) {
	t.Helper()
	for _, sc := range append(append(append([]termScenario{}, termCorpusScenarios...), termKnownScenarios...), termRandomMix(2)) {
		if sc.name != name {
			continue
		}
		variant := &termCorpusVariant{}
		playTermScenario(t, sc, &termRecorder{variant}, true)
		start := 0
		for _, step := range variant.Steps {
			steps = append(steps, swStep{step.Resize, gridterm.SplitWrites(string(variant.data[start:step.End]))})
			start = step.End
		}
		return sc.cols, sc.rows, steps
	}
	t.Fatalf("no scenario %q", name)
	return 0, 0, nil
}

type swStep struct {
	resize []int
	writes []string
}

func (s *swScreen) play(steps []swStep) {
	for _, step := range steps {
		if step.resize != nil {
			s.resize(step.resize[0], step.resize[1])
		}
		s.writes(step.writes...)
	}
}

// The switch is the exact value 1; every other value, and the variable unset, leaves the terminal sending the renderers' bytes.
func TestScreenWriterSwitch(t *testing.T) {
	for value, want := range map[string]bool{"": false, "1": true, "0": false, "true": false, "yes": false, "pi": false, " 1": false} {
		t.Run("value="+value, func(t *testing.T) {
			t.Setenv(screenWriterEnv, value)
			var out bytes.Buffer
			terminal := NewProcessTerminalWithOutput(nil, nil, &out)
			if (terminal.screen != nil) != want {
				t.Fatalf("screen writer present = %v, want %v", terminal.screen != nil, want)
			}
			if want {
				return
			}
			var wrote strings.Builder
			for _, data := range append(swStarted(), swFrame(swLine(1, "one")), swFrame(swLine(1, "one")), "\x1b[?1049l") {
				terminal.Write(data)
				wrote.WriteString(data)
			}
			if out.String() != wrote.String() {
				t.Errorf("with the writer off the terminal was sent %q, want exactly %q", out.String(), wrote.String())
			}
		})
	}
}

// PIG_TUI_SCREEN_WRITER_LOG records every write of a terminal with the writer on as it came and as it went on.
func TestScreenWriterLogRecordsEveryWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "writes.bin")
	t.Setenv(screenWriterEnv, "1")
	t.Setenv(screenWriterLogEnv, path)
	var out bytes.Buffer
	terminal := NewProcessTerminalWithOutput(nil, nil, &out)
	inputs := append(swStarted(), swFrame(swLine(1, "one")), swFrame(swLine(1, "one")), swFrame(swLine(1, "one!")), "\x1b[?25h")
	for _, data := range inputs {
		terminal.Write(data)
	}
	log, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var in, sent strings.Builder
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
		log = rest
	}
	if in.String() != strings.Join(inputs, "") {
		t.Errorf("the log's inputs are not what was written")
	}
	if sent.String() != out.String() {
		t.Errorf("the log's outputs are %q, the terminal was sent %q", sent.String(), out.String())
	}
}

// The corpus scenarios through a real terminal: the same screen with and without the writer, and fewer bytes with it.
func TestScreenWriterTerminalShowsTheSameScreens(t *testing.T) {
	for _, name := range []string{"stream-follow", "stream-tokens-dock", "resize-mid-stream", "wide-styled", "random-mix-2"} {
		t.Run(name, func(t *testing.T) {
			cols, rows, steps := swScenario(t, name)
			reference, writer := swBoth(t, cols, rows, func(s *swScreen) { s.play(steps) })
			sameScreens(t, name, reference, writer)
			if stats := writer.term.screen.Stats(); stats.Translated == 0 {
				t.Errorf("the writer translated no frame: %+v", stats)
			}
			if len(writer.transcript()) >= len(reference.transcript()) {
				t.Errorf("the writer sent %d bytes, the renderers wrote %d", len(writer.transcript()), len(reference.transcript()))
			}
		})
	}
}

// A pty whose reader is slow: the terminal's write blocks on the full pty, and what is sent is still the same screen. The recorded
// 50-file write of the benchmark is 711 KB, many times the pty's buffer.
func TestScreenWriterSlowPTY(t *testing.T) {
	writes := screenWriterStreams(t)["race-write50"]
	reference := newSWScreen(t, false, 160, 48, 512, 100*time.Microsecond)
	writer := newSWScreen(t, true, 160, 48, 512, 100*time.Microsecond)
	for _, s := range []*swScreen{reference, writer} {
		done := make(chan struct{})
		go func() { s.writes(writes...); close(done) }()
		select {
		case <-done:
		case <-time.After(60 * time.Second):
			t.Fatal("the writes did not finish through a slow pty")
		}
		s.settle()
	}
	sameScreens(t, "race-write50 through a slow pty", reference, writer)
	if len(writer.transcript())*2 >= len(reference.transcript()) {
		t.Errorf("the writer sent %d bytes of the %d the renderers wrote", len(writer.transcript()), len(reference.transcript()))
	}
}

// Leaving the alternate screen: the bytes outside it are the renderers' bytes, the screen of each session is the same, and nothing is
// written after the screen was left, whatever the settle timer had pending.
func TestScreenWriterAlternateScreenEnterAndLeave(t *testing.T) {
	script := func(s *swScreen) {
		s.write("main screen one\r\n")
		s.writes(swStarted()...)
		s.writes(swFrame(swLine(1, "first"), swLine(2, "session"), swLine(3, "text")), swFrame(swLine(1, "first"), swLine(2, "session!"), swLine(3, "text")))
		s.settle()
		s.write("\x1b[?25h" + swLeave)
		s.write("main screen two\r\n")
		s.writes(swStarted()...)
		s.writes(swFrame("\x1b[2J", swLine(1, "second"), swLine(2, "one")), swFrame(swLine(1, "second"), swLine(2, "one!!")))
		s.write("\x1b[?25h" + swLeave) // the settle write of this session is pending
		s.write("after\r\n")
	}
	reference, writer := swBoth(t, 40, 10, script)
	r, w := reference.transcript(), writer.transcript()
	if got, want := afterLast(w, swLeave), afterLast(r, swLeave); got != want || want != "after\r\n" {
		t.Errorf("after the last screen the writer wrote %q, the renderers %q", got, want)
	}
	for _, marker := range []string{"main screen one\r\n", "main screen two\r\n"} {
		if strings.Count(w, marker) != 1 {
			t.Errorf("%q written %d times", marker, strings.Count(w, marker))
		}
	}
	between := func(s string) string {
		a := strings.Index(s, "main screen one\r\n")
		return s[a : a+len("main screen one\r\n")]
	}
	if between(w) != between(r) {
		t.Errorf("the text before the screen differs")
	}
	sameScreens(t, "enter and leave", reference, writer)
}

// An exit: the renderer leaves the screen, the terminal stops and drains; the exit restore of an exit that skipped them. Whatever
// settle write was pending is not written after the screen was left.
func TestScreenWriterExit(t *testing.T) {
	frames := func(s *swScreen) {
		s.writes(swStarted()...)
		s.writes(swFrame(swLine(1, "one"), swLine(2, "two"), swLine(3, "three")), swFrame(swLine(1, "one"), swLine(2, "two!"), swLine(3, "three")))
	}
	t.Run("the settle write comes when nothing exits", func(t *testing.T) {
		s := newSWScreen(t, true, 40, 10, 4096, 0)
		frames(s)
		s.settle()
		if stats := s.term.screen.Stats(); stats.Settles != 1 {
			t.Fatalf("settle writes = %d, want 1: the exit cases below would prove nothing", stats.Settles)
		}
	})
	exits := map[string]func(s *swScreen){
		"the renderer stops, the terminal stops and drains": func(s *swScreen) {
			s.write("\x1b[?25h" + swLeave)
			if err := s.term.Start(func(string) {}, func() {}); err != nil {
				t.Fatal(err)
			}
			s.term.Stop()
			if err := s.term.DrainInput(100*time.Millisecond, 20*time.Millisecond); err != nil {
				t.Fatal(err)
			}
		},
		"the exit restore": func(s *swScreen) {
			s.term.RestoreOnExit(exitRestorePrefix(s.term, true, false), exitRestoreSuffix(true))
			s.write("a frame still in flight")
		},
		"the exit restore while a write holds the output lock": func(s *swScreen) {
			s.term.outMu.Lock()
			done := make(chan struct{})
			go func() {
				s.term.RestoreOnExit(exitRestorePrefix(s.term, true, false), exitRestoreSuffix(true))
				close(done)
			}()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				s.term.outMu.Unlock()
				t.Fatal("the exit restore waited for the output lock")
			}
			s.term.outMu.Unlock()
		},
	}
	for name, exit := range exits {
		t.Run(name, func(t *testing.T) {
			var tails [2]string
			var settles int
			for i, writer := range []bool{false, true} {
				s := newSWScreen(t, writer, 40, 10, 4096, 0)
				frames(s)
				exit(s) // the settle timer is armed and about to fire
				s.drain()
				before := s.size()
				time.Sleep(screenwriter.DefaultSettle + 200*time.Millisecond)
				s.drain()
				tails[i] = s.transcript()
				if writer {
					settles = s.term.screen.Stats().Settles
					if got := s.transcript()[before:]; got != "" {
						t.Errorf("written after the exit: %q", got)
					}
				}
			}
			if settles != 0 {
				t.Errorf("%d settle writes after the exit", settles)
			}
			if got, want := afterLast(tails[1], swLeave), afterLast(tails[0], swLeave); got != want {
				t.Errorf("after leaving the screen the writer sent %q, the renderers' bytes %q", got, want)
			}
		})
	}
}

// Suspend and resume: the terminal stops (the shell has it), starts again and the renderer enters the screen and repaints.
func TestScreenWriterSuspendAndResume(t *testing.T) {
	script := func(s *swScreen) {
		start := func() {
			if err := s.term.Start(func(string) {}, func() {}); err != nil {
				t.Fatal(err)
			}
		}
		start()
		s.writes(swStarted()...)
		s.writes(swFrame(swLine(1, "before"), swLine(2, "the suspend")), swFrame(swLine(1, "before"), swLine(2, "the suspend!")))
		s.write("\x1b[?25h" + swLeave) // the renderer stops
		s.term.Stop()
		s.write("$ fg\r\n")
		start() // fg
		s.writes(swStarted()...)
		s.writes(swFrame("\x1b[2J", swLine(1, "after"), swLine(2, "the resume")), swFrame(swLine(1, "after"), swLine(2, "the resume!!")))
		s.settle()
		s.write("\x1b[?25h" + swLeave)
		s.term.Stop()
	}
	reference, writer := swBoth(t, 40, 10, script)
	sameScreens(t, "suspend and resume", reference, writer)
	between := func(s string) string {
		a := strings.Index(s, swLeave)
		b := strings.Index(s[a:], swEnter)
		return s[a : a+b]
	}
	if got, want := between(writer.transcript()), between(reference.transcript()); got != want {
		t.Errorf("between the screens the writer sent %q, the renderers' bytes %q", got, want)
	}
	if got, want := afterLast(writer.transcript(), swLeave), afterLast(reference.transcript(), swLeave); got != want {
		t.Errorf("after the last screen the writer sent %q, the renderers' bytes %q", got, want)
	}
}

// A resize between frames: the terminal's screen is no longer the one the writer kept. Nothing is written for the old size after
// the resize (the settle write would wrap rows of the old width), a frame laid out for the old size is sent as it came, and the
// repaint that follows is the same screen.
func TestScreenWriterResizeBetweenFrames(t *testing.T) {
	var resizedAt [2]int
	var transcripts [2]string
	script := func(i int) func(s *swScreen) {
		return func(s *swScreen) {
			s.writes(swStarted()...)
			s.writes(
				swFrame(swLine(1, "one"), swLine(2, "two"), swLine(9, "nine, a row of the old size")),
				swFrame(swLine(1, "one"), swLine(2, "two!"), swLine(9, "nine, a row of the old size!")),
			)
			s.resize(30, 8)
			resizedAt[i] = s.size()
			time.Sleep(screenwriter.DefaultSettle + 200*time.Millisecond) // the settle write is due
			s.drain()
			transcripts[i] = s.transcript()
			s.write(swFrame(swLine(1, "one"), swLine(2, "two!!"), swLine(9, "nine, laid out for the old size")))
			s.writes(swFrame("\x1b[2J", swLine(1, "one"), swLine(2, "two!!"), swLine(8, "eight")), swFrame(swLine(1, "one"), swLine(2, "two!!!"), swLine(8, "eight")))
		}
	}
	reference := newSWScreen(t, false, 40, 10, 4096, 0)
	script(0)(reference)
	reference.settle()
	writer := newSWScreen(t, true, 40, 10, 4096, 0)
	script(1)(writer)
	writer.settle()
	if got := transcripts[1][resizedAt[1]:]; got != "" {
		t.Errorf("after the resize and before the repaint the writer sent %q", got)
	}
	sameScreens(t, "resize between frames", reference, writer)
}

// Kitty and iTerm2 image rows are sent as they came, and the frames after them show the same screen.
func TestScreenWriterImageRows(t *testing.T) {
	kitty := "\x1b_Ga=T,f=100,c=4,r=2,i=7;iVBORw0KGgo=\x1b\\"
	iterm := "\x1b]1337;File=inline=1;width=4;height=2:iVBORw0KGgo=\a"
	imageFrame := swFrame(swLine(1, "one"), swLine(2, kitty+"    "), swLine(3, iterm+"    "), swLine(4, "four"))
	script := func(s *swScreen) {
		s.writes(swStarted()...)
		s.writes(
			swFrame(swLine(1, "one"), swLine(2, "two"), swLine(3, "three"), swLine(4, "four")),
			swFrame(swLine(1, "one!"), swLine(2, "two"), swLine(3, "three"), swLine(4, "four")),
			imageFrame,
			swFrame(swLine(1, "one!"), swLine(2, "    "), swLine(3, "    "), swLine(4, "four!")),
			swFrame(swLine(1, "one!!"), swLine(2, "    "), swLine(3, "    "), swLine(4, "four!")),
		)
	}
	reference, writer := swBoth(t, 40, 10, script)
	if !strings.Contains(writer.transcript(), imageFrame) {
		t.Errorf("the frame with the image rows was not sent as it came")
	}
	if stats := writer.term.screen.Stats(); stats.Unread != 1 || stats.Translated < 2 {
		t.Errorf("stats %+v: want the one image frame unread and the others translated", stats)
	}
	sameScreens(t, "image rows", reference, writer)
}

// A write that goes to the terminal's file past the terminal (WriteToTerminal, an extension's editor handing over the screen, text
// printed with the alternate screen shown) is told to the writer: the rows it scribbled on are written whole when the frames rewrite them.
func TestScreenWriterWritesPastTheTerminal(t *testing.T) {
	script := func(s *swScreen) {
		s.writes(swStarted()...)
		s.writes(swFrame(swLine(1, "one"), swLine(2, "two"), swLine(3, "three")))
		s.writes(swFrame(swLine(1, "one"), swLine(2, "two"), swLine(3, "three")))
		WriteToTerminal(s.slave, swAt(2, 1)+"SCRIBBLE"+swAt(1, 1))
		s.writes(swFrame(swLine(1, "one"), swLine(2, "two"), swLine(3, "three!")))
	}
	reference, writer := swBoth(t, 40, 10, script)
	sameScreens(t, "write past the terminal", reference, writer)
	for _, s := range []*swScreen{reference, writer} {
		if row := s.screens(0)[0].Row(1); row != "two" {
			t.Errorf("row 2 shows %q after the frames rewrote it", row)
		}
	}
}
