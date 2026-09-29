package codingagent

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/MichaelKinsy/PiG/tui"
)

const consoleEditorResultEnv = "PIG_CODINGAGENT_CONSOLE_EDITOR_RESULT"

// Pi ProcessTerminal.stop removes its input listener before editInExternalEditor inherits stdin. Windows interactive mode requires a console (resolveAppMode), so a child process attached to a pseudo console runs the reader on its console input. Pause must end the reader's console wait without a keystroke, the editor must read the next input directly from the console, and resume must deliver input again.
func TestInteractiveTerminalReaderHandsConsoleInputToExternalEditor(t *testing.T) {
	if result := os.Getenv(consoleEditorResultEnv); result != "" {
		handConsoleToExternalEditor(t, result)
		return
	}
	result := filepath.Join(t.TempDir(), "editor")
	t.Setenv(consoleEditorResultEnv, result)
	console := startInPseudoConsole(t, os.Args[0], "-test.run=^TestInteractiveTerminalReaderHandsConsoleInputToExternalEditor$", "-test.count=1")
	for _, step := range []struct{ ready, keys string }{{".ready", "before"}, {".paused", "child"}, {".resumed", "after"}} {
		console.waitForFile(t, result+step.ready)
		console.write(t, step.keys)
	}
	console.wait(t)
	got, err := os.ReadFile(result)
	if err != nil {
		t.Fatalf("read the helper result: %v\nconsole output: %q", err, console.output())
	}
	if string(got) != "before|child|after" {
		t.Fatalf("reader, editor, reader received %q", got)
	}
}

// handConsoleToExternalEditor runs in the child process attached to the pseudo console.
func handConsoleToExternalEditor(t *testing.T, result string) {
	restore, err := tui.EnterRawMode()
	if err != nil {
		t.Fatal(err)
	}
	defer restore()
	reader := newInteractiveTerminalReader(t.Context(), os.Stdin)
	decoded := &decodedReaderInput{reader: reader}
	decoded.input = tui.NewTerminalInput(func(sequence string) { decoded.typed = append(decoded.typed, sequence...) })
	signal := func(name string) {
		if err := os.WriteFile(result+name, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	signal(".ready")
	before := decoded.receive(t, len("before"))
	pauseWithin(t, reader)
	signal(".paused")
	// Keys typed while the editor starts wait in the console for it. A reader that pause left running would take them.
	waitForConsoleInput(t, 2*len("child"))
	child := readConsoleAsEditor(t, len("child"))
	reader.resume()
	signal(".resumed")
	after := decoded.receive(t, len("after"))
	pauseWithin(t, reader)
	if err := os.WriteFile(result, []byte(before+"|"+child+"|"+after), 0o600); err != nil {
		t.Fatal(err)
	}
}

// pauseWithin fails when pause does not end the reader's wait on the console. A reader still blocked would consume the editor's input.
func pauseWithin(t *testing.T, reader *interactiveTerminalReader) {
	t.Helper()
	paused := make(chan struct{})
	go func() {
		reader.pause()
		close(paused)
	}()
	select {
	case <-paused:
	case <-time.After(5 * time.Second):
		t.Fatal("pause did not end the reader's console wait")
	}
}

// decodedReaderInput decodes the reader's chunks with TerminalInput, as the interactive input loop does. TerminalInput consumes the terminal's answers to the keyboard protocol query that raw mode sends; the Windows 10 pseudo console answers the Device Attributes query itself with ESC [ ? 1 ; 0 c ahead of any typed key.
type decodedReaderInput struct {
	reader *interactiveTerminalReader
	input  *tui.TerminalInput
	typed  []byte
}

func (d *decodedReaderInput) receive(t *testing.T, n int) string {
	t.Helper()
	for len(d.typed) < n {
		select {
		case data := <-d.reader.data:
			d.input.Process(data)
		case <-d.input.C:
			d.input.Flush()
		case err := <-d.reader.errors:
			t.Fatalf("reader error after %q: %v", d.typed, err)
		case <-time.After(10 * time.Second):
			t.Fatalf("the reader delivered %q", d.typed)
		}
	}
	got := string(d.typed)
	d.typed = nil
	return got
}

// waitForConsoleInput waits until the console input buffer holds at least n records, one press and one release per typed key.
func waitForConsoleInput(t *testing.T, n int) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		var events uint32
		if err := windows.GetNumberOfConsoleInputEvents(windows.Handle(os.Stdin.Fd()), &events); err != nil {
			t.Fatal(err)
		}
		if int(events) >= n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the console holds %d input records, want %d: another reader took the editor's input", events, n)
		}
	}
}

// readConsoleAsEditor reads the console handle directly, as the external editor process that inherits it does.
func readConsoleAsEditor(t *testing.T, n int) string {
	t.Helper()
	got := make(chan string, 1)
	go func() {
		var text []uint16
		buf := make([]uint16, 16)
		for len(text) < n {
			var read uint32
			if err := windows.ReadConsole(windows.Handle(os.Stdin.Fd()), &buf[0], uint32(len(buf)), &read, nil); err != nil {
				got <- "error: " + err.Error()
				return
			}
			text = append(text, buf[:read]...)
		}
		got <- string(utf16.Decode(text))
	}()
	select {
	case s := <-got:
		return s
	case <-time.After(10 * time.Second):
		t.Fatal("the editor did not receive the input typed after pause")
		return ""
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

// startInPseudoConsole starts argv attached to a new pseudo console, as a terminal emulator starts a shell, and drains the console's output. It mirrors the tui package's console test helper.
func startInPseudoConsole(t *testing.T, argv ...string) *pseudoConsole {
	t.Helper()
	var inRead, inWrite, outRead, outWrite windows.Handle
	if err := windows.CreatePipe(&inRead, &inWrite, nil, 0); err != nil {
		t.Fatal(err)
	}
	if err := windows.CreatePipe(&outRead, &outWrite, nil, 0); err != nil {
		t.Fatal(err)
	}
	p := &pseudoConsole{input: os.NewFile(uintptr(inWrite), "pseudo-console-input"), drained: make(chan struct{})}
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
	if err := attributes.Update(windows.PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE, *(*unsafe.Pointer)(unsafe.Pointer(&p.console)), unsafe.Sizeof(p.console)); err != nil { //nolint:gosec // G103: UpdateProcThreadAttribute takes the HPCON value in its pointer argument, and x/sys takes that value as unsafe.Pointer.
		t.Fatal(err)
	}
	var startup windows.StartupInfoEx
	startup.Cb = uint32(unsafe.Sizeof(startup))
	// Null standard handles make the child use the pseudo console even when this test's own output is redirected.
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
