//go:build windows

package tui

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const consoleReadResultEnv = "PIG_TUI_CONSOLE_READ_RESULT"

// Pi 0.87.1 binds undo to Ctrl+Z on win32, and Node's TTY delivers Ctrl+Z
// (0x1A) from the console as a key. ReadInput must deliver it too. Reading the
// console through os.File.Read reported a Ctrl+Z at the start of a read as end
// of file, which ended pig's interactive input loop with "error: EOF".
func TestReadInputDeliversCtrlZFromTheConsole(t *testing.T) {
	if result := os.Getenv(consoleReadResultEnv); result != "" {
		readConsoleUntil(t, result, 'b')
		return
	}
	result := filepath.Join(t.TempDir(), "read")
	t.Setenv(consoleReadResultEnv, result)
	console := startInPseudoConsole(t, os.Args[0], "-test.run=^TestReadInputDeliversCtrlZFromTheConsole$", "-test.count=1")
	console.waitForFile(t, result+".ready")
	keys := "a\x1aé\U0001F600\x1ab"
	console.write(t, keys)
	console.wait(t)
	got, err := os.ReadFile(result)
	if err != nil {
		t.Fatalf("read the helper result: %v\nconsole output: %q", err, console.output())
	}
	if string(got) != keys {
		t.Fatalf("ReadInput delivered %q, want %q", got, keys)
	}
}

// A surrogate pair split across two console reads decodes to one rune, and an
// unpaired surrogate becomes U+FFFD.
func TestConsoleInputJoinsSurrogatesAcrossReads(t *testing.T) {
	for _, tc := range []struct {
		name  string
		reads [][]uint16
		want  string
	}{
		{"pair in one read", [][]uint16{{'a', 0xD83D, 0xDE00, 0x1A}}, "a\U0001F600\x1a"},
		{"pair split across reads", [][]uint16{{'a', 0xD83D}, {0xDE00, 'b'}}, "a\U0001F600b"},
		{"high surrogate then a letter", [][]uint16{{0xD83D}, {'x'}}, "�x"},
		{"lone low surrogate", [][]uint16{{0xDE00, 'y'}}, "�y"},
		{"two high surrogates", [][]uint16{{0xD83D, 0xD83D, 0xDE00}}, "�\U0001F600"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var c consoleInput
			var got []byte
			for _, units := range tc.reads {
				got = c.appendUTF8(got, units)
			}
			if string(got) != tc.want {
				t.Fatalf("decoded %q, want %q", got, tc.want)
			}
		})
	}
}

const consoleNegotiationResultEnv = "PIG_TUI_CONSOLE_NEGOTIATION_RESULT"

