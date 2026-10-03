package extensionconformance

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// The event bus realms share one log file, so a Python realm's line must land intact between lines other processes append at the same time. Python's own append mode does not give that on Windows, where the C runtime seeks to the end and then writes, and the crossing-emitters scenario lost lines to it.
func TestPythonBusFixtureAppendKeepsEveryLineWhenProcessesAppendConcurrently(t *testing.T) {
	python := requireConformanceTool(t, testPythonExecutable())
	const writers, linesPerWriter = 4, 300
	padding := strings.Repeat("x", 200)
	log := filepath.Join(t.TempDir(), "shared.log")
	script := "import os, sys\n" + pythonBusAppendLine + "\n" +
		fmt.Sprintf("for i in range(%d):\n    append_line(sys.argv[1], f\"{sys.argv[2]}|{i}|%s\\n\".encode(\"utf-8\"))\n", linesPerWriter, padding)

	var wg sync.WaitGroup
	errs := make(chan error, writers+1)
	for writer := range writers {
		wg.Go(func() {
			out, err := exec.Command(python, "-c", script, log, fmt.Sprintf("py%d", writer)).CombinedOutput()
			if err != nil {
				errs <- fmt.Errorf("python writer %d: %w\n%s", writer, err, out)
			}
		})
	}
	// The Go, Rust, and Node fixtures append through the platform's atomic append; one of them runs against the Python writers.
	wg.Go(func() {
		for i := range linesPerWriter {
			f, err := os.OpenFile(log, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
			if err != nil {
				errs <- err
				return
			}
			_, err = fmt.Fprintf(f, "go|%d|%s\n", i, padding)
			if closeErr := f.Close(); err == nil {
				err = closeErr
			}
			if err != nil {
				errs <- err
				return
			}
		}
	})
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}

	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]int{}
	for line := range strings.SplitSeq(strings.TrimSuffix(string(data), "\n"), "\n") {
		seen[line]++
	}
	for _, writer := range []string{"py0", "py1", "py2", "py3", "go"} {
		for i := range linesPerWriter {
			line := fmt.Sprintf("%s|%d|%s", writer, i, padding)
			if seen[line] != 1 {
				t.Fatalf("line %s|%d appears %d times in the shared log, want once", writer, i, seen[line])
			}
		}
	}
	if want := (writers + 1) * linesPerWriter; len(seen) != want {
		t.Fatalf("shared log holds %d distinct lines, want %d", len(seen), want)
	}
}
