//go:build windows

package subprocess

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

const winProcessStillActive = 259 // STILL_ACTIVE

type windowsHarnessEntrySpec struct {
	mode     string
	exitCode int
	args     []string
	env      []string
}

func newWindowsHarnessEntry(t *testing.T, spec windowsHarnessEntrySpec) (cmd *exec.Cmd, root, pidFile, argsFile string) {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skipf("node unavailable: %v", err)
	}
	root = t.TempDir()
	pidFile = filepath.Join(root, "binary.pid")
	argsFile = filepath.Join(root, "binary.args.json")
	binary := harnessSelfBinaryPath(t)
	cli, err := filepath.Abs(filepath.Join("runtime-node", "harness", "cli.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	cmd = exec.Command(node, append([]string{cli}, spec.args...)...)
	cmd.Env = append(os.Environ(),
		"PIG_HARNESS_BINARY="+binary,
		harnessTestHelperEnv+"="+spec.mode,
		harnessTestHelperPIDEnv+"="+pidFile,
		harnessTestHelperArgsEnv+"="+argsFile,
		harnessTestHelperExitEnv+"="+strconv.Itoa(spec.exitCode),
	)
	cmd.Env = append(cmd.Env, spec.env...)
	return cmd, root, pidFile, argsFile
}

// snapshotWindowsProcesses returns every process visible to CreateToolhelp32Snapshot.
func snapshotWindowsProcesses() ([]harnessProcessSnapshotEntry, error) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer func() { _ = windows.CloseHandle(snap) }()
	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	var out []harnessProcessSnapshotEntry
	for err = windows.Process32First(snap, &entry); err == nil; err = windows.Process32Next(snap, &entry) {
		out = append(out, harnessProcessSnapshotEntry{
			PID:  int(entry.ProcessID),
			PPID: int(entry.ParentProcessID),
			Name: windows.UTF16ToString(entry.ExeFile[:]),
		})
	}
	if !errors.Is(err, windows.ERROR_NO_MORE_FILES) {
		return out, err
	}
	return out, nil
}

func windowsProcessDiagnostics(focusPIDs ...int) string {
	entries, err := snapshotWindowsProcesses()
	if err != nil {
		return fmt.Sprintf("snapshot error: %v", err)
	}
	return formatHarnessProcessSnapshot(entries, focusPIDs...)
}

// openWindowsProcessHandle opens pid for wait + exit-code inspection and optional
// cleanup termination. Keeping the handle open prevents PID reuse while the test
// observes exit status.
func openWindowsProcessHandle(t *testing.T, pid int, what string) windows.Handle {
	t.Helper()
	if pid <= 0 {
		t.Fatalf("invalid %s pid %d", what, pid)
	}
	h, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		t.Fatalf("OpenProcess(%s pid=%d): %v; processes: %s", what, pid, err, windowsProcessDiagnostics(pid))
	}
	t.Cleanup(func() {
		_ = windows.TerminateProcess(h, 1)
		_ = windows.CloseHandle(h)
	})
	return h
}

func windowsHandleAlive(h windows.Handle) (alive bool, exitCode uint32, waitEvent uint32, err error) {
	ev, werr := windows.WaitForSingleObject(h, 0)
	if werr != nil {
		return false, 0, ev, werr
	}
	var code uint32
	if gerr := windows.GetExitCodeProcess(h, &code); gerr != nil {
		return ev == uint32(windows.WAIT_TIMEOUT), 0, ev, gerr
	}
	return ev == uint32(windows.WAIT_TIMEOUT) || code == winProcessStillActive, code, ev, nil
}

func windowsPIDAlive(pid int) (bool, uint32) {
	h, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false, 0
	}
	defer func() { _ = windows.CloseHandle(h) }()
	alive, code, _, err := windowsHandleAlive(h)
	if err != nil {
		return false, 0
	}
	return alive, code
}