// Pi consumes a DA reply and returns to its event loop. No key or EOF is
// necessary to stop the terminal after negotiation, including on ReadConsoleW.
func TestStartedTerminalStopsAfterConsoleNegotiation(t *testing.T) {
	if result := os.Getenv(consoleNegotiationResultEnv); result != "" {
		output := &negotiationWriter{seen: make(chan struct{})}
		terminal := NewProcessTerminalWithOutput(os.Stdin, os.Stdout, io.MultiWriter(os.Stdout, output))
		if err := terminal.Start(func(data []byte) { t.Errorf("unexpected input: %q", data) }, nil); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(result+".ready", nil, 0o600); err != nil {
			t.Fatal(err)
		}
		select {
		case <-output.seen:
		case <-time.After(5 * time.Second):
			t.Fatal("DA reply did not reach the console reader")
		}
		stopped := make(chan struct{})
		go func() { terminal.Stop(); close(stopped) }()
		select {
		case <-stopped:
		case <-time.After(5 * time.Second):
			t.Fatal("Stop blocked after console negotiation without a following key")
		}
		if err := os.WriteFile(result, []byte("stopped"), 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	result := filepath.Join(t.TempDir(), "negotiation")
	t.Setenv(consoleNegotiationResultEnv, result)
	console := startInPseudoConsole(t, os.Args[0], "-test.run=^TestStartedTerminalStopsAfterConsoleNegotiation$", "-test.count=1")
	console.waitForFile(t, result+".ready")
	console.write(t, "\x1b[?62;22c")
	console.wait(t)
	got, err := os.ReadFile(result)
	if err != nil || string(got) != "stopped" {
		t.Fatalf("console stop result = %q, %v", got, err)
	}
}

const consoleHandoffResultEnv = "PIG_TUI_CONSOLE_HANDOFF_RESULT"

// One console read can return more input than one delivery holds, and input
// the reader has already taken from the console does not signal the handle
// again. A started terminal must still deliver all of it with no further
// keystroke. A console also signals for records that yield no character, such
// as key releases; the reader must not block on those, or stopping the
// terminal waits for the next keystroke. A second terminal on stdin then reads
// the next input, as the interactive editor does after a startup prompt.
func TestStartedTerminalsDeliverEveryBufferedConsoleByte(t *testing.T) {
	if result := os.Getenv(consoleHandoffResultEnv); result != "" {
		readConsoleAcrossTerminals(t, result)
		return
	}
	result := filepath.Join(t.TempDir(), "read")
	t.Setenv(consoleHandoffResultEnv, result)
	console := startInPseudoConsole(t, os.Args[0], "-test.run=^TestStartedTerminalsDeliverEveryBufferedConsoleByte$", "-test.count=1")
	console.waitForFile(t, result+".ready")
	// Type the first batch before the child reads, so its first console read
	// returns more than one delivery holds.
	first := strings.Repeat("\u00e9", 200) + "Y"
	console.write(t, first)
	time.Sleep(500 * time.Millisecond)
	if err := os.WriteFile(result+".typed", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	console.waitForFile(t, result+".second")
	second := strings.Repeat("\u00fc", 150) + "Z"
	console.write(t, second)
	console.wait(t)
	for _, check := range []struct{ file, want string }{{".first", first}, {".second-read", second}} {
		got, err := os.ReadFile(result + check.file)
		if err != nil {
			t.Fatalf("read the helper result: %v\nconsole output: %q", err, console.output())
		}
		if string(got) != check.want {
			t.Fatalf("%s: the terminal delivered %d bytes, want all %d:\n%q", check.file, len(got), len(check.want), got)
		}
	}
}

// terminalReplies matches the Device Attributes and Kitty flag replies that
// starting a terminal requests; they are not keystrokes.
var terminalReplies = regexp.MustCompile(`\x1b\[\?[0-9;]*[cu]`)

// readConsoleAcrossTerminals runs in the child process attached to the pseudo
// console. The first terminal reads until 'Y', the input typed before it
// started, and is stopped after a key release reaches the console. A second
// terminal then
// reads until 'Z'. Each terminal's input goes to its own result file.
func readConsoleAcrossTerminals(t *testing.T, result string) {
	if err := os.WriteFile(result+".ready", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	waitForFile(t, result+".typed")
	readWithTerminal(t, result+".first", 'Y', nil, func() {
		// A key release signals the console handle but yields no
		// character. The running terminal sees it before it stops.
		release := keyRecord(false, 'A', 'a')
		var written uint32
		if r1, _, err := procWriteConsoleInputW.Call(uintptr(windows.Handle(os.Stdin.Fd())), uintptr(unsafe.Pointer(&release)), 1, uintptr(unsafe.Pointer(&written))); r1 == 0 { //nolint:gosec // G103: WriteConsoleInputW reads the INPUT_RECORD inputRecord mirrors and writes the count, and x/sys has no wrapper for it.
			t.Fatal(err)
		}
		time.Sleep(300 * time.Millisecond)
	})
	readWithTerminal(t, result+".second-read", 'Z', func() {
		if err := os.WriteFile(result+".second", nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}, nil)
}

var procWriteConsoleInputW = kernel32.NewProc("WriteConsoleInputW")

// keyRecord builds a KEY_EVENT input record.
func keyRecord(down bool, virtualKey, char uint16) inputRecord {
	r := inputRecord{EventType: windows.KEY_EVENT}
	if down {
		binary.LittleEndian.PutUint32(r.Event[0:4], 1)
	}
	binary.LittleEndian.PutUint16(r.Event[4:6], 1)
	binary.LittleEndian.PutUint16(r.Event[6:8], virtualKey)
	binary.LittleEndian.PutUint16(r.Event[10:12], char)
	return r
}

// Only records ReadConsoleW turns into characters count as pending input.
func TestInputRecordYieldsCharacter(t *testing.T) {
	for _, tc := range []struct {
		name   string
		record inputRecord
		want   bool
	}{
		{"a letter pressed", keyRecord(true, 'A', 'a'), true},
		{"a letter released", keyRecord(false, 'A', 'a'), false},
		{"an arrow pressed, encoded as an escape sequence", keyRecord(true, windows.VK_UP, 0), true},
		{"Shift pressed", keyRecord(true, windows.VK_SHIFT, 0), false},
		{"Ctrl pressed", keyRecord(true, windows.VK_CONTROL, 0), false},
		{"Alt released after a keypad code", keyRecord(false, windows.VK_MENU, 0xE9), true},
		{"Alt released alone", keyRecord(false, windows.VK_MENU, 0), false},
		{"a focus change", inputRecord{EventType: windows.FOCUS_EVENT}, false},
	} {
		if got := tc.record.yieldsCharacter(); got != tc.want {
			t.Errorf("%s: yieldsCharacter = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// waitForFile waits up to 20s for path to exist.
func waitForFile(t *testing.T, path string) {
	t.Helper()
	for deadline := time.Now().Add(20 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s did not appear", path)
		}
	}
}

// readWithTerminal starts a terminal on stdin, calls started, collects input
// until last arrives or none arrives for 10s, calls beforeStop, stops the
// terminal and writes what it delivered to path. A Stop that has not returned
// within 5s is an error: its reader is blocked in a console read.
func readWithTerminal(t *testing.T, path string, last byte, started, beforeStop func()) {
	var mu sync.Mutex
	var got []byte
	done := make(chan struct{})
	var doneOnce sync.Once
	readErr := make(chan error, 1)
	terminal := NewProcessTerminal(os.Stdin, os.Stdout)
	err := terminal.StartWithReadError(func(data []byte) {
		mu.Lock()
		got = append(got, data...)
		complete := bytes.IndexByte(got, last) >= 0
		mu.Unlock()
		if complete {
			doneOnce.Do(func() { close(done) })
		}
	}, nil, func(err error) { readErr <- err })
	if err != nil {
		t.Fatal(err)
	}
	if started != nil {
		started()
	}
	select {
	case <-done:
	case err := <-readErr:
		t.Error(err)
	case <-time.After(10 * time.Second):
	}
	// Only input delivered before beforeStop counts: its console events
	// must not be what delivers the rest.
	mu.Lock()
	delivered := terminalReplies.ReplaceAll(got, nil)
	mu.Unlock()
	if err := os.WriteFile(path, delivered, 0o600); err != nil {
		t.Fatal(err)
	}
	if beforeStop != nil {
		beforeStop()
	}
	stopped := make(chan struct{})
	go func() {
		terminal.Stop()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Error("Stop did not return: the terminal's reader is blocked in a console read")
	}
}

// readConsoleUntil runs in the child process attached to the pseudo console.
// It reads the console in raw mode through ReadInput until last arrives and
// writes everything it read, or the error that ended the read, to result.
func readConsoleUntil(t *testing.T, result string, last byte) {
	restore, err := EnterRawMode()
	if err != nil {
		t.Fatal(err)
	}
	defer restore()
	if err := os.WriteFile(result+".ready", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	var got []byte
	for !bytes.Contains(got, []byte{last}) {
		data, err := ReadInput(os.Stdin)
		if err != nil {
			got = append(got, "error: "+err.Error()...)
			break
		}
		got = append(got, data...)
	}
	if err := os.WriteFile(result, got, 0o600); err != nil {
		t.Fatal(err)
	}
}

type pseudoConsole struct {
	console windows.Handle
	process windows.Handle
	input   *os.File

	mu      sync.Mutex
	out     bytes.Buffer
	drained chan struct{}
}

// startInPseudoConsole starts argv attached to a new pseudo console, as a
// terminal emulator starts a shell, and drains the console's output.
func startInPseudoConsole(t *testing.T, argv ...string) *pseudoConsole {
	t.Helper()
	var inRead, inWrite, outRead, outWrite windows.Handle
	if err := windows.CreatePipe(&inRead, &inWrite, nil, 0); err != nil {
		t.Fatal(err)
	}
	if err := windows.CreatePipe(&outRead, &outWrite, nil, 0); err != nil {
		t.Fatal(err)
	}
	p := &pseudoConsole{
		input:   os.NewFile(uintptr(inWrite), "pseudo-console-input"),
		drained: make(chan struct{}),
	}
	output := os.NewFile(uintptr(outRead), "pseudo-console-output")
	err := windows.CreatePseudoConsole(windows.Coord{X: 100, Y: 30}, inRead, outWrite, 0, &p.console)
	// The pseudo console holds its own references to the ends it uses.
	_ = windows.CloseHandle(inRead)
	_ = windows.CloseHandle(outWrite)
	if err != nil {
		_ = p.input.Close()
		_ = output.Close()
		t.Skipf("pseudo consoles are unavailable: %v", err)
	}
	go func() {
		defer close(p.drained)
		_, _ = io.Copy(p, output)
	}()
	t.Cleanup(func() {
		if p.process != 0 {
			_ = windows.TerminateProcess(p.process, 1)
			_ = windows.CloseHandle(p.process)
		}
		windows.ClosePseudoConsole(p.console)
		_ = p.input.Close()
		<-p.drained
		_ = output.Close()
	})

	attributes, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		t.Fatal(err)
	}
	defer attributes.Delete()
	// The attribute value is the pseudo console handle itself.
	if err := attributes.Update(windows.PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE, *(*unsafe.Pointer)(unsafe.Pointer(&p.console)), unsafe.Sizeof(p.console)); err != nil { //nolint:gosec // G103: UpdateProcThreadAttribute takes the HPCON value in its pointer argument, and x/sys takes that value as unsafe.Pointer.
		t.Fatal(err)
	}
	var startup windows.StartupInfoEx
	startup.Cb = uint32(unsafe.Sizeof(startup))
	// Null standard handles make the child use the pseudo console even when
	// this test's own output is redirected.
	startup.Flags = windows.STARTF_USESTDHANDLES
	startup.ProcThreadAttributeList = attributes.List()
	commandLine, err := windows.UTF16PtrFromString(windows.ComposeCommandLine(argv))
	if err != nil {
		t.Fatal(err)
	}
	var info windows.ProcessInformation
	if err := windows.CreateProcess(nil, commandLine, nil, nil, false, windows.EXTENDED_STARTUPINFO_PRESENT, nil, nil, &startup.StartupInfo, &info); err != nil {
		t.Fatal(err)
	}
	_ = windows.CloseHandle(info.Thread)
	p.process = info.Process
	return p
}

func (p *pseudoConsole) Write(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.out.Write(b)
}

func (p *pseudoConsole) output() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.out.String()
}

func (p *pseudoConsole) write(t *testing.T, keys string) {
	t.Helper()
	if _, err := p.input.WriteString(keys); err != nil {
		t.Fatal(err)
	}
}

func (p *pseudoConsole) waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		if event, _ := windows.WaitForSingleObject(p.process, 0); event == windows.WAIT_OBJECT_0 {
			t.Fatalf("the child exited before %s appeared\nconsole output: %q", path, p.output())
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s did not appear within 30s\nconsole output: %q", path, p.output())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (p *pseudoConsole) wait(t *testing.T) {
	t.Helper()
	event, err := windows.WaitForSingleObject(p.process, 30_000)
	if err != nil {
		t.Fatal(err)
	}
	if event != windows.WAIT_OBJECT_0 {
		t.Fatalf("the child did not exit within 30s\nconsole output: %q", p.output())
	}
	var code uint32
	if err := windows.GetExitCodeProcess(p.process, &code); err != nil {
		t.Fatal(err)
	}
	if code != 0 {
		t.Fatalf("the child exited with %d\nconsole output: %q", code, p.output())
	}
}
