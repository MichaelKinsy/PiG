//go:build windows

package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// A pseudoconsole is Windows' pseudo-terminal. The size is wider than the 80
// columns the Unix helpers use so that ConPTY does not re-wrap the startup
// listing's long Windows paths.
const (
	conPTYColumns = 160
	conPTYRows    = 40
)

// conPTYProcess is a process attached to a pseudoconsole. input is the
// keyboard side and output is the screen side of the terminal.
type conPTYProcess struct {
	input, output *os.File
	console       windows.Handle
	process       windows.Handle
}

// startOnConPTY starts commandLine (a complete Windows command line) with a
// pseudoconsole as its console and standard streams. env is the child's
// environment and dir its working directory.
func startOnConPTY(t *testing.T, commandLine string, env []string, dir string) *conPTYProcess {
	t.Helper()
	var inputRead, inputWrite, outputRead, outputWrite windows.Handle
	if err := windows.CreatePipe(&inputRead, &inputWrite, nil, 0); err != nil {
		t.Fatalf("create terminal input pipe: %v", err)
	}
	if err := windows.CreatePipe(&outputRead, &outputWrite, nil, 0); err != nil {
		t.Fatalf("create terminal output pipe: %v", err)
	}
	var console windows.Handle
	err := windows.CreatePseudoConsole(windows.Coord{X: conPTYColumns, Y: conPTYRows}, inputRead, outputWrite, 0, &console)
	// The pseudoconsole owns its ends of the pipes from here.
	_ = windows.CloseHandle(inputRead)
	_ = windows.CloseHandle(outputWrite)
	if err != nil {
		_ = windows.CloseHandle(inputWrite)
		_ = windows.CloseHandle(outputRead)
		t.Fatalf("create pseudoconsole: %v", err)
	}
	attributes, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		t.Fatalf("allocate process attribute list: %v", err)
	}
	defer attributes.Delete()
	// The attribute value is the HPCON itself, not a pointer to it.
	if err := attributes.Update(windows.PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE, *(*unsafe.Pointer)(unsafe.Pointer(&console)), unsafe.Sizeof(console)); err != nil { //nolint:gosec // G103: UpdateProcThreadAttribute takes the HPCON value in its pointer argument, and x/sys takes that value as unsafe.Pointer.
		t.Fatalf("attach pseudoconsole: %v", err)
	}
	commandLineUTF16, err := windows.UTF16PtrFromString(commandLine)
	if err != nil {
		t.Fatal(err)
	}
	dirUTF16, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		t.Fatal(err)
	}
	startup := windows.StartupInfoEx{ProcThreadAttributeList: attributes.List()}
	startup.Cb = uint32(unsafe.Sizeof(startup))
	// Null standard handles make the child use the pseudoconsole even when this test's own streams are redirected.
	startup.Flags = windows.STARTF_USESTDHANDLES
	var info windows.ProcessInformation
	err = windows.CreateProcess(nil, commandLineUTF16, nil, nil, false,
		windows.EXTENDED_STARTUPINFO_PRESENT|windows.CREATE_UNICODE_ENVIRONMENT,
		environmentBlock(env), dirUTF16, &startup.StartupInfo, &info)
	if err != nil {
		windows.ClosePseudoConsole(console)
		_ = windows.CloseHandle(inputWrite)
		_ = windows.CloseHandle(outputRead)
		t.Fatalf("start %q on a pseudoconsole: %v", commandLine, err)
	}
	_ = windows.CloseHandle(info.Thread)
	return &conPTYProcess{
		input:   os.NewFile(uintptr(inputWrite), "conpty-input"),
		output:  os.NewFile(uintptr(outputRead), "conpty-output"),
		console: console,
		process: info.Process,
	}
}

// environmentBlock encodes env as the double-NUL-terminated UTF-16 block
// CreateProcess takes with CREATE_UNICODE_ENVIRONMENT.
func environmentBlock(env []string) *uint16 {
	var block []uint16
	for _, entry := range env {
		block = append(block, windows.StringToUTF16(entry)...)
	}
	return &append(block, 0)[0]
}

// wait blocks until the process exits or timeout elapses, and returns its exit
// code. A process still running at timeout is reported as not exited.
func (p *conPTYProcess) wait(timeout time.Duration) (code int, exited bool, err error) {
	event, err := windows.WaitForSingleObject(p.process, uint32(timeout.Milliseconds()))
	if err != nil {
		return 0, false, err
	}
	if event == uint32(windows.WAIT_TIMEOUT) {
		return 0, false, nil
	}
	var exitCode uint32
	if err := windows.GetExitCodeProcess(p.process, &exitCode); err != nil {
		return 0, true, err
	}
	return int(exitCode), true, nil
}

// close terminates the process if it still runs and releases the
// pseudoconsole. ConPTY can block closing until its output is read, so the
// output is drained while it closes.
func (p *conPTYProcess) close() {
	_ = windows.TerminateProcess(p.process, 1)
	_, _ = windows.WaitForSingleObject(p.process, windows.INFINITE)
	_ = windows.CloseHandle(p.process)
	go func() { _, _ = io.Copy(io.Discard, p.output) }()
	windows.ClosePseudoConsole(p.console)
	_ = p.input.Close()
	_ = p.output.Close()
}

// conPTYTerminal reads the terminal's screen output and writes its keyboard
// input.
type conPTYTerminal struct{ *conPTYProcess }

func (t conPTYTerminal) Read(p []byte) (int, error)  { return t.output.Read(p) }
func (t conPTYTerminal) Write(p []byte) (int, error) { return t.input.Write(p) }

// runPigOnTerminalStdinToFile runs pig the way `pig "q" > out.txt` runs from a
// terminal: stdin is a console and stdout is the file at outPath. The
// redirection comes from cmd.exe running inside the pseudoconsole, as it does
// in a console window; stderr is redirected to a file as well so that the
// terminal carries only stdin.
func runPigOnTerminalStdinToFile(t *testing.T, bin, outPath string, args ...string) (string, int) {
	t.Helper()
	cmd := modePigCommand(t.Context(), t, bin, t.TempDir(), args...)
	stderrPath := outPath + ".stderr"
	commandLine := fmt.Sprintf(`cmd.exe /d /s /c "%s > "%s" 2> "%s""`, windows.ComposeCommandLine(cmd.Args), outPath, stderrPath)
	process := startOnConPTY(t, commandLine, cmd.Environ(), cmd.Dir)
	defer process.close()
	go func() { _, _ = io.Copy(io.Discard, process.output) }()
	code, exited, err := process.wait(testbudget.Wait(t))
	if err != nil || !exited {
		t.Fatalf("pig %v on a console stdin did not exit: exited=%v err=%v", args, exited, err)
	}
	stderr, _ := os.ReadFile(stderrPath)
	return string(stderr), code
}

// startPigOnTerminal starts cmd on a pseudoconsole, which is its console and
// standard streams. It returns the terminal and a function that terminates the
// process and releases the pseudoconsole. cmd's Stdin, Stdout and Stderr are
// not used.
func startPigOnTerminal(t *testing.T, cmd *exec.Cmd) (io.ReadWriter, func()) {
	t.Helper()
	path := cmd.Path
	if !filepath.IsAbs(path) {
		resolved, err := exec.LookPath(path)
		if err != nil {
			t.Fatal(err)
		}
		path = resolved
	}
	args := append([]string{path}, cmd.Args[1:]...)
	process := startOnConPTY(t, windows.ComposeCommandLine(args), cmd.Environ(), cmd.Dir)
	return conPTYTerminal{process}, process.close
}
