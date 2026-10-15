//go:build linux

package codingagent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// processSession is the session id of a Linux process: field 6 of /proc/<pid>/stat, after the parenthesized command.
func processSession(t *testing.T, stat string) string {
	t.Helper()
	fields := strings.Fields(stat[strings.LastIndex(stat, ")")+1:])
	if len(fields) < 4 {
		t.Fatalf("unexpected stat %q", stat)
	}
	return fields[3]
}

// Pi's openBrowser spawns the launcher with `detached: true` (libuv setsid) and `stdio: "ignore"`, so a terminal
// interrupt aimed at Pi's process group never reaches the browser launcher, and nothing the launcher reads or writes
// touches the terminal. A stand-in xdg-open records its argument, its session id and whether stdin is the null device.
func TestOpenBrowserLaunchesADetachedLauncherWithIgnoredStdio(t *testing.T) {
	dir := t.TempDir()
	record := filepath.Join(dir, "record")
	script := "#!/bin/sh\nprintf '%s\\n' \"$1\" > " + record + ".tmp\ncut -d' ' -f6 /proc/$$/stat >> " + record + ".tmp\nreadlink /proc/$$/fd/0 >> " + record + ".tmp\nmv " + record + ".tmp " + record + "\n"
	if err := os.WriteFile(filepath.Join(dir, "xdg-open"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	target := "https://example.test/oauth?a=1&b=2|echo.INJECTED"
	if err := openBrowser(target); err != nil {
		t.Fatal(err)
	}
	var lines []string
	deadline := time.Now().Add(10 * time.Second)
	for {
		if data, err := os.ReadFile(record); err == nil {
			lines = strings.Split(strings.TrimSpace(string(data)), "\n")
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the launcher did not run")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(lines) != 3 || lines[0] != target {
		t.Fatalf("launcher record = %q, want the target as one argument", lines)
	}
	own, err := os.ReadFile("/proc/self/stat")
	if err != nil {
		t.Fatal(err)
	}
	if lines[1] == processSession(t, string(own)) {
		t.Fatalf("launcher session %s is PiG's own, want a new session (detached)", lines[1])
	}
	if lines[2] != "/dev/null" {
		t.Fatalf("launcher stdin = %q, want /dev/null (stdio ignore)", lines[2])
	}
}

// Upstream swallows a launcher that cannot start (a missing xdg-open): the target is still presented to the user.
func TestOpenBrowserLauncherFailureDoesNotPanic(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	OpenBrowser("https://example.test/")
}