func awaitWindowsHandleExit(t *testing.T, h windows.Handle, pid int, what string, focusPIDs ...int) (uint32, time.Duration) {
	t.Helper()
	budget := testbudget.Wait(t)
	start := time.Now()
	for {
		alive, code, ev, err := windowsHandleAlive(h)
		if err != nil {
			t.Fatalf("inspect %s (pid=%d) after %s: %v (waitEvent=%#x); processes: %s",
				what, pid, time.Since(start).Round(time.Millisecond), err, ev, windowsProcessDiagnostics(append(focusPIDs, pid)...))
		}
		if !alive {
			return code, time.Since(start)
		}
		if time.Since(start) > budget {
			t.Fatalf("%s (pid=%d) was still alive %s after trigger (waitEvent=%#x exitCode=%d); processes: %s",
				what, pid, time.Since(start).Round(time.Millisecond), ev, code, windowsProcessDiagnostics(append(focusPIDs, pid)...))
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func awaitWindowsNoChildren(t *testing.T, what string, parentPIDs ...int) {
	t.Helper()
	budget := testbudget.Wait(t)
	start := time.Now()
	for {
		entries, err := snapshotWindowsProcesses()
		if err != nil {
			t.Fatalf("CreateToolhelp32Snapshot while checking %s: %v", what, err)
		}
		var live []harnessProcessSnapshotEntry
		for _, child := range harnessChildrenByParent(entries, parentPIDs...) {
			if alive, _ := windowsPIDAlive(child.PID); alive {
				live = append(live, child)
			}
		}
		if len(live) == 0 {
			return
		}
		if time.Since(start) > budget {
			t.Fatalf("%s still had live child process(es) after %s: %+v; full focus snapshot: %s",
				what, time.Since(start).Round(time.Millisecond), live, formatHarnessProcessSnapshot(entries, parentPIDs...))
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func harnessSupervisorWindows(t *testing.T, binaryPID, entryPID int) int {
	t.Helper()
	entries, err := snapshotWindowsProcesses()
	if err != nil {
		t.Fatalf("CreateToolhelp32Snapshot: %v", err)
	}
	parent := harnessParentFromSnapshot(entries, binaryPID)
	if parent <= 0 || parent == entryPID {
		t.Fatalf("binary (pid=%d) parent is pid=%d while entry is pid=%d: expected supervisor process between entry and binary; snapshot: %s",
			binaryPID, parent, entryPID, formatHarnessProcessSnapshot(entries, entryPID, binaryPID))
	}
	supervisorParent := harnessParentFromSnapshot(entries, parent)
	if supervisorParent != entryPID {
		t.Logf("note: supervisor (pid=%d) parent is pid=%d (entry pid=%d); snapshot: %s",
			parent, supervisorParent, entryPID, formatHarnessProcessSnapshot(entries, entryPID, parent, binaryPID))
	}
	return parent
}

// TestHarnessEntryTerminateEndsItsBinary verifies that terminating the harness
// entry on Windows via TerminateProcess ((*os.Process).Kill) closes the IPC
// channel to supervisor.mjs and ends the extension binary (and supervisor)
// within testbudget.Wait(t).
func TestHarnessEntryTerminateEndsItsBinary(t *testing.T) {
	var stderr bytes.Buffer
	cmd, _, pidFile, argsFile := newWindowsHarnessEntry(t, windowsHarnessEntrySpec{
		mode: "wait",
		args: []string{"--mode", "rpc"},
	})
	cmd.Stderr = &stderr
	startHarnessEntry(t, cmd)
	entryPID := cmd.Process.Pid
	entryEnded := harnessEntryEnds(cmd)

	binaryPID := waitHarnessChildPID(t, pidFile)
	binaryHandle := openWindowsProcessHandle(t, binaryPID, "binary")
	supervisorPID := harnessSupervisorWindows(t, binaryPID, entryPID)
	supervisorHandle := openWindowsProcessHandle(t, supervisorPID, "supervisor")

	rawArgs, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("read args file %s: %v", argsFile, err)
	}
	var gotArgs []string
	if err := json.Unmarshal(rawArgs, &gotArgs); err != nil {
		t.Fatalf("unmarshal args %q: %v", rawArgs, err)
	}
	if !slices.Equal(gotArgs, []string{"--mode", "rpc"}) {
		t.Fatalf("binary args = %q, want [--mode rpc]", gotArgs)
	}

	killStart := time.Now()
	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("TerminateProcess on entry (pid=%d): %v", entryPID, err)
	}

	budget := testbudget.Wait(t)
	var entryErr error
	select {
	case entryErr = <-entryEnded:
	case <-time.After(budget):
		t.Fatalf("entry (pid=%d) did not exit %s after TerminateProcess; processes: %s; stderr: %q",
			entryPID, budget, windowsProcessDiagnostics(entryPID, supervisorPID, binaryPID), stderr.String())
	}
	var exitErr *exec.ExitError
	if !errors.As(entryErr, &exitErr) {
		t.Fatalf("entry (pid=%d) wait result after TerminateProcess = %v, want *exec.ExitError; stderr: %q",
			entryPID, entryErr, stderr.String())
	}

	binExitCode, binElapsed := awaitWindowsHandleExit(t, binaryHandle, binaryPID, "binary", entryPID, supervisorPID)
	supExitCode, supElapsed := awaitWindowsHandleExit(t, supervisorHandle, supervisorPID, "supervisor", entryPID, binaryPID)
	awaitWindowsNoChildren(t, fmt.Sprintf("terminated entry pid=%d / supervisor pid=%d", entryPID, supervisorPID), entryPID, supervisorPID)

	t.Logf("entry(pid=%d, exit=%d) terminated; binary(pid=%d, exit=%d) gone in %s; supervisor(pid=%d, exit=%d) gone in %s (total %s)",
		entryPID, exitErr.ExitCode(),
		binaryPID, binExitCode, binElapsed.Round(time.Millisecond),
		supervisorPID, supExitCode, supElapsed.Round(time.Millisecond),
		time.Since(killStart).Round(time.Millisecond),
	)
}

// TestHarnessEntryKeepsBinaryExitStatusWindows verifies that a non-zero exit
// code (7) from the binary passes cleanly through supervisor.mjs and cli.mjs on
// Windows and leaves neither binary nor supervisor behind.
func TestHarnessEntryKeepsBinaryExitStatusWindows(t *testing.T) {
	var stderr bytes.Buffer
	cmd, _, pidFile, _ := newWindowsHarnessEntry(t, windowsHarnessEntrySpec{
		mode:     "exit",
		exitCode: 7,
	})
	cmd.Stderr = &stderr
	startHarnessEntry(t, cmd)
	entryPID := cmd.Process.Pid
	entryEnded := harnessEntryEnds(cmd)

	binaryPID := waitHarnessChildPID(t, pidFile)
	budget := testbudget.Wait(t)
	var err error
	select {
	case err = <-entryEnded:
	case <-time.After(budget):
		t.Fatalf("entry (pid=%d) did not exit %s after binary (pid=%d) exited 7; processes: %s; stderr: %q",
			entryPID, budget, binaryPID, windowsProcessDiagnostics(entryPID, binaryPID), stderr.String())
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 7 {
		t.Fatalf("entry (pid=%d) exit = %v (code %d), want exit code 7; stderr: %q",
			entryPID, err, cmd.ProcessState.ExitCode(), stderr.String())
	}
	if alive, code := windowsPIDAlive(binaryPID); alive {
		t.Fatalf("binary (pid=%d) still alive (code=%d) after entry exited 7; processes: %s",
			binaryPID, code, windowsProcessDiagnostics(entryPID, binaryPID))
	}
	awaitWindowsNoChildren(t, fmt.Sprintf("exited entry pid=%d", entryPID), entryPID)
}

// TestHarnessEntryNormalExitLeavesNoProcess verifies that a normal exit (code 0)
// relays stdin/stdout/stderr and arguments, exits 0, and leaves no child or
// supervisor process running on Windows.
func TestHarnessEntryNormalExitLeavesNoProcess(t *testing.T) {
	var stdout, stderr bytes.Buffer
	cmd, _, pidFile, _ := newWindowsHarnessEntry(t, windowsHarnessEntrySpec{
		mode:     "stdio-exit",
		exitCode: 0,
		args:     []string{"--mode", "rpc x"},
	})
	cmd.Stdin = strings.NewReader("hello-windows\n")
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	startHarnessEntry(t, cmd)
	entryPID := cmd.Process.Pid
	entryEnded := harnessEntryEnds(cmd)

	binaryPID := waitHarnessChildPID(t, pidFile)
	budget := testbudget.Wait(t)
	select {
	case err := <-entryEnded:
		if err != nil {
			t.Fatalf("entry (pid=%d) exit = %v, want nil (code 0); stdout=%q stderr=%q; processes: %s",
				entryPID, err, stdout.String(), stderr.String(), windowsProcessDiagnostics(entryPID, binaryPID))
		}
	case <-time.After(budget):
		t.Fatalf("entry (pid=%d) did not exit %s after binary (pid=%d) normal exit; stdout=%q stderr=%q; processes: %s",
			entryPID, budget, binaryPID, stdout.String(), stderr.String(), windowsProcessDiagnostics(entryPID, binaryPID))
	}

	if got, want := strings.ReplaceAll(stdout.String(), "\r\n", "\n"), "out:hello-windows|args:--mode,rpc x\n"; got != want {
		t.Fatalf("stdout = %q, want %q (stderr=%q)", got, want, stderr.String())
	}
	if got, want := strings.ReplaceAll(stderr.String(), "\r\n", "\n"), "err:hello-windows\n"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
	if alive, code := windowsPIDAlive(binaryPID); alive {
		t.Fatalf("binary (pid=%d) still alive (code=%d) after normal entry exit; processes: %s",
			binaryPID, code, windowsProcessDiagnostics(entryPID, binaryPID))
	}
	awaitWindowsNoChildren(t, fmt.Sprintf("normally exited entry pid=%d", entryPID), entryPID)
}
