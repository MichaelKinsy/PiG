package testenv

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// BashExecutionStringProbe writes a BASH_ENV script for a test that compares
// the -c command bash receives from Pi and from PiG. The script sends bash's
// stderr to the returned file, then prints BASH_EXECUTION_STRING, the -c
// argument as bash received it, as "<%s>\n". Pi and PiG merge a child's
// stdout and stderr pipes into one stream whose interleaving differs from run
// to run; with stderr in the file, that stream holds stdout in the order bash
// wrote it, and BashStderr reads stderr after each command.
func BashExecutionStringProbe(t testing.TB) (probe, stderr string) {
	t.Helper()
	dir := t.TempDir()
	probe = filepath.Join(dir, "execution-string.sh")
	stderr = filepath.ToSlash(filepath.Join(dir, "stderr.txt"))
	if strings.ContainsRune(stderr, '\'') {
		t.Fatalf("stderr path %q needs shell quoting", stderr)
	}
	script := "exec 2>'" + stderr + "'\n" + `printf '<%s>\n' "$BASH_EXECUTION_STRING"` + "\n"
	if err := os.WriteFile(probe, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	return probe, stderr
}

// BashStderr returns what the last bash that sourced a
// BashExecutionStringProbe script wrote to stderr.
func BashStderr(t testing.TB, stderr string) string {
	t.Helper()
	data, err := os.ReadFile(stderr)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
