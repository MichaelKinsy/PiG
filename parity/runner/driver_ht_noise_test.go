//go:build parity

package runner

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeHTName is the executable name under which the runner test binary acts
// as an ht that writes noise to stderr around a fixed stdout.
const fakeHTName = "ht"

// runFakeHT echoes its arguments on stdout and writes warning noise on
// stderr; "fail" exits 3 with a diagnostic on stderr.
func runFakeHT(args []string) int {
	fmt.Fprintln(os.Stderr, "ht: warning: daemon socket is stale, restarting")
	if len(args) > 0 && args[0] == "fail" {
		fmt.Fprintln(os.Stderr, "ht: session not found")
		return 3
	}
	fmt.Println(strings.Join(args, " "))
	fmt.Fprintln(os.Stderr, "ht: warning: terminal size fell back to 80x24")
	return 0
}

// installFakeHT puts a copy of this test binary first on PATH as ht.
func installFakeHT(t *testing.T) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, fakeHTName), binary, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestHTOutputKeepsStderrNoiseOutOfCapturedScreen(t *testing.T) {
	installFakeHT(t)
	output, err := htOutput(t.Context(), "view", "session")
	if err != nil {
		t.Fatal(err)
	}
	if string(output) != "view session\n" {
		t.Fatalf("ht view output = %q, want only stdout %q", output, "view session\n")
	}
	if _, err := htOutput(t.Context(), "fail"); err == nil || !strings.Contains(err.Error(), "ht: session not found") {
		t.Fatalf("failing ht error = %v, want its stderr diagnostic", err)
	}
}
