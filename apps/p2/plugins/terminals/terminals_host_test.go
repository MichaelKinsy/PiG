package terminals

import (
	"bytes"
	"context"
	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/plugins/terminals/ghostty"
)

// Ports test/p2/terminals-host.test.ts: terminals run in the detached host, survive disconnects and come back as
// snapshots; closing asks first while the shell runs a job; programs see the viewer's colors; a viewer that falls behind
// stops receiving output and resyncs; a snapshot larger than the window does not resync a viewer that keeps up.

func (c *hostConn) text() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return string(bytes.Join(c.output, nil))
}

func (c *hostConn) bytesReceived() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	total := 0
	for _, chunk := range c.output {
		total += len(chunk)
	}
	return total
}

func (c *hostConn) count(eventType string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	total := 0
	for _, event := range c.events {
		if event.Type == eventType {
			total++
		}
	}
	return total
}

func (c *hostConn) first(eventType string) (TerminalEvent, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, event := range c.events {
		if event.Type == eventType {
			return event, true
		}
	}
	return TerminalEvent{}, false
}

func (c *hostConn) last() TerminalEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.events[len(c.events)-1]
}

func (c *hostConn) type_(text string) {
	c.t.Helper()
	frame, err := EncodeFrame([]byte(text))
	if err != nil {
		c.t.Fatal(err)
	}
	if _, err := c.conn.Write(frame); err != nil {
		c.t.Fatal(err)
	}
}

