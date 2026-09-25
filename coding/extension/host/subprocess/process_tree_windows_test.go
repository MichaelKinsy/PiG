//go:build windows

package subprocess

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestWindowsProcessTreeCancellationDuringAssignmentKillsDescendants(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "escaped-child")
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, "cmd.exe", windowsEscapingDescendantArgs(t, marker)...)
	ops := defaultWindowsProcessTreeOps()
	killAttempted := make(chan struct{})
	cancelObserved := false
	ops.killAttempted = killAttempted
	ops.afterStart = func() {
		cancel()
		select {
		case <-killAttempted:
			cancelObserved = true
		case <-time.After(5 * time.Second):
		}
	}
	tree, err := startProcessTreeWithWindowsOps(cmd, ops)
	if err != nil {
		t.Fatal(err)
	}
	if !cancelObserved {
		_ = tree.Close()
		_ = cmd.Wait()
		t.Fatal("CommandContext did not attempt cancellation during job assignment")
	}
	defer func() { _ = tree.Close() }()
	waitForWindowsCommand(t, cmd)
	time.Sleep(4 * time.Second)
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("descendant escaped cancelled Windows job: %v", err)
	}
}

func TestWindowsProcessTreeAssignmentFailureKillsSuspendedChild(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "resumed")
	cmd := exec.CommandContext(context.Background(), "cmd.exe", "/d", "/s", "/c", `echo resumed>"`+marker+`"`)
	ops := defaultWindowsProcessTreeOps()
	ops.assign = func(windows.Handle, windows.Handle) error { return errors.New("injected assignment failure") }

	done := make(chan error, 1)
	go func() {
		_, err := startProcessTreeWithWindowsOps(cmd, ops)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil || err.Error() != "injected assignment failure" {
			t.Fatalf("assignment failure = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("assignment failure left the suspended child alive or blocked in Wait")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("suspended child ran before assignment: %v", err)
	}
}

func TestWindowsEscapingDescendantFixtureWritesMarker(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "negative-control")
	cmd := exec.Command("cmd.exe", windowsEscapingDescendantArgs(t, marker)...)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	// No job owns this negative control, so cmd.exe's own kill would leave
	// its descendants, including the endless ping, running after the test.
	defer func() {
		_ = exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid)).Run()
		_ = cmd.Wait()
	}()
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(marker); err == nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("descendant fixture never wrote its marker; cancellation coverage would be vacuous")
}

// windowsEscapingDescendantArgs returns cmd.exe arguments that start a
// background descendant which writes marker after about three seconds while
// the parent keeps running. The descendant's work lives in a script beside
// marker: Go escapes each argument for the C runtime, which cmd.exe does not
// parse, so the command line must not need nested quotes.
func windowsEscapingDescendantArgs(t *testing.T, marker string) []string {
	t.Helper()
	script := filepath.Join(filepath.Dir(marker), "escaping-descendant.cmd")
	body := "@echo off\r\nping -n 4 127.0.0.1 >NUL\r\necho escaped>\"" + marker + "\"\r\n"
	if err := os.WriteFile(script, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return []string{"/d", "/c", "start", "", "/b", "cmd.exe", "/d", "/c", script, "&", "ping", "-t", "127.0.0.1", ">NUL"}
}

func waitForWindowsCommand(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("cancelled Windows process tree did not exit")
	}
}
