//go:build !pig_strip_node_extensions

package subprocess

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// stalledStderr replaces PiG's stderr with a pipe that is full and has no reader, so a write of an extension's output to the terminal blocks, as a stalled terminal or log disk does at the moment pig exits. release starts the reader after stall. finish closes the write end, which is what the exit of the process does to a copy that is still blocked, and returns everything the pipe carried.
func stalledStderr(t *testing.T) (release func(stall time.Duration), finish func() string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("a pipe that is full needs deadlines on os.Pipe")
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	fill := func(chunk []byte) {
		for {
			_ = writer.SetWriteDeadline(time.Now().Add(50 * time.Millisecond))
			if _, err := writer.Write(chunk); err != nil {
				return
			}
		}
	}
	fill(make([]byte, 4096))
	fill([]byte{'.'})
	_ = writer.SetWriteDeadline(time.Time{})
	original := os.Stderr
	os.Stderr = writer
	var collected bytes.Buffer
	copied := make(chan struct{})
	started := make(chan struct{})
	go func() {
		defer close(copied)
		<-started
		_, _ = io.Copy(&collected, reader)
	}()
	var startOnce sync.Once
	release = func(stall time.Duration) {
		time.AfterFunc(stall, func() { startOnce.Do(func() { close(started) }) })
	}
	var once sync.Once
	var out string
	restore := func() {
		once.Do(func() {
			os.Stderr = original
			_ = writer.Close()
			startOnce.Do(func() { close(started) })
			<-copied
			_ = reader.Close()
			out = collected.String()
		})
	}
	t.Cleanup(restore)
	return release, func() string { restore(); return out }
}

// writeNodeConsoleExtension is an extension whose factory writes one line to stderr and one to stdout (Pi's console.error and console.log) and then idles, as the extension of the docs event-bus page does just before the host ends.
func writeNodeConsoleExtension(t *testing.T, name string) string {
	t.Helper()
	source := filepath.Join(t.TempDir(), name+".mjs")
	body := `import fs from "node:fs";
export default function(pi) {
  fs.writeSync(2, "stderr line of ` + name + `\n");
  fs.writeSync(1, "stdout line of ` + name + `\n");
  setInterval(() => {}, 60000);
}`
	if err := os.WriteFile(source, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return source
}

// What an extension wrote before the host ended reaches PiG's terminal even when the terminal is slow: the host that kills its extension processes before PiG exits joins their output copy, so the exit cannot drop a line the extension already wrote. Pi writes in its own process, so a write made before its exit is never lost.
func TestTerminateProcessesDeliversTheOutputWrittenBeforeIt(t *testing.T) {
	for _, packed := range []bool{false, true} {
		t.Run(map[bool]string{false: "isolated", true: "packed"}[packed], func(t *testing.T) {
			release, finish := stalledStderr(t)
			h := NewHost(t.TempDir())
			t.Cleanup(func() { h.Shutdown("test complete") })
			var configs []ExtConfig
			for _, name := range []string{"one", "two"} {
				configs = append(configs, ExtConfig{Name: name, Source: writeNodeConsoleExtension(t, name), Enabled: true})
			}
			if packed {
				if _, errs := h.LoadAll(testbudget.Context(t), configs); len(errs) != 0 {
					t.Fatal(errs)
				}
			} else {
				for _, config := range configs {
					if _, err := h.Load(testbudget.Context(t), config); err != nil {
						t.Fatal(err)
					}
				}
			}
			release(200 * time.Millisecond)
			h.TerminateProcesses()
			got := finish()
			for _, name := range []string{"one", "two"} {
				for _, line := range []string{"stderr line of " + name + "\n", "stdout line of " + name + "\n"} {
					if !strings.Contains(got, line) {
						t.Errorf("terminal lacks %q after TerminateProcesses returned", line)
					}
				}
			}
		})
	}
}

// A descendant outside the process tree that keeps the output pipe open after its extension ended holds the exit only for the bound: TerminateProcesses returns when the bound passed, and one line on stderr says that the undelivered output is dropped.
func TestTerminateProcessesEndsAtTheBoundForAPipeThatStaysOpen(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs sleep")
	}
	collect := swapStd(t)
	h := NewHost(t.TempDir())
	h.waitDelay = 300 * time.Millisecond
	t.Cleanup(func() { h.Shutdown("test complete") })
	source := filepath.Join(t.TempDir(), "holder.mjs")
	body := `import { spawn } from "node:child_process";
export default function(pi) {
  spawn("sleep", ["4"], { detached: true, stdio: ["ignore", "inherit", "inherit"] }).unref();
  setInterval(() => {}, 60000);
}`
	if err := os.WriteFile(source, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Load(testbudget.Context(t), ExtConfig{Name: "holder", Source: source, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	h.TerminateProcesses()
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("TerminateProcesses waited %v for a pipe that stays open, want about the %v bound", elapsed, h.waitDelay)
	}
	_, stderr := collect()
	if want := "pig: 1 extension process(es) did not finish ending within 300ms; the output they have not yet delivered is dropped\n"; !strings.Contains(stderr, want) {
		t.Errorf("stderr = %q, want it to contain %q", stderr, want)
	}
}