func (c *hostConn) until(check func() bool, timeout ...time.Duration) {
	c.t.Helper()
	limit := 10 * time.Second
	if len(timeout) > 0 {
		limit = timeout[0]
	}
	deadline := time.Now().Add(limit)
	for !check() {
		if time.Now().After(deadline) {
			c.t.Fatalf("Timed out; output: %q", c.text())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (c *hostConn) awaitPrompt() {
	c.t.Helper()
	prompt := regexp.MustCompile(`[$#] $`)
	c.until(func() bool { return prompt.MatchString(c.text()) })
}

func (c *hostConn) awaitText(text string, timeout ...time.Duration) {
	c.t.Helper()
	c.until(func() bool { return strings.Contains(c.text(), text) }, timeout...)
}

func (c *hostConn) awaitEvents(eventType string, n int) {
	c.t.Helper()
	c.until(func() bool { return c.count(eventType) >= n })
}

// realTempDir is a temporary directory with its symlinks resolved (`realpath(mkdtemp(...))`).
func realTempDir(t *testing.T, prefix string) string {
	t.Helper()
	directory, err := os.MkdirTemp("", prefix)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	real, err := realpath(directory)
	if err != nil {
		t.Fatal(err)
	}
	return real
}

func TestTerminalsRunInTheHostSurviveDisconnectsAndComeBackAsSnapshots(t *testing.T) {
	p2test.NoLeakedGoroutines(t)
	useDaemon(t)
	directory := t.TempDir()
	cwd := realTempDir(t, "p2-terminal-cwd-")
	first := dialHost(t, directory)
	t.Cleanup(func() { shutdownHost(t, directory) })
	first.send(TerminalRequest{Type: RequestOpen, SessionID: "s1", Cwd: cwd, Cols: 40, Rows: 10})
	var attached TerminalEvent
	first.until(func() bool { var ok bool; attached, ok = first.first(EventAttached); return ok })
	id := attached.Terminal.ID
	// Typed before the shell's first prompt, the line is echoed first and the prompt then precedes the output
	// ("$ in /..."), as in any terminal; a slow shell start (Termux) lost that race.
	first.awaitPrompt()
	first.type_("printf 'in %s\\n' \"$PWD\"; echo $TERM\n")
	first.awaitText("in " + cwd)
	first.awaitText("xterm-256color")
	_ = first.conn.Close()

	// A new connection lists it and attaches: the snapshot has the earlier output.
	second := dialHost(t, directory)
	second.send(TerminalRequest{Type: RequestList, SessionID: "s1"})
	var list TerminalEvent
	second.until(func() bool { var ok bool; list, ok = second.first(EventList); return ok })
	if len(list.Terminals) != 1 || list.Terminals[0].ID != id {
		t.Fatalf("list = %+v", list.Terminals)
	}
	second.send(TerminalRequest{Type: RequestList, SessionID: "other"})
	second.awaitEvents(EventList, 2)
	if last := second.last(); last.Type != EventList || len(last.Terminals) != 0 {
		t.Fatalf("last = %+v", last)
	}
	second.send(TerminalRequest{Type: RequestAttach, TerminalID: id})
	second.until(func() bool { second.mu.Lock(); defer second.mu.Unlock(); return len(second.output) > 0 })
	second.mu.Lock()
	snapshot := second.output[0]
	second.mu.Unlock()
	engine, err := ghostty.Load(context.Background(), ghosttyWasm)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close(context.Background())
	vt, err := engine.Terminal(ghostty.Options{Cols: 40, Rows: 10, Scrollback: 100})
	if err != nil {
		t.Fatal(err)
	}
	defer vt.Free()
	vt.Write(snapshot)
	var rows []string
	for _, row := range vt.Frame(true).Changed {
		var line strings.Builder
		for _, cell := range row.Cells {
			if cell.Text == "" {
				line.WriteByte(' ')
			} else {
				line.WriteString(cell.Text)
			}
		}
		rows = append(rows, line.String())
	}
	screen := strings.Join(rows, "\n")
	// Long lines wrap at 40 columns.
	if !regexp.MustCompile(`(?m)^in /`).MatchString(screen) || !regexp.MustCompile(`(?m)^xterm-256color`).MatchString(screen) {
		t.Fatalf("screen = %q", screen)
	}

	// Resizing reaches the program; its exit is reported.
	second.send(TerminalRequest{Type: RequestResize, Cols: 50, Rows: 12})
	second.type_("stty size; exit 7\n")
	second.awaitText("12 50")
	var exited TerminalEvent
	second.until(func() bool {
		second.mu.Lock()
		defer second.mu.Unlock()
		for _, event := range second.events {
			if event.Type == EventChanged && event.Terminal.Exited != nil {
				exited = event
				return true
			}
		}
		return false
	})
	if code := exited.Terminal.Exited.Code; code == nil || *code != 7 || exited.Terminal.Exited.Error != nil {
		t.Fatalf("exited = %+v", exited.Terminal.Exited)
	}

	second.send(TerminalRequest{Type: RequestClose})
	second.until(func() bool { _, ok := second.first(EventClosed); return ok })
	second.send(TerminalRequest{Type: RequestList, SessionID: "s1"})
	second.awaitEvents(EventList, 3)
	if last := second.last(); last.Type != EventList || len(last.Terminals) != 0 {
		t.Fatalf("last = %+v", last)
	}
}

func TestTerminalsClosingAsksFirstWhileTheShellRunsAJob(t *testing.T) {
	p2test.NoLeakedGoroutines(t)
	useDaemon(t)
	directory := t.TempDir()
	cwd := realTempDir(t, "p2-terminal-busy-cwd-")
	viewer := dialHost(t, directory)
	t.Cleanup(func() { shutdownHost(t, directory) })
	opened := func() {
		count := viewer.count(EventAttached)
		viewer.send(TerminalRequest{Type: RequestOpen, SessionID: "s1", Cwd: cwd, Cols: 80, Rows: 10})
		viewer.until(func() bool { return viewer.count(EventAttached) > count })
	}
	// At the prompt: closes at once.
	opened()
	viewer.type_("echo PRO''MPT\n")
	viewer.awaitText("PROMPT")
	viewer.send(TerminalRequest{Type: RequestClose})
	viewer.until(func() bool { _, ok := viewer.first(EventClosed); return ok })

	// A job in front: the host names it and keeps the terminal until a forced close.
	opened()
	// The job prints once it runs in front of the terminal; the shell hands it over before that.
	viewer.type_("sh -c 'echo RUN''NING; exec sleep 100'\n")
	viewer.awaitText("RUNNING")
	viewer.send(TerminalRequest{Type: RequestClose})
	var busy TerminalEvent
	viewer.until(func() bool { var ok bool; busy, ok = viewer.first(EventBusy); return ok })
	// The job's name: its shell, or `sleep` once it has run `exec`.
	if busy.Program == "" {
		t.Fatalf("busy = %+v", busy)
	}
	if got := viewer.count(EventClosed); got != 1 {
		t.Fatalf("closed %d times", got)
	}
	force := true
	viewer.send(TerminalRequest{Type: RequestClose, Force: &force})
	viewer.until(func() bool { return viewer.count(EventClosed) == 2 })
	viewer.send(TerminalRequest{Type: RequestList, SessionID: "s1"})
	var list TerminalEvent
	viewer.until(func() bool { var ok bool; list, ok = viewer.first(EventList); return ok })
	if len(list.Terminals) != 0 {
		t.Fatalf("list = %+v", list.Terminals)
	}
}

func TestTerminalsProgramsSeeTheViewersColorsAndHearAboutLightDarkSwitches(t *testing.T) {
	p2test.NoLeakedGoroutines(t)
	useDaemon(t)
	directory := t.TempDir()
	cwd := realTempDir(t, "p2-terminal-theme-cwd-")
	viewer := dialHost(t, directory)
	t.Cleanup(func() { shutdownHost(t, directory) })
	viewer.send(TerminalRequest{Type: RequestTheme, Theme: TerminalTheme{Foreground: 0x1d1b19, Background: 0xfbfaf8, Scheme: "light"}})
	viewer.send(TerminalRequest{Type: RequestOpen, SessionID: "s1", Cwd: cwd, Cols: 80, Rows: 10})
	viewer.type_("echo fgbg=$COLORFGBG\n")
	viewer.awaitText("fgbg=0;15")
	// A program asks for the background color (OSC 11) and reads the answer from its input.
	viewer.type_("stty raw -echo; printf '\\033]11;?\\007'; r=$(dd bs=1 count=24 2>/dev/null); stty sane; printf 'got%s\\n' \"$r\" | tr -d '\\033\\007'\n")
	viewer.awaitText("got]11;rgb:fbfb/fafa/f8f8")
	// A program that enabled mode 2031 is told when the viewer switches to dark.
	viewer.type_("stty raw -echo; printf '\\033[?2031hREADY'; r=$(dd bs=1 count=9 2>/dev/null); stty sane; printf 'scheme%s\\n' \"$r\" | tr -d '\\033'\n")
	// The program's output, not the echoed command line.
	viewer.awaitText("\x1b[?2031hREADY")
	viewer.send(TerminalRequest{Type: RequestTheme, Theme: TerminalTheme{Foreground: 0xe6e6e6, Background: 0x141414, Scheme: "dark"}})
	viewer.awaitText("scheme[?997;1n")
}

func TestTerminalsAViewerThatFallsBehindStopsReceivingOutputAndResyncsWithASnapshot(t *testing.T) {
	p2test.NoLeakedGoroutines(t)
	useDaemon(t)
	directory := t.TempDir()
	cwd := realTempDir(t, "p2-terminal-cwd-")
	viewer := dialHost(t, directory)
	t.Cleanup(func() { shutdownHost(t, directory) })
	viewer.send(TerminalRequest{Type: RequestOpen, SessionID: "s1", Cwd: cwd, Cols: 40, Rows: 10})
	viewer.until(func() bool { _, ok := viewer.first(EventAttached); return ok })
	viewer.awaitPrompt()
	// About 8 MiB of output, then a marker once the flood is over.
	viewer.type_("yes 0123456789abcdef0123456789abcdef | head -n 230000; echo flood-done\n")
	viewer.until(func() bool { return viewer.bytesReceived() > TerminalOutputWindow })
	// Without acknowledgements the host stops after one window plus the chunk that crossed it.
	time.Sleep(2 * time.Second)
	received := viewer.bytesReceived()
	if received >= 2*TerminalOutputWindow {
		t.Fatalf("received %d bytes without acknowledging", received)
	}
	if got := viewer.count(EventAttached); got != 1 {
		t.Fatalf("attached %d times", got)
	}

	// Acknowledging everything resyncs: `attached` again and a snapshot of the screen as it is now.
	viewer.send(TerminalRequest{Type: RequestAck, Bytes: received})
	viewer.until(func() bool { return viewer.count(EventAttached) == 2 })
	viewer.until(func() bool { viewer.mu.Lock(); defer viewer.mu.Unlock(); return len(viewer.output) > 0 })
	viewer.mu.Lock()
	snapshot := viewer.output[len(viewer.output)-1]
	viewer.mu.Unlock()
	viewer.send(TerminalRequest{Type: RequestAck, Bytes: viewer.bytesReceived() - received})
	viewer.awaitText("flood-done", 30*time.Second)
	if len(snapshot) == 0 {
		t.Fatal("empty snapshot")
	}
}

// acknowledging acknowledges everything the connection receives, as the web terminal does (every 10 ms); it returns the
// bytes received so far, and a stop.
func acknowledging(connection *hostConn) (received func() int, stop func()) {
	acked := 0
	done := make(chan struct{})
	var group sync.WaitGroup
	group.Add(1)
	go func() {
		defer group.Done()
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if pending := connection.bytesReceived() - acked; pending != 0 {
					acked += pending
					connection.send(TerminalRequest{Type: RequestAck, Bytes: pending})
				}
			case <-done:
				return
			}
		}
	}()
	return connection.bytesReceived, func() { close(done); group.Wait() }
}

func TestTerminalsASnapshotLargerThanTheOutputWindowDoesNotResyncAViewerThatKeepsUp(t *testing.T) {
	p2test.NoLeakedGoroutines(t)
	useDaemon(t)
	directory := t.TempDir()
	cwd := realTempDir(t, "p2-terminal-cwd-")
	writer := dialHost(t, directory)
	t.Cleanup(func() { shutdownHost(t, directory) })
	_, stopWriter := acknowledging(writer)
	defer stopWriter()
	writer.send(TerminalRequest{Type: RequestOpen, SessionID: "s1", Cwd: cwd, Cols: 80, Rows: 24})
	var attached TerminalEvent
	writer.until(func() bool { var ok bool; attached, ok = writer.first(EventAttached); return ok })
	writer.awaitPrompt()
	// 400 lines with a different truecolor foreground per cell: the scrollback snapshot is several windows large. The
	// markers are computed by the shell, so the echoed command line does not contain them.
	writer.type_(`awk 'BEGIN{for(i=0;i<400;i++){s="";for(j=0;j<79;j++)s=s sprintf("\033[38;2;%d;%d;%dmx",i%256,j*3,(i*j)%256);print s "\033[0m"}}'; echo colors-$((1+1))` + "\n")
	writer.awaitText("colors-2", 30*time.Second)

	current := dialHost(t, directory)
	received, stopCurrent := acknowledging(current)
	defer stopCurrent()
	current.send(TerminalRequest{Type: RequestAttach, TerminalID: attached.Terminal.ID})
	current.until(func() bool { current.mu.Lock(); defer current.mu.Unlock(); return len(current.output) > 0 })
	current.mu.Lock()
	snapshot := current.output[0]
	current.mu.Unlock()
	if len(snapshot) <= 2*TerminalOutputWindow {
		t.Fatalf("snapshot of %d bytes", len(snapshot))
	}
	time.Sleep(1500 * time.Millisecond)
	// One snapshot and nothing more: the terminal is idle. Before the fix it resent the snapshot in a loop.
	if got := current.count(EventAttached); got != 1 {
		t.Fatalf("attached %d times", got)
	}
	if got := received(); got >= len(snapshot)+4096 {
		t.Fatalf("received %d bytes for a %d byte snapshot", got, len(snapshot))
	}

	// Live output still reaches it.
	writer.type_("echo live-$((2+3))\n")
	current.awaitText("live-5")
	if got := current.count(EventAttached); got != 1 {
		t.Fatalf("attached %d times", got)
	}
}
